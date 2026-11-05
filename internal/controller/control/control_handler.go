// Package controller expone los endpoints HTTP del control automático /
// supervisión del MPC (SCA-400):
//
//	POST  /api/v1/lotes/{id}/control/start         (Supervisor/Admin)
//	POST  /api/v1/lotes/{id}/control/stop          (Supervisor/Admin)
//	PATCH /api/v1/lotes/{id}/control/parametros    (Supervisor/Admin)
//	PATCH /api/v1/lotes/{id}/modo                  (Supervisor/Admin)
//	GET   /api/v1/lotes/{id}/consigna-propuesta     (Bearer dispositivo o JWT)
//	POST  /api/v1/lotes/{id}/aplicar-consigna       (Operario mínimo o Bearer dispositivo)
//	POST  /api/v1/lotes/{id}/consigna/ack           (Bearer dispositivo)
//	GET   /api/v1/lotes/{id}/decisiones             (JWT cookie)
//	GET   /api/v1/lotes/{id}/control/estado         (JWT cookie)
//	GET   /api/v1/lotes/{id}/confianza-clases       (JWT cookie)
//
// El start/stop manipula el Loop del lote en memoria (Registry). El
// consigna-propuesta lo lee el ControlService de la Pi ENTRADA (que ya
// tenemos para telemetría). aplicar-consigna aprueba la propuesta de
// SUPERVISION (operario del panel o la propia Pi); consigna/ack confirma
// que la Pi efectivamente aplicó el setpoint.
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/helpers"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/control"
	loteSectorService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// ControlHandler agrupa los handlers del control.
type ControlHandler struct {
	deps ControlDeps
}

// LoopRegistry es el mapa en memoria de Loop por lote activo. El Loop se
// crea cuando entra un lote con modo != MANUAL y se cancela cuando el lote
// cierra (o se llama a stop explícitamente).
type LoopRegistry struct {
	mu       sync.Mutex
	loops    map[string]any // referencia al *control.Loop
	byCancel map[string]context.CancelFunc
	// gens evita que el watcher de un loop viejo borre la entrada de un loop
	// más nuevo registrado para el mismo lote.
	gens map[string]uint64
	// startedAt guarda cuándo se registró el loop vigente de cada lote.
	startedAt map[string]time.Time
}

func NewLoopRegistry() *LoopRegistry {
	return &LoopRegistry{
		loops:     map[string]any{},
		byCancel:  map[string]context.CancelFunc{},
		gens:      map[string]uint64{},
		startedAt: map[string]time.Time{},
	}
}

// MetricasRepo es el mínimo del repo de telemetría para alimentar el Loop.
type MetricasRepo interface {
	GetUltimasParaLote(ctx context.Context, loteID string, limite int) ([]metricashorno.MetricaHorno, error)
}

// EventosRepo es el mínimo del repo de lote para contar eventos y agregar la
// confianza de detección en la ventana.
type EventosRepo interface {
	CountEventosEnVentana(ctx context.Context, loteID string, desde, hasta time.Time) (ok, crudo, quemado int, err error)
	ConfianzaPromedioEnVentana(ctx context.Context, loteID string, desde, hasta time.Time) (promedio float64, muestras int, err error)
}

// ConfianzaRepo es el mínimo del repo de lote para agregar la confianza del
// modelo por clase sobre las últimas detecciones de un lote.
type ConfianzaRepo interface {
	ConfianzaPorClase(ctx context.Context, loteID string, limite int) ([]lotesector.ConfianzaClase, error)
}

// Deps que el handler necesita del lote_sector para configurar el loop.
// Las dependencias MetricasRepo, EventosRepo, ConfianzaRepo, DecisionesRepo y
// Broker son OBLIGATORIAS: el Loop las invoca dentro del tick o los handlers
// de lectura las consultan, y un nil ahí produce nil pointer dereference.
type ControlDeps struct {
	LoteLookup     LoteLookupService
	ProductoRepo   ProductoRepo
	Optimizer      *optimizador.Optimizer
	LoopCancels    *LoopRegistry
	MetricasRepo   MetricasRepo
	EventosRepo    EventosRepo
	ConfianzaRepo  ConfianzaRepo
	DecisionesRepo decisiones_control.Repository
	Broker         *sse.Broker
	// LoteRepo persiste cambios de estado/modo del lote (PATCH/PUT /lotes/{id}/modo).
	LoteRepo lotesector.Repository
	// DeviceSector valida que el PUT de la Pi afecte sólo lotes de su sector.
	DeviceSector DeviceSectorLookup
	// Modelos verifica si el sidecar tiene un modelo entrenado para el producto.
	// Nil = se omite el chequeo (tests / sidecar opcional).
	Modelos ModeloStatus
	// Alertas persiste alertas del loop (p.ej. timeout de precalentamiento).
	// Nil = el loop las omite.
	Alertas alerta.Repository
	// ConsignaRepo cierra la consigna encolada como APLICADA al recibir el ack
	// de la Pi. Nil = se omite (tests / despliegues sin historial).
	ConsignaRepo ConsignaAckRepo
}

// ConsignaAckRepo es el mínimo del repo de consignas que necesita el ack.
type ConsignaAckRepo interface {
	MarcarAplicadaPorDecision(ctx context.Context, decisionID string) error
}

// ModeloStatus es el mínimo del cliente del sidecar que necesita el handler.
type ModeloStatus interface {
	ModeloEntrenado(ctx context.Context, productoID string) (bool, error)
}

// DeviceSectorLookup resuelve el sector de la Pi autenticada.
type DeviceSectorLookup interface {
	SectorDelDispositivo(ctx context.Context, deviceID string) (*loteSectorService.SectorInfo, error)
}

// ProductoRepo es el mínimo del repo de productos para el handler.
type ProductoRepo interface {
	GetByID(ctx context.Context, id string) (*producto.Producto, error)
}

// LoteLookupService es el mínimo que necesita el handler.
type LoteLookupService interface {
	GetByID(ctx context.Context, loteID string) (*lotesector.Lote, error)
}

// NewControlHandler construye el handler a partir de sus dependencias.
// Valida que todas las deps requeridas estén presentes: un nil se descubriría
// recién dentro de la goroutine del Loop como nil pointer dereference, así que
// fallamos temprano y ruidosamente.
func NewControlHandler(deps ControlDeps) *ControlHandler {
	required := []struct {
		name  string
		isNil bool
	}{
		{"LoteLookup", deps.LoteLookup == nil},
		{"ProductoRepo", deps.ProductoRepo == nil},
		{"Optimizer", deps.Optimizer == nil},
		{"LoopCancels", deps.LoopCancels == nil},
		{"MetricasRepo", deps.MetricasRepo == nil},
		{"EventosRepo", deps.EventosRepo == nil},
		{"ConfianzaRepo", deps.ConfianzaRepo == nil},
		{"DecisionesRepo", deps.DecisionesRepo == nil},
		{"Broker", deps.Broker == nil},
		{"LoteRepo", deps.LoteRepo == nil},
		{"DeviceSector", deps.DeviceSector == nil},
	}
	for _, r := range required {
		if r.isNil {
			panic(fmt.Errorf("NewControlHandler: required dep %s is nil", r.name))
		}
	}
	return &ControlHandler{deps: deps}
}

// HandleControlStart maneja POST /api/v1/lotes/{id}/control/start.
// Crea un Loop en memoria si el modo != MANUAL y el horno lo requiere.
func (h *ControlHandler) HandleControlStart() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		// Sólo Supervisor/Admin pueden iniciar el control.
		if !helpers.RolValido(r, user.RoleSupervisor, user.RoleAdmin) {
			response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
			return
		}
		loteID, err := helpers.ExtractLoteID(r.URL.Path, "/control/start")
		if err != nil {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		lote, err := h.deps.LoteLookup.GetByID(r.Context(), loteID)
		if err != nil {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		if lote.Estado != lotesector.EstadoAbierto {
			response.Error(w, http.StatusConflict, "Lote no está abierto", nil)
			return
		}
		if lote.Modo == lotesector.ModoManual {
			response.Error(w, http.StatusConflict, "El lote está en modo MANUAL", nil)
			return
		}
		// Idempotente: si el loop ya está corriendo, no lo reiniciamos (un
		// reinicio perdería la fase de precalentamiento y volvería a mandar
		// velocidad 0).
		if err := h.EnsureLoopForLote(r.Context(), lote); err != nil {
			response.Error(w, http.StatusUnprocessableEntity, "Producto desconocido", nil)
			return
		}
		response.OK(w, "Control iniciado", map[string]interface{}{
			"lote_id":                        lote.ID,
			"modo":                           lote.Modo,
			"ventana_evaluacion_segundos":    lote.VentanaEvaluacionSegundos,
			"ventana_actualizacion_segundos": lote.VentanaActualizacionSegundos,
		})
	}
}

// EnsureLoopForLote arranca el loop del lote si corresponde (ABIERTO y modo no
// MANUAL) y todavía no hay uno registrado. Es idempotente.
func (h *ControlHandler) EnsureLoopForLote(ctx context.Context, lote *lotesector.Lote) error {
	return h.EnsureLoopForLoteEnFase(ctx, lote, "")
}

// EnsureLoopForLoteEnFase es como EnsureLoopForLote pero fuerza la fase inicial
// del loop (p.ej. ADAPTANDO al reanudar lotes abiertos tras un reinicio).
func (h *ControlHandler) EnsureLoopForLoteEnFase(ctx context.Context, lote *lotesector.Lote, fase string) error {
	if lote == nil || lote.Estado != lotesector.EstadoAbierto || lote.Modo == lotesector.ModoManual {
		return nil
	}
	if _, ok := h.deps.LoopCancels.GetLoop(lote.ID); ok {
		return nil
	}
	return h.startLoopForLote(ctx, lote, fase)
}

// startLoopForLote arranca un Loop de control para el lote con la configuración
// vigente (ventanas, pesos, modo). El registro cancela cualquier loop
// previo del mismo lote. El contexto del loop es propio (no el de la request):
// si se atara a r.Context(), el servidor lo cancelaría al responder y mataría
// el control (regresión F-B2). Devuelve error si el producto del lote no existe.
func (h *ControlHandler) startLoopForLote(ctx context.Context, lote *lotesector.Lote, faseInicial string) error {
	prod, err := h.deps.ProductoRepo.GetByID(ctx, lote.ProductoID)
	if err != nil {
		return err
	}
	hornoID := lote.SectorID
	if hornoID == "" {
		return fmt.Errorf("el lote %s no tiene sector asignado", lote.ID)
	}
	pesos := optimizador.PesosDesdeLote(lote.PesosObjetivo)
	limites := optimizador.LimitesDe(prod, lote)
	loopCtx, cancel := context.WithCancel(context.Background())
	loop := control.NewLoop(
		lote.ID, h.deps.LoteLookup,
		h.deps.MetricasRepo,
		h.deps.EventosRepo,
		h.deps.ProductoRepo,
		h.deps.Optimizer,
		h.deps.DecisionesRepo,
		h.deps.Broker,
	)
	loop.Configurar(
		prod.ID, lote.Modo, hornoID,
		lote.VentanaEvaluacionSegundos, lote.VentanaActualizacionSegundos,
		pesos, limites,
	)
	// Umbral de confianza de detección; un valor inválido conserva el default.
	confianzaMin := 0.5
	if raw := os.Getenv("MPC_CONFIANZA_MIN"); raw != "" {
		if v, perr := strconv.ParseFloat(raw, 64); perr == nil && v > 0 {
			confianzaMin = v
		}
	}
	loop.SetConfianzaMin(confianzaMin)
	// Sólo para demos/tesis sin modelo entrenado: permite que AUTOMATICO
	// opere con el placeholder. En producción queda bloqueado por default.
	if permitir, perr := strconv.ParseBool(os.Getenv("MPC_PERMITIR_PLACEHOLDER")); perr == nil {
		loop.SetPermitirPlaceholder(permitir)
	}
	if seg, perr := strconv.Atoi(os.Getenv("MPC_PRECALENTAMIENTO_TIMEOUT_SEGUNDOS")); perr == nil {
		loop.SetPreheatTimeout(seg)
	}
	// Poll corto sólo para demos/E2E: por defecto el loop reintenta cada 15 s
	// mientras espera la primera telemetría.
	if seg, perr := strconv.Atoi(os.Getenv("MPC_PREHEAT_POLL_SEGUNDOS")); perr == nil {
		loop.SetPreheatPoll(seg)
	}
	loop.SetAlertas(h.deps.Alertas)
	if faseInicial != "" {
		loop.SetFaseInicial(faseInicial)
	}
	h.deps.LoopCancels.Register(lote.ID, loopCtx, cancel)
	h.deps.LoopCancels.SetLoop(lote.ID, loop)
	loop.Start(loopCtx, cancel)
	return nil
}

// ResumeLotesAbiertos reanuda los loops de todos los lotes ABIERTO no MANUAL
// tras un reinicio del backend, arrancándolos en ADAPTANDO (el horno ya está en
// régimen) y dejando una fila de auditoría por lote. Idempotente: un lote que ya
// tiene loop registrado se saltea.
func (h *ControlHandler) ResumeLotesAbiertos(ctx context.Context) {
	lotes, err := h.deps.LoteRepo.ListAbiertos(ctx)
	if err != nil {
		log.Printf("[control] reanudando lotes abiertos: %v", err)
		return
	}
	for i := range lotes {
		lote := &lotes[i]
		if lote.Modo == lotesector.ModoManual {
			continue
		}
		if _, ok := h.deps.LoopCancels.GetLoop(lote.ID); ok {
			continue
		}
		if err := h.EnsureLoopForLoteEnFase(ctx, lote, control.FaseAdaptando); err != nil {
			log.Printf("[control] reanudando loop lote %s: %v", lote.ID, err)
			continue
		}
		h.auditarReanudacion(ctx, lote)
	}
}

// auditarReanudacion deja traza del reinicio. Es best-effort: un fallo de Save
// no frena la reanudación del resto de los lotes.
func (h *ControlHandler) auditarReanudacion(ctx context.Context, lote *lotesector.Lote) {
	skipped := "reanudado_tras_reinicio"
	cero := 0.0
	now := time.Now().UTC()
	decision := &decisiones_control.DecisionControl{
		LoteID:        lote.ID,
		VentanaInicio: now,
		VentanaFin:    now,
		Muestras:      0,
		TempActual:    0,
		// vel_actual es NOT NULL en la tabla: 0 como el resto de los skips.
		VelActual:    &cero,
		AplicadoPor:  decisiones_control.AplicadoPorNadie,
		RazonSkipped: &skipped,
	}
	if err := h.deps.DecisionesRepo.Save(ctx, decision); err != nil {
		log.Printf("[control] audit reanudación lote %s: %v", lote.ID, err)
	}
}

// HandleControlStop maneja POST /api/v1/lotes/{id}/control/stop.
// Cancela el loop en memoria. El lote sigue abierto (no se cierra); sólo
// se detiene el despacho automático.
func (h *ControlHandler) HandleControlStop() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		if !helpers.RolValido(r, user.RoleSupervisor, user.RoleAdmin) {
			response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
			return
		}
		loteID, err := helpers.ExtractLoteID(r.URL.Path, "/control/stop")
		if err != nil {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		if !h.deps.LoopCancels.StopLoop(loteID) {
			response.OK(w, "Sin loop activo", map[string]interface{}{"lote_id": loteID, "stopped": false, "motivo": "no_loop_activo"})
			return
		}
		response.OK(w, "Control detenido", map[string]interface{}{"lote_id": loteID, "stopped": true})
	}
}

// HandleControlEstado expone GET /api/v1/lotes/{id}/control/estado. Devuelve el
// modo persistido del lote y el estado runtime del loop en memoria: si está
// activo y desde cuándo. started_at viaja null cuando no hay loop.
func (h *ControlHandler) HandleControlEstado(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	if !helpers.RolValido(r, user.RoleAdmin, user.RoleSupervisor, user.RoleOperario) {
		response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
		return
	}
	loteID, err := helpers.ExtractLoteID(r.URL.Path, "/control/estado")
	if err != nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	lote, err := h.deps.LoteLookup.GetByID(r.Context(), loteID)
	if err != nil || lote == nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	var startedAt *time.Time
	if t, ok := h.deps.LoopCancels.StartedAt(loteID); ok {
		startedAt = &t
	}
	response.OK(w, "Estado del control", controlEstadoData{
		LoteID:     loteID,
		Modo:       lote.Modo,
		LoopActivo: startedAt != nil,
		StartedAt:  startedAt,
	})
}

// controlEstadoData es el payload de GET /lotes/{id}/control/estado.
type controlEstadoData struct {
	LoteID     string     `json:"lote_id"`
	Modo       string     `json:"modo"`
	LoopActivo bool       `json:"loop_activo"`
	StartedAt  *time.Time `json:"started_at"`
}

// Límites del parámetro `limite` de GET /lotes/{id}/confianza-clases.
const (
	confianzaClasesLimiteDefault = 200
	confianzaClasesLimiteMax     = 2000
)

// limiteConfianzaClases normaliza ?limite=. Ausente o no numérico cae al
// default; fuera de rango se clampa (el panel no debe recibir un 400 por pedir
// una página grande).
func limiteConfianzaClases(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return confianzaClasesLimiteDefault
	}
	if n < 1 {
		return 1
	}
	if n > confianzaClasesLimiteMax {
		return confianzaClasesLimiteMax
	}
	return n
}

// HandleConfianzaClases expone GET /api/v1/lotes/{id}/confianza-clases: la
// confianza media y el desvío por clase sobre las últimas N detecciones del
// lote. Sirve para detectar un modelo degradado por clase.
func (h *ControlHandler) HandleConfianzaClases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	if !helpers.RolValido(r, user.RoleAdmin, user.RoleSupervisor, user.RoleOperario) {
		response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
		return
	}
	loteID, err := helpers.ExtractLoteID(r.URL.Path, "/confianza-clases")
	if err != nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	if lote, lerr := h.deps.LoteLookup.GetByID(r.Context(), loteID); lerr != nil || lote == nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	clases, err := h.deps.ConfianzaRepo.ConfianzaPorClase(r.Context(), loteID, limiteConfianzaClases(r.URL.Query().Get("limite")))
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
		return
	}
	if clases == nil {
		clases = []lotesector.ConfianzaClase{}
	}
	response.OK(w, "Confianza por clase", map[string]interface{}{
		"lote_id": loteID,
		"clases":  clases,
		"total":   len(clases),
	})
}

// modoRequest es el body de PATCH /api/v1/lotes/{id}/modo.
type modoRequest struct {
	Modo string `json:"modo"`
}

// modoCambioData es el payload de respuesta del cambio de modo. En el no-op
// viaja `modo`; en el cambio real, `modo_anterior`/`modo_nuevo`/`loop_activo`.
type modoCambioData struct {
	LoteID       string `json:"lote_id"`
	Modo         string `json:"modo,omitempty"`
	ModoAnterior string `json:"modo_anterior,omitempty"`
	ModoNuevo    string `json:"modo_nuevo,omitempty"`
	Cambiado     bool   `json:"cambiado"`
	LoopActivo   bool   `json:"loop_activo"`
}

// HandleModeChange maneja PATCH (panel) y PUT (Pi) /api/v1/lotes/{id}/modo.
// Cambia el modo de operación de un lote ABIERTO (MANUAL/SUPERVISION/AUTOMATICO):
// cancela el loop vigente, persiste el modo, re-arranca el loop si corresponde
// y deja traza en decisiones_control.
//
// PATCH exige cookie JWT de Supervisor/Administrador. PUT es el push de la Pi
// (Bearer de dispositivo) y sólo puede afectar lotes de su propio sector.
func (h *ControlHandler) HandleModeChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPut {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	principal, isDevice := deviceauth.PrincipalFromContext(r.Context())
	if !isDevice && !helpers.RolValido(r, user.RoleSupervisor, user.RoleAdmin) {
		response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
		return
	}
	loteID, err := helpers.ExtractLoteID(r.URL.Path, "/modo")
	if err != nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	var body modoRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "Body inválido", nil)
		return
	}
	nuevo, err := lotesector.ParseModo(body.Modo)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Modo inválido", nil)
		return
	}
	lote, err := h.deps.LoteLookup.GetByID(r.Context(), loteID)
	if err != nil || lote == nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	if lote.Estado != lotesector.EstadoAbierto {
		response.Error(w, http.StatusConflict, "Lote no está abierto", nil)
		return
	}
	if isDevice {
		info, err := h.deps.DeviceSector.SectorDelDispositivo(r.Context(), principal.DeviceID)
		if err != nil || info == nil {
			response.Error(w, http.StatusForbidden, "El dispositivo no tiene sector asignado", nil)
			return
		}
		if info.SectorID != lote.SectorID {
			response.Error(w, http.StatusForbidden, "El dispositivo no pertenece al sector del lote", nil)
			return
		}
	}
	anterior := lote.Modo
	if anterior == nuevo {
		response.OK(w, "Modo sin cambios", modoCambioData{
			LoteID:   loteID,
			Modo:     nuevo,
			Cambiado: false,
		})
		return
	}
	// Validar el producto antes de tocar la DB sólo cuando el modo nuevo
	// requiere loop (SUPERVISION/AUTOMATICO). Un producto inexistente se
	// rechaza sin cambiar el modo, igual que HandleControlStart.
	if nuevo != lotesector.ModoManual {
		prod, perr := h.deps.ProductoRepo.GetByID(r.Context(), lote.ProductoID)
		if perr != nil || prod == nil {
			response.Error(w, http.StatusUnprocessableEntity, "Producto desconocido", nil)
			return
		}
		if nuevo == lotesector.ModoAutomatico {
			if prod.TempSetpoint == nil || prod.VelocidadCintaSetpoint == nil {
				response.Error(w, http.StatusUnprocessableEntity,
					"Receta incompleta: el producto no tiene setpoint de temperatura/velocidad", nil)
				return
			}
			// Sin modelo entrenado, AUTOMATICO no debe mover el horno. El
			// placeholder sólo se permite explícitamente para demos.
			permitir, _ := strconv.ParseBool(os.Getenv("MPC_PERMITIR_PLACEHOLDER"))
			if !permitir && h.deps.Modelos != nil {
				entrenado, merr := h.deps.Modelos.ModeloEntrenado(r.Context(), lote.ProductoID)
				if merr != nil {
					response.Error(w, http.StatusServiceUnavailable, "El predictor no está disponible", nil)
					return
				}
				if !entrenado {
					response.Error(w, http.StatusUnprocessableEntity,
						"El modelo XGBoost todavía no está entrenado", nil)
					return
				}
			}
		}
	}
	// Persistir primero: si falla, no tocamos el loop en memoria.
	if err := h.deps.LoteRepo.UpdateModo(r.Context(), loteID, nuevo); err != nil {
		if errors.Is(err, lotesector.ErrNotFound) {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
		return
	}
	lote.Modo = nuevo
	// Cancelar el loop viejo si existe, en todos los casos: MANUAL no levanta
	// loop nuevo y SUPERVISION/AUTOMATICO lo levantan a continuación.
	h.deps.LoopCancels.StopLoop(loteID)
	// Auditoría secundaria: un fallo de Save no bloquea el cambio de modo.
	h.auditarCambioModo(r.Context(), lote, anterior, nuevo)
	loopActivo := false
	if nuevo != lotesector.ModoManual {
		if err := h.startLoopForLote(r.Context(), lote, ""); err != nil {
			log.Printf("[control] arrancando loop tras cambio de modo lote %s: %v", loteID, err)
		} else {
			loopActivo = true
		}
	}
	response.OK(w, "Modo actualizado", modoCambioData{
		LoteID:       loteID,
		ModoAnterior: anterior,
		ModoNuevo:    nuevo,
		Cambiado:     true,
		LoopActivo:   loopActivo,
	})
}

// auditarCambioModo inserta una fila de auditoría en decisiones_control para el
// cambio de modo. Si el Save falla, sólo se loggea: el cambio de modo es lo
// importante y el audit es secundario.
func (h *ControlHandler) auditarCambioModo(ctx context.Context, lote *lotesector.Lote, anterior, nuevo string) {
	skipped := fmt.Sprintf("cambio_modo_%s_%s", anterior, nuevo)
	cero := 0.0
	now := time.Now().UTC()
	decision := &decisiones_control.DecisionControl{
		LoteID:        lote.ID,
		VentanaInicio: now.Add(-time.Duration(lote.VentanaEvaluacionSegundos) * time.Second),
		VentanaFin:    now,
		TempActual:    0,
		VelActual:     &cero,
		TempPropuesto: 0,
		VelPropuesto:  0,
		Prediccion:    []byte("{}"),
		AplicadoPor:   decisiones_control.AplicadoPorNadie,
		RazonSkipped:  &skipped,
	}
	if err := h.deps.DecisionesRepo.Save(ctx, decision); err != nil {
		log.Printf("[control] audit cambio de modo lote %s (%s→%s): %v", lote.ID, anterior, nuevo, err)
	}
}

// tuningRequest es el body de PATCH /api/v1/lotes/{id}/control/parametros.
// Todos los campos son opcionales: ausente = no cambiar.
type tuningRequest struct {
	ToleranciaTempC         *float64 `json:"tolerancia_temp_c"`
	UmbralAceptacionCrudo   *float64 `json:"umbral_aceptacion_crudo"`
	UmbralAceptacionQuemado *float64 `json:"umbral_aceptacion_quemado"`
	DeltaTemp1MaxC          *float64 `json:"delta_temp1_max_c"`
	DeltaVelMax             *float64 `json:"delta_vel_max"`
}

// HandleTuning maneja PATCH /api/v1/lotes/{id}/control/parametros: ajusta
// tolerancia, umbrales de aceptación y Δmáx del lote (Supervisor/Admin). Si el
// loop estaba activo se reinicia en ADAPTANDO para tomar los valores nuevos.
func (h *ControlHandler) HandleTuning(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	if !helpers.RolValido(r, user.RoleSupervisor, user.RoleAdmin) {
		response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
		return
	}
	loteID, err := helpers.ExtractLoteID(r.URL.Path, "/control/parametros")
	if err != nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	var body tuningRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		response.Error(w, http.StatusBadRequest, "Body inválido", nil)
		return
	}
	if body.ToleranciaTempC == nil && body.UmbralAceptacionCrudo == nil &&
		body.UmbralAceptacionQuemado == nil && body.DeltaTemp1MaxC == nil && body.DeltaVelMax == nil {
		response.Error(w, http.StatusBadRequest, "Sin parámetros para actualizar", nil)
		return
	}
	if v := body.ToleranciaTempC; v != nil && (*v <= 0 || *v > 20) {
		response.Error(w, http.StatusBadRequest, "tolerancia_temp_c fuera de rango (0,20]", nil)
		return
	}
	for _, v := range []*float64{body.UmbralAceptacionCrudo, body.UmbralAceptacionQuemado} {
		if v != nil && (*v < 0 || *v > 100) {
			response.Error(w, http.StatusBadRequest, "umbral de aceptación fuera de rango [0,100]", nil)
			return
		}
	}
	if v := body.DeltaTemp1MaxC; v != nil && (*v <= 0 || *v > 50) {
		response.Error(w, http.StatusBadRequest, "delta_temp1_max_c fuera de rango (0,50]", nil)
		return
	}
	if v := body.DeltaVelMax; v != nil && (*v <= 0 || *v > 2) {
		response.Error(w, http.StatusBadRequest, "delta_vel_max fuera de rango (0,2]", nil)
		return
	}

	lote, err := h.deps.LoteLookup.GetByID(r.Context(), loteID)
	if err != nil || lote == nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	if lote.Estado != lotesector.EstadoAbierto {
		response.Error(w, http.StatusConflict, "Lote no está abierto", nil)
		return
	}

	params := lotesector.ParametrosControl{
		ToleranciaTempC:         body.ToleranciaTempC,
		UmbralAceptacionCrudo:   body.UmbralAceptacionCrudo,
		UmbralAceptacionQuemado: body.UmbralAceptacionQuemado,
		DeltaTemp1MaxC:          body.DeltaTemp1MaxC,
		DeltaVelMax:             body.DeltaVelMax,
	}
	if err := h.deps.LoteRepo.UpdateParametrosControl(r.Context(), loteID, params); err != nil {
		if errors.Is(err, lotesector.ErrNotFound) {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
		return
	}

	// Reflejar el tuning en la copia en memoria para la respuesta. El loop
	// vivo relee los límites en cada tick (`limitesVigentes`), así que no hace
	// falta reiniciarlo ni resetear la fase de precalentamiento.
	aplicarTuning(lote, params)

	response.OK(w, "Parámetros actualizados", map[string]interface{}{
		"lote_id":                   lote.ID,
		"tolerancia_temp_c":         lote.ToleranciaTempC,
		"umbral_aceptacion_crudo":   lote.UmbralAceptacionCrudo,
		"umbral_aceptacion_quemado": lote.UmbralAceptacionQuemado,
		"delta_temp1_max_c":         lote.DeltaTemp1MaxC,
		"delta_vel_max":             lote.DeltaVelMax,
	})
}

// aplicarTuning copia a lote los parámetros no nulos del tuning.
func aplicarTuning(lote *lotesector.Lote, p lotesector.ParametrosControl) {
	if p.ToleranciaTempC != nil {
		lote.ToleranciaTempC = *p.ToleranciaTempC
	}
	if p.UmbralAceptacionCrudo != nil {
		lote.UmbralAceptacionCrudo = *p.UmbralAceptacionCrudo
	}
	if p.UmbralAceptacionQuemado != nil {
		lote.UmbralAceptacionQuemado = *p.UmbralAceptacionQuemado
	}
	if p.DeltaTemp1MaxC != nil {
		lote.DeltaTemp1MaxC = p.DeltaTemp1MaxC
	}
	if p.DeltaVelMax != nil {
		lote.DeltaVelMax = p.DeltaVelMax
	}
}

// limitesResponse son los límites efectivos del MPC que viajan con la propuesta
// para que el panel muestre el rango vigente.
type limitesResponse struct {
	TempMin         float64 `json:"temp_min"`
	TempMax         float64 `json:"temp_max"`
	VelMin          float64 `json:"vel_min"`
	VelMax          float64 `json:"vel_max"`
	DeltaTemp1MaxC  float64 `json:"delta_temp1_max_c"`
	DeltaVelMax     float64 `json:"delta_vel_max"`
	ToleranciaTempC float64 `json:"tolerancia_temp_c"`
}

// limitesDe resuelve los límites efectivos (producto + overrides del lote). Si
// no hay producto o lote, devuelve nil y el campo se omite del payload.
func (h *ControlHandler) limitesDe(ctx context.Context, lote *lotesector.Lote) *limitesResponse {
	if lote == nil || h.deps.ProductoRepo == nil {
		return nil
	}
	prod, err := h.deps.ProductoRepo.GetByID(ctx, lote.ProductoID)
	if err != nil || prod == nil {
		return nil
	}
	lim := optimizador.LimitesDe(prod, lote)
	return &limitesResponse{
		TempMin:         lim.TempMin,
		TempMax:         lim.TempMax,
		VelMin:          lim.VelMin,
		VelMax:          lim.VelMax,
		DeltaTemp1MaxC:  lim.DeltaTemp1,
		DeltaVelMax:     lim.DeltaVel,
		ToleranciaTempC: lim.ToleranciaTempC,
	}
}

// consignaPropuestaResponse es el payload de GET /lotes/{id}/consigna-propuesta.
type consignaPropuestaResponse struct {
	DecisionID    string    `json:"decision_id"`
	Temp1Objetivo float64   `json:"temp1_objetivo"`
	VelObjetivo   float64   `json:"vel_objetivo"`
	GeneratedAt   time.Time `json:"generated_at"`
	TTLSegundos   int       `json:"ttl_segundos"`
	VentanaInicio time.Time `json:"ventana_inicio"`
	VentanaFin    time.Time `json:"ventana_fin"`
	// Autorizada indica si la Pi puede aplicar sin esperar aprobación
	// (AUTOMATICO o propuesta de SUPERVISION ya aprobada).
	Autorizada bool `json:"autorizada"`
	// ManualOverride marca una consigna manual encolada por el backend: la Pi
	// la aplica sin el clamp de Δ por ciclo.
	ManualOverride bool   `json:"manual_override,omitempty"`
	Fase           string `json:"fase,omitempty"`
	// AprobadaPor identifica quién aprobó la propuesta en SUPERVISION.
	AprobadaPor *string `json:"aprobada_por,omitempty"`
	// Limites son los límites efectivos del MPC (producto + overrides del lote).
	Limites *limitesResponse `json:"limites,omitempty"`
}

// propuestaProvider es la vista mínima del loop que expone la propuesta
// pendiente sin consumirla.
type propuestaProvider interface {
	PropuestaPendiente(ttlSegundos int) *control.PropuestaSnapshot
}

// HandleConsignaPropuesta expone GET /api/v1/lotes/{id}/consigna-propuesta.
// Devuelve la propuesta pendiente del loop en memoria si existe; si no,
// reintenta contra la última decisión pendiente persistida (pull-based para
// redes poco confiables). Sirve tanto AUTOMATICO (autorizada=true, la Pi
// aplica sola) como SUPERVISION (autorizada sólo tras la aprobación). En
// MANUAL no hay MPC pero sí se sirve un override manual ya aprobado, marcado
// con manual_override=true para que la Pi omita su clamp de Δ.
// Responde 204 cuando no hay nada pendiente o el lote está cerrado.
func (h *ControlHandler) HandleConsignaPropuesta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	loteID, err := helpers.ExtractLoteID(r.URL.Path, "/consigna-propuesta")
	if err != nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	// El TTL coincide con la ventana de actualización del lote. Si no se puede
	// resolver, se sirve la propuesta con TTL 0 sin bloquear el endpoint.
	ttl := 0
	var lote *lotesector.Lote
	if h.deps.LoteLookup != nil {
		if l, err := h.deps.LoteLookup.GetByID(r.Context(), loteID); err == nil && l != nil {
			lote = l
			ttl = lote.VentanaActualizacionSegundos
		}
	}
	limites := h.limitesDe(r.Context(), lote)

	// 1. Propuesta en memoria del loop activo (lectura no destructiva).
	if h.deps.LoopCancels != nil {
		if loopAny, ok := h.deps.LoopCancels.GetLoop(loteID); ok {
			if loop, ok := loopAny.(propuestaProvider); ok {
				if snap := loop.PropuestaPendiente(ttl); snap != nil {
					response.OK(w, "Consigna propuesta", consignaPropuestaResponse{
						DecisionID:    snap.DecisionID,
						Temp1Objetivo: snap.Temp1,
						VelObjetivo:   snap.Velocidad,
						GeneratedAt:   snap.CreatedAt,
						TTLSegundos:   snap.TTLSegundos,
						VentanaInicio: snap.VentanaInicio,
						VentanaFin:    snap.VentanaFin,
						Autorizada:    snap.Autorizada,
						Fase:          snap.Fase,
						Limites:       limites,
					})
					return
				}
			}
		}
	}

	// 2. Fallback: última decisión pendiente persistida (SUPERVISION o
	// AUTOMATICO esperando el ack del nodo).
	if h.deps.DecisionesRepo == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	d, err := h.deps.DecisionesRepo.GetUltimaPendiente(r.Context(), loteID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
		return
	}
	if d == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Chequear que el lote está vivo y que la propuesta no esté vencida. Si no,
	// respondemos 204 (mismo caso que sin propuesta): no se sirve una decisión
	// NADIE de un lote cerrado ni una propuesta del MPC en MANUAL (ahí sólo
	// vale un override manual ya aprobado).
	// El override manual se persiste con Muestras=0 y aprobado al instante; las
	// decisiones del MPC llevan el conteo de muestras de la ventana.
	manualOverride := d.Muestras == 0 && d.AprobadaPor != nil
	if lote != nil {
		if lote.Estado != lotesector.EstadoAbierto {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if lote.Modo == lotesector.ModoManual && !manualOverride {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	if ttl > 0 && time.Since(d.CreatedAt) > time.Duration(ttl)*time.Second {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// En AUTOMATICO la Pi aplica sin aprobación; en SUPERVISION hace falta que
	// un operario (o la Pi local) haya aprobado la fila.
	autorizada := d.AprobadaPor != nil
	if lote != nil && lote.Modo == lotesector.ModoAutomatico {
		autorizada = true
	}
	response.OK(w, "Consigna propuesta", consignaPropuestaResponse{
		DecisionID:     d.ID,
		Temp1Objetivo:  d.TempPropuesto,
		VelObjetivo:    d.VelPropuesto,
		GeneratedAt:    d.CreatedAt,
		TTLSegundos:    ttl,
		VentanaInicio:  d.VentanaInicio,
		VentanaFin:     d.VentanaFin,
		Autorizada:     autorizada,
		ManualOverride: manualOverride,
		AprobadaPor:    d.AprobadaPor,
		Limites:        limites,
	})
}

// HandleAplicarConsigna maneja POST /api/v1/lotes/{id}/aplicar-consigna (y su
// alias /aprobar-consigna). En SUPERVISION aprueba la propuesta pendiente: el
// operario del panel (JWT) o la propia Pi (Bearer de dispositivo). El backend
// sólo autoriza y responde; la aplicación real llega después por
// /consigna/ack.
func (h *ControlHandler) HandleAplicarConsigna() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		principal, isDevice := deviceauth.PrincipalFromContext(r.Context())
		if !isDevice && !helpers.RolValido(r, user.RoleOperario, user.RoleSupervisor, user.RoleAdmin) {
			response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
			return
		}
		suffix := "/aplicar-consigna"
		if strings.HasSuffix(r.URL.Path, "/aprobar-consigna") {
			suffix = "/aprobar-consigna"
		}
		loteID, err := helpers.ExtractLoteID(r.URL.Path, suffix)
		if err != nil {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		// decision_id es opcional (compatibilidad); si viene, la aprobación
		// sólo vale si es la propuesta vigente. Evita aprobar una propuesta
		// distinta de la que el operario está viendo.
		var body struct {
			DecisionID string `json:"decision_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lote, err := h.deps.LoteLookup.GetByID(r.Context(), loteID)
		if err != nil || lote == nil {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		if lote.Estado != lotesector.EstadoAbierto {
			response.Error(w, http.StatusConflict, "Lote no está abierto", nil)
			return
		}
		if lote.Modo == lotesector.ModoManual {
			response.Error(w, http.StatusConflict, "El lote está en modo MANUAL", nil)
			return
		}
		if isDevice {
			info, serr := h.deps.DeviceSector.SectorDelDispositivo(r.Context(), principal.DeviceID)
			if serr != nil || info == nil {
				response.Error(w, http.StatusForbidden, "El dispositivo no tiene sector asignado", nil)
				return
			}
			if info.SectorID != lote.SectorID {
				response.Error(w, http.StatusForbidden, "El dispositivo no pertenece al sector del lote", nil)
				return
			}
		}
		// En AUTOMATICO la Pi aplica sola: no hay nada que aprobar.
		if lote.Modo == lotesector.ModoAutomatico {
			response.Error(w, http.StatusConflict, "El modo automático no requiere aprobación", nil)
			return
		}
		// Identidad que aprueba, para la auditoría.
		usuario := ""
		if claims := authController.GetClaimsFromContext(r.Context()); claims != nil {
			usuario = claims.Email
		} else if isDevice {
			usuario = "dispositivo:" + principal.DeviceID
		}
		d, err := h.deps.DecisionesRepo.GetUltimaPendiente(r.Context(), loteID)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
			return
		}
		if d == nil {
			response.Error(w, http.StatusConflict, "No hay propuesta pendiente", nil)
			return
		}
		if body.DecisionID != "" && body.DecisionID != d.ID {
			response.Error(w, http.StatusConflict, "La propuesta cambió; recargá antes de aprobar", nil)
			return
		}
		// La Pi es la única actuadora: siempre se aprueba la propuesta y la
		// aplicación real llega después por /consigna/ack.
		if err := h.deps.DecisionesRepo.Aprobar(r.Context(), d.ID, usuario); err != nil {
			if errors.Is(err, decisiones_control.ErrSinPendiente) {
				response.Error(w, http.StatusConflict, "No hay propuesta pendiente", nil)
				return
			}
			response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
			return
		}
		if loopAny, ok := h.deps.LoopCancels.GetLoop(loteID); ok {
			if loop, isLoop := loopAny.(*control.Loop); isLoop {
				loop.AprobarPropuesta(d.ID)
			}
		}
		response.OK(w, "Consigna aprobada", map[string]interface{}{
			"lote_id":        loteID,
			"aprobada":       true,
			"aplicada":       false,
			"decision_id":    d.ID,
			"temp1_objetivo": d.TempPropuesto,
			"vel_objetivo":   d.VelPropuesto,
		})
	}
}

// propuestaClearer es la vista mínima del loop para descartar la propuesta
// aplicada por el nodo.
type propuestaClearer interface {
	ClearPropuesta(decisionID string)
}

// consignaAckRequest es el body de POST /api/v1/lotes/{id}/consigna/ack.
type consignaAckRequest struct {
	DecisionID   string   `json:"decision_id"`
	TempAplicado *float64 `json:"temp_aplicado"`
	VelAplicado  *float64 `json:"vel_aplicado"`
}

// HandleConsignaAck maneja POST /api/v1/lotes/{id}/consigna/ack: la Pi confirma
// que aplicó la propuesta y con qué setpoints. Idempotente (un reenvío del ack
// vuelve a responder 200).
func (h *ControlHandler) HandleConsignaAck() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		principal, isDevice := deviceauth.PrincipalFromContext(r.Context())
		if !isDevice {
			deviceauth.WriteInvalidToken(w)
			return
		}
		loteID, err := helpers.ExtractLoteID(r.URL.Path, "/consigna/ack")
		if err != nil {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		var body consignaAckRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			response.Error(w, http.StatusBadRequest, "Body inválido", nil)
			return
		}
		if body.DecisionID == "" || body.TempAplicado == nil || body.VelAplicado == nil {
			response.Error(w, http.StatusBadRequest, "Faltan decision_id, temp_aplicado o vel_aplicado", nil)
			return
		}
		lote, err := h.deps.LoteLookup.GetByID(r.Context(), loteID)
		if err != nil || lote == nil {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		if lote.Estado != lotesector.EstadoAbierto {
			response.Error(w, http.StatusConflict, "Lote no está abierto", nil)
			return
		}
		info, serr := h.deps.DeviceSector.SectorDelDispositivo(r.Context(), principal.DeviceID)
		if serr != nil || info == nil {
			response.Error(w, http.StatusForbidden, "El dispositivo no tiene sector asignado", nil)
			return
		}
		if info.SectorID != lote.SectorID {
			response.Error(w, http.StatusForbidden, "El dispositivo no pertenece al sector del lote", nil)
			return
		}
		d, err := h.deps.DecisionesRepo.GetByID(r.Context(), body.DecisionID)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
			return
		}
		if d == nil || d.LoteID != loteID {
			response.Error(w, http.StatusNotFound, "Decisión no encontrada", nil)
			return
		}
		if lote.Modo == lotesector.ModoSupervision && d.AprobadaPor == nil {
			response.Error(w, http.StatusConflict, "La propuesta no fue aprobada", nil)
			return
		}
		aplicadoPor := decisiones_control.AplicadoPorOperario
		if lote.Modo == lotesector.ModoAutomatico {
			aplicadoPor = decisiones_control.AplicadoPorMPC
		}
		if err := h.deps.DecisionesRepo.MarcarAplicada(r.Context(), d.ID, *body.TempAplicado, *body.VelAplicado, aplicadoPor); err != nil {
			// Sin sentinel de "no encontrada" en el repo: desambiguamos
			// releyendo la fila.
			if actual, _ := h.deps.DecisionesRepo.GetByID(r.Context(), d.ID); actual == nil {
				response.Error(w, http.StatusNotFound, "Decisión no encontrada", nil)
				return
			}
			response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
			return
		}
		// Best-effort: el ack ya es válido; si falla el cierre de la consigna
		// sólo se loguea (la decisión ya quedó aplicada).
		if h.deps.ConsignaRepo != nil {
			if cerr := h.deps.ConsignaRepo.MarcarAplicadaPorDecision(r.Context(), body.DecisionID); cerr != nil {
				log.Printf("[control] marcando consigna %s como APLICADA: %v", body.DecisionID, cerr)
			}
		}
		// Best-effort: el ack ya es válido; si falla el espejo de setpoints
		// sólo se loguea.
		if err := h.deps.LoteRepo.UpdateSetpointsAplicados(r.Context(), loteID, *body.TempAplicado, *body.VelAplicado); err != nil {
			log.Printf("[control] actualizando setpoints del lote %s tras ack: %v", loteID, err)
		}
		if loopAny, ok := h.deps.LoopCancels.GetLoop(loteID); ok {
			if clearer, ok := loopAny.(propuestaClearer); ok {
				clearer.ClearPropuesta(d.ID)
			}
		}
		response.OK(w, "Consigna aplicada", map[string]interface{}{
			"decision_id":  d.ID,
			"aplicado":     true,
			"aplicado_por": aplicadoPor,
		})
	}
}

// --- helpers ---

// --- Registry ---

// Register guarda el cancel del loop de un lote y arranca un watcher que
// limpia la entrada cuando el contexto del loop se cancela. Si ya existía un
// loop para el lote, cancela el anterior (evita loops duplicados).
func (r *LoopRegistry) Register(loteID string, ctx context.Context, cancel context.CancelFunc) {
	r.mu.Lock()
	if viejo, ok := r.byCancel[loteID]; ok {
		// Mejor esfuerzo: cancelamos el viejo.
		go func() {
			defer func() { _ = recover() }()
			viejo()
		}()
	}
	r.gens[loteID]++
	gen := r.gens[loteID]
	r.byCancel[loteID] = cancel
	r.startedAt[loteID] = time.Now().UTC()
	r.mu.Unlock()

	// Watcher: al cancelarse el contexto, elimina la entrada si sigue siendo
	// la vigente (el gen protege ante re-registro del mismo lote).
	go func() {
		<-ctx.Done()
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.gens[loteID] != gen {
			return
		}
		delete(r.byCancel, loteID)
		delete(r.loops, loteID)
		delete(r.gens, loteID)
		delete(r.startedAt, loteID)
	}()
}

// StopAll cancela todos los loops activos y limpia la registry. Pensado para
// el shutdown graceful del servidor.
func (r *LoopRegistry) StopAll() {
	r.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(r.byCancel))
	for _, c := range r.byCancel {
		cancels = append(cancels, c)
	}
	r.byCancel = map[string]context.CancelFunc{}
	r.loops = map[string]any{}
	r.gens = map[string]uint64{}
	r.startedAt = map[string]time.Time{}
	r.mu.Unlock()

	for _, c := range cancels {
		func() {
			defer func() { _ = recover() }()
			c()
		}()
	}
}

// StopLoop cancela el loop del lote si existe y limpia su entrada de la
// registry. Devuelve true si había un loop activo. El cancel se invoca fuera
// del lock para no bloquear al watcher que reacciona a ctx.Done().
func (r *LoopRegistry) StopLoop(loteID string) bool {
	r.mu.Lock()
	cancel, ok := r.byCancel[loteID]
	if ok {
		delete(r.byCancel, loteID)
		delete(r.loops, loteID)
		delete(r.startedAt, loteID)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	func() {
		defer func() { _ = recover() }()
		cancel()
	}()
	return true
}

// Get devuelve el cancel del loop (para stop explícito).
func (r *LoopRegistry) Get(loteID string) (context.CancelFunc, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byCancel[loteID]
	return c, ok
}

// GetLoop devuelve la referencia al Loop (para aplicar-propuesta).
func (r *LoopRegistry) GetLoop(loteID string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	l, ok := r.loops[loteID]
	return l, ok
}

// StartedAt devuelve cuándo se registró el loop vigente del lote. ok=false si
// no hay loop activo (nunca registrado o ya cancelado).
func (r *LoopRegistry) StartedAt(loteID string) (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.startedAt[loteID]
	return t, ok
}

// SetLoop guarda la referencia al Loop en la registry. Complemento de
// Register; se llama desde el caller con el loop creado antes de Start.
func (r *LoopRegistry) SetLoop(loteID string, loop any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loops[loteID] = loop
}

// (predicador eliminado: el predictor se inyecta al Optimizer desde main.go.)
