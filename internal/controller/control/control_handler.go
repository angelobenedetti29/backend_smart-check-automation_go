// Package controller expone los endpoints HTTP del control automático /
// supervisión del MPC (SCA-400):
//
//	POST /api/v1/lotes/{id}/control/start          (Supervisor/Admin)
//	POST /api/v1/lotes/{id}/control/stop           (Supervisor/Admin)
//	GET  /api/v1/lotes/{id}/consigna-propuesta      (Bearer dispositivo o JWT)
//	POST /api/v1/lotes/{id}/aplicar-consigna        (Operario mínimo)
//	GET  /api/v1/lotes/{id}/decisiones              (JWT cookie)
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
	"net/http"
	"os"
	"sync"
	"time"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/helpers"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/control"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// ControlHandler agrupa los handlers del control.
type ControlHandler struct {
	registry       *LoopRegistry
	decisionesRepo decisiones_control.Repository
	loteLookup     LoteLookupService
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
}

func NewLoopRegistry() *LoopRegistry {
	return &LoopRegistry{
		loops:    map[string]any{},
		byCancel: map[string]context.CancelFunc{},
		gens:     map[string]uint64{},
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

// Deps que el handler necesita del lote_sector para configurar el loop.
// Las cuatro dependencias siguientes (MetricasRepo, EventosRepo, DecisionesRepo,
// Broker) son OBLIGATORIAS: el Loop las invoca dentro del tick y un nil ahí
// produce nil pointer dereference.
type ControlDeps struct {
	LoteLookup     LoteLookupService
	ProductoRepo   ProductoRepo
	Optimizer      *optimizador.Optimizer
	Consigna       control.ConsignaDispatchService
	LoopCancels    *LoopRegistry
	MetricasRepo   MetricasRepo
	EventosRepo    EventosRepo
	DecisionesRepo decisiones_control.Repository
	Broker         *sse.Broker
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
		{"DecisionesRepo", deps.DecisionesRepo == nil},
		{"Broker", deps.Broker == nil},
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
		prod, err := deps.ProductoRepo.GetByID(r.Context(), lote.ProductoID)
		if err != nil {
			response.Error(w, http.StatusUnprocessableEntity, "Producto desconocido", nil)
			return
		}
		pesos := optimizador.PesosDesdeLote(lote.PesosObjetivo)
		limites := optimizador.LimitesDe(prod, lote)
		hornoID := os.Getenv("MPC_HORNO_ID")
		if hornoID == "" {
			hornoID = "horno-1"
		}
		// El loop debe sobrevivir al cierre de la request HTTP: si se atara a
		// r.Context(), el servidor lo cancelaría al responder 200 y mataría el
		// control. Se desacopla con un contexto raíz propio.
		ctx, cancel := context.WithCancel(context.Background())
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
		h.registry.Register(lote.ID, ctx, cancel)
		h.registry.SetLoop(lote.ID, loop)
		loop.Start(ctx, cancel)
		response.OK(w, "Control iniciado", map[string]interface{}{
			"lote_id":                        lote.ID,
			"modo":                           lote.Modo,
			"ventana_evaluacion_segundos":    lote.VentanaEvaluacionSegundos,
			"ventana_actualizacion_segundos": lote.VentanaActualizacionSegundos,
		})
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
		cancel, ok := h.registry.Get(loteID)
		if !ok {
			response.OK(w, "Sin loop activo", map[string]interface{}{"lote_id": loteID, "stopped": false, "motivo": "no_loop_activo"})
			return
		}
		cancel()
		h.registry.Unregister(loteID)
		response.OK(w, "Control detenido", map[string]interface{}{"lote_id": loteID, "stopped": true})
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
	r.mu.Unlock()

	for _, c := range cancels {
		func() {
			defer func() { _ = recover() }()
			c()
		}()
	}
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

// Unregister elimina el loop de la registry. Se invoca desde el caller's
// defer tras cancelar.
func (r *LoopRegistry) Unregister(loteID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byCancel, loteID)
	delete(r.loops, loteID)
}

// SetLoop guarda la referencia al Loop en la registry. Complemento de
// Register; se llama desde el caller con el loop creado antes de Start.
func (r *LoopRegistry) SetLoop(loteID string, loop any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loops[loteID] = loop
}

// (predicador eliminado: el predictor se inyecta al Optimizer desde main.go.)
