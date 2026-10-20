// Package controller expone los endpoints HTTP del control automático /
// supervisión del MPC (SCA-400):
//
//	POST  /api/v1/lotes/{id}/control/start         (Supervisor/Admin)
//	POST  /api/v1/lotes/{id}/control/stop          (Supervisor/Admin)
//	PATCH /api/v1/lotes/{id}/modo                  (Supervisor/Admin)
//	GET   /api/v1/lotes/{id}/consigna-propuesta     (Bearer dispositivo o JWT)
//	POST  /api/v1/lotes/{id}/aplicar-consigna       (Operario mínimo)
//	GET   /api/v1/lotes/{id}/decisiones             (JWT cookie)
//	GET   /api/v1/lotes/{id}/control/estado         (JWT cookie)
//	GET   /api/v1/lotes/{id}/confianza-clases       (JWT cookie)
//
// El start/stop manipula el Loop del lote en memoria (Registry). El
// consigna-propuesta lo lee el ControlService de la Pi ENTRADA (que ya
// tenemos para telemetría). aplicar-consigna acepta la confirmación
// del operario en SUPERVISION.
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
	"sync"
	"time"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/helpers"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
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
	registry       *LoopRegistry
	decisionesRepo decisiones_control.Repository
	loteLookup     LoteLookupService
	// deps conserva el set completo de dependencias para los handlers que no
	// reciben deps por parámetro (p.ej. HandleModeChange, que arranca un Loop).
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

// EventosRepo es el mínimo del repo de lote para contar eventos en la ventana.
type EventosRepo interface {
	CountEventosEnVentana(ctx context.Context, loteID string, desde, hasta time.Time) (ok, crudo, quemado int, err error)
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
	Consigna       control.ConsignaDispatchService
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
		{"Consigna", deps.Consigna == nil},
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
	return &ControlHandler{
		registry:       deps.LoopCancels,
		decisionesRepo: deps.DecisionesRepo,
		loteLookup:     deps.LoteLookup,
		deps:           deps,
	}
}

// HandleControlStart maneja POST /api/v1/lotes/{id}/control/start.
// Crea un Loop en memoria si el modo != MANUAL y el horno lo requiere.
func (h *ControlHandler) HandleControlStart(deps ControlDeps) http.HandlerFunc {
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
		lote, err := deps.LoteLookup.GetByID(r.Context(), loteID)
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
		if err := h.startLoopForLote(r.Context(), deps, lote); err != nil {
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

// startLoopForLote arranca un Loop de control para el lote con la configuración
// vigente (ventanas, pesos, horno 2, modo). El registro cancela cualquier loop
// previo del mismo lote. El contexto del loop es propio (no el de la request):
// si se atara a r.Context(), el servidor lo cancelaría al responder y mataría
// el control (regresión F-B2). Devuelve error si el producto del lote no existe.
func (h *ControlHandler) startLoopForLote(ctx context.Context, deps ControlDeps, lote *lotesector.Lote) error {
	prod, err := deps.ProductoRepo.GetByID(ctx, lote.ProductoID)
	if err != nil {
		return err
	}
	hornoID := os.Getenv("MPC_HORNO_ID")
	if hornoID == "" {
		hornoID = "horno-1"
	}
	pesos := optimizador.PesosDesdeLote(lote.PesosObjetivo)
	limites := optimizador.LimitesDe(prod, lote)
	loopCtx, cancel := context.WithCancel(context.Background())
	loop := control.NewLoop(
		lote.ID, deps.LoteLookup,
		deps.MetricasRepo,
		deps.EventosRepo,
		deps.ProductoRepo,
		deps.Optimizer,
		deps.Consigna,
		deps.DecisionesRepo,
		deps.Broker,
	)
	loop.Configurar(
		prod.ID, lote.Modo, lote.Horno2Habilitado, hornoID,
		lote.VentanaEvaluacionSegundos, lote.VentanaActualizacionSegundos,
		pesos, limites,
	)
	deps.LoopCancels.Register(lote.ID, loopCtx, cancel)
	deps.LoopCancels.SetLoop(lote.ID, loop)
	loop.Start(loopCtx, cancel)
	return nil
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
		if !h.registry.StopLoop(loteID) {
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
	lote, err := h.loteLookup.GetByID(r.Context(), loteID)
	if err != nil || lote == nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	var startedAt *time.Time
	if t, ok := h.registry.StartedAt(loteID); ok {
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
	if lote, lerr := h.loteLookup.GetByID(r.Context(), loteID); lerr != nil || lote == nil {
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
	lote, err := h.loteLookup.GetByID(r.Context(), loteID)
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
		if _, perr := h.deps.ProductoRepo.GetByID(r.Context(), lote.ProductoID); perr != nil {
			response.Error(w, http.StatusUnprocessableEntity, "Producto desconocido", nil)
			return
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
	h.registry.StopLoop(loteID)
	// Auditoría secundaria: un fallo de Save no bloquea el cambio de modo.
	h.auditarCambioModo(r.Context(), lote, anterior, nuevo)
	loopActivo := false
	if nuevo != lotesector.ModoManual {
		if err := h.startLoopForLote(r.Context(), h.deps, lote); err != nil {
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
	if err := h.decisionesRepo.Save(ctx, decision); err != nil {
		log.Printf("[control] audit cambio de modo lote %s (%s→%s): %v", lote.ID, anterior, nuevo, err)
	}
}

// consignaPropuestaResponse es el payload de GET /lotes/{id}/consigna-propuesta.
type consignaPropuestaResponse struct {
	DecisionID      string    `json:"decision_id"`
	Temp1Objetivo   float64   `json:"temp1_objetivo"`
	Temp2Objetivo   float64   `json:"temp2_objetivo"`
	VelObjetivo     float64   `json:"vel_objetivo"`
	GeneratedAt     time.Time `json:"generated_at"`
	TTLSegundos     int       `json:"ttl_segundos"`
	VentanaInicio   time.Time `json:"ventana_inicio"`
	VentanaFin      time.Time `json:"ventana_fin"`
	Temp2Disponible bool      `json:"temp2_disponible"`
}

// propuestaProvider es la vista mínima del loop que expone la propuesta
// pendiente sin consumirla.
type propuestaProvider interface {
	PropuestaPendiente(ttlSegundos int) *control.PropuestaSnapshot
}

// prediccionPersistidaTemp2 lee el T2 propuesto del JSONB persistido en
// decisiones_control.prediccion.
type prediccionPersistidaTemp2 struct {
	Temp2 *float64 `json:"temp2_pred"`
}

// HandleConsignaPropuesta expone GET /api/v1/lotes/{id}/consigna-propuesta.
// Devuelve la propuesta pendiente del loop en memoria si existe; si no,
// reintenta contra la última decisión pendiente persistida (pull-based para
// redes poco confiables). Responde 204 cuando no hay nada pendiente.
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
	if h.loteLookup != nil {
		if lote, err := h.loteLookup.GetByID(r.Context(), loteID); err == nil && lote != nil {
			ttl = lote.VentanaActualizacionSegundos
		}
	}

	// 1. Propuesta en memoria del loop activo (lectura no destructiva).
	if h.registry != nil {
		if loopAny, ok := h.registry.GetLoop(loteID); ok {
			if loop, ok := loopAny.(propuestaProvider); ok {
				if snap := loop.PropuestaPendiente(ttl); snap != nil {
					disponible := snap.Temp2 != nil
					t2 := 0.0
					if disponible {
						t2 = *snap.Temp2
					}
					response.OK(w, "Consigna propuesta", consignaPropuestaResponse{
						DecisionID:      snap.DecisionID,
						Temp1Objetivo:   snap.Temp1,
						Temp2Objetivo:   t2,
						VelObjetivo:     snap.Velocidad,
						GeneratedAt:     snap.CreatedAt,
						TTLSegundos:     snap.TTLSegundos,
						VentanaInicio:   snap.VentanaInicio,
						VentanaFin:      snap.VentanaFin,
						Temp2Disponible: disponible,
					})
					return
				}
			}
		}
	}

	// 2. Fallback: última decisión pendiente persistida (SUPERVISION no aprobada).
	if h.decisionesRepo == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	d, err := h.decisionesRepo.GetUltimaPendiente(r.Context(), loteID)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
		return
	}
	if d == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Chequear que el lote está vivo y en modo compatible, y que la propuesta
	// no esté vencida. Si no, respondemos 204 (mismo caso que sin propuesta):
	// no se sirve una decisión NADIE de un lote cerrado, en MANUAL o vieja.
	if h.loteLookup != nil {
		if l, lerr := h.loteLookup.GetByID(r.Context(), loteID); lerr == nil && l != nil {
			if l.Estado != lotesector.EstadoAbierto || l.Modo != lotesector.ModoSupervision {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
	}
	if ttl > 0 && time.Since(d.CreatedAt) > time.Duration(ttl)*time.Second {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var pred prediccionPersistidaTemp2
	if len(d.Prediccion) > 0 {
		_ = json.Unmarshal(d.Prediccion, &pred)
	}
	disponible := pred.Temp2 != nil
	t2 := 0.0
	if disponible {
		t2 = *pred.Temp2
	}
	response.OK(w, "Consigna propuesta", consignaPropuestaResponse{
		DecisionID:      d.ID,
		Temp1Objetivo:   d.TempPropuesto,
		Temp2Objetivo:   t2,
		VelObjetivo:     d.VelPropuesto,
		GeneratedAt:     d.CreatedAt,
		TTLSegundos:     ttl,
		VentanaInicio:   d.VentanaInicio,
		VentanaFin:      d.VentanaFin,
		Temp2Disponible: disponible,
	})
}

// HandleAplicarConsigna maneja POST /api/v1/lotes/{id}/aplicar-consigna.
// El operario confirma la propuesta del modo SUPERVISION.
func (h *ControlHandler) HandleAplicarConsigna(deps ControlDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		if !helpers.RolValido(r, user.RoleOperario, user.RoleSupervisor, user.RoleAdmin) {
			response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
			return
		}
		loteID, err := helpers.ExtractLoteID(r.URL.Path, "/aplicar-consigna")
		if err != nil {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		// Lookup el loop activo. La registry expone un getter tipado al Loop.
		loopAny, ok := h.registry.GetLoop(loteID)
		if !ok {
			response.Error(w, http.StatusNotFound, "No hay loop de control para este lote", nil)
			return
		}
		loop, isLoop := loopAny.(*control.Loop)
		if !isLoop {
			response.Error(w, http.StatusInternalServerError, "Registry corrupto", nil)
			return
		}
		// Identidad del operario desde el JWT, para la auditoría.
		claims := authController.GetClaimsFromContext(r.Context())
		usuario := ""
		if claims != nil {
			usuario = claims.Email
		}
		cons, err := loop.AplicarPropuesta(r.Context(), usuario)
		if err != nil {
			if errors.Is(err, control.ErrLoteNoAbierto) {
				response.Error(w, http.StatusConflict, "No hay propuesta pendiente", nil)
				return
			}
			writeAplicarError(w, err)
			return
		}
		response.OK(w, "Consigna aplicada", map[string]interface{}{
			"lote_id":  loteID,
			"aplicado": true,
			"consigna": cons,
		})
	}
}

// --- helpers ---

func writeAplicarError(w http.ResponseWriter, err error) {
	if errors.Is(err, consigna.ErrHorno2NoHabilitado) {
		response.Error(w, http.StatusUnprocessableEntity, "El producto del lote no tiene el horno 2 habilitado", map[string]string{"code": "horno2_no_habilitado"})
		return
	}
	if errors.Is(err, consigna.ErrFueraDeRango) {
		response.Error(w, http.StatusUnprocessableEntity, "Setpoint fuera de rango del producto", nil)
		return
	}
	if errors.Is(err, consigna.ErrDispatchFallido) {
		response.Error(w, http.StatusBadGateway, "El controlador físico rechazó la consigna", nil)
		return
	}
	response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
}

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
