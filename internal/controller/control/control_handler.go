// Package controller expone los endpoints HTTP del control automático /
// supervisión del MPC (SCA-400):
//
//   POST /api/v1/lotes/{id}/control/start          (Supervisor/Admin)
//   POST /api/v1/lotes/{id}/control/stop           (Supervisor/Admin)
//   GET  /api/v1/lotes/{id}/consigna-propuesta      (Bearer dispositivo o JWT)
//   POST /api/v1/lotes/{id}/aplicar-consigna        (Operario mínimo)
//   GET  /api/v1/lotes/{id}/decisiones              (JWT cookie)
//
// El start/stop manipula el Loop del lote en memoria (Registry). El
// consigna-propuesta lo lee el ControlService de la Pi ENTRADA (que ya
// tenemos para telemetría). aplicar-consigna acepta la confirmación
// del operario en SUPERVISION.
package controller

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/control"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/predictor"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// ControlHandler agrupa los handlers del control.
type ControlHandler struct {
	registry *LoopRegistry
}

// LoopRegistry es el mapa en memoria de Loop por lote activo. El Loop se
// crea cuando entra un lote con modo != MANUAL y se cancela cuando el lote
// cierra (o se llama a stop explícitamente).
type LoopRegistry struct {
	mu       sync.Mutex
	loops    map[string]any // referencia al *control.Loop
	byCancel map[string]context.CancelFunc
}

func NewLoopRegistry() *LoopRegistry {
	return &LoopRegistry{
		loops:    map[string]any{},
		byCancel: map[string]context.CancelFunc{},
	}
}

// deps que el handler necesita del lote_sector para configurar el loop.
type ControlDeps struct {
	LoteLookup   LoteLookupService
	ProductoRepo ProductoRepo
	Optimizer    *optimizador.Optimizer
	Consigna     control.ConsignaDispatchService
	LoopCancels  *LoopRegistry
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
func NewControlHandler(deps ControlDeps) *ControlHandler {
	return &ControlHandler{registry: deps.LoopCancels}
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
		if !rolValido(r, user.RoleSupervisor, user.RoleAdmin) {
			response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
			return
		}
		loteID, err := extractLoteIDFromControlPath(r.URL.Path, "/control/start")
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
		limites := optimizador.LimitesDesdeProducto(prod)
		ctx, cancel := context.WithCancel(r.Context())
		loop := control.NewLoop(
			lote.ID, deps.LoteLookup,
			nil, // metricasRepo: se inyecta desde main.go
			deps.ProductoRepo,
			deps.Optimizer,
			deps.Consigna,
			nil, // decisionesRepo: se inyecta desde main.go
			nil, // broker: se inyecta desde main.go
		)
		loop.Configurar(
			prod.ID, lote.Modo, lote.Horno2Habilitado,
			lote.VentanaEvaluacionSegundos, lote.VentanaActualizacionSegundos,
			pesos, limites,
		)
		h.registry.Register(lote.ID, cancel)
		h.registry.SetLoop(lote.ID, loop)
		loop.Start(ctx)
		response.OK(w, "Control iniciado", map[string]interface{}{
			"lote_id":   lote.ID,
			"modo":      lote.Modo,
			"ventana_evaluacion_segundos":   lote.VentanaEvaluacionSegundos,
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
		if !rolValido(r, user.RoleSupervisor, user.RoleAdmin) {
			response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
			return
		}
		loteID, err := extractLoteIDFromControlPath(r.URL.Path, "/control/stop")
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

// HandleConsignaPropuesta expone GET /api/v1/lotes/{id}/consigna-propuesta.
// Por ahora devuelve 204 cuando no hay propuesta pendiente. La Pi ENTRADA
// polea este endpoint y aplica lo que encuentre.
func (h *ControlHandler) HandleConsignaPropuesta() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		// 204 No Content por ahora: el loop ya emite por SSE; el endpoint
		// es para sincronización pull-based en redes poco confiables.
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleAplicarConsigna maneja POST /api/v1/lotes/{id}/aplicar-consigna.
// El operario confirma la propuesta del modo SUPERVISION.
func (h *ControlHandler) HandleAplicarConsigna(deps ControlDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		if !rolValido(r, user.RoleOperario, user.RoleSupervisor, user.RoleAdmin) {
			response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
			return
		}
		loteID, err := extractLoteIDFromControlPath(r.URL.Path, "/aplicar-consigna")
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
		cons, err := loop.AplicarPropuesta(r.Context())
		if err != nil {
			if errors.Is(err, control.ErrLoteNoAbierto) {
				response.Error(w, http.StatusConflict, "No hay propuesta pendiente", nil)
				return
			}
			writeAplicarError(w, err)
			return
		}
		response.OK(w, "Consigna aplicada", map[string]interface{}{
			"lote_id":    loteID,
			"aplicado":   true,
			"consigna":   cons,
		})
	}
}

// --- helpers ---

func extractLoteIDFromControlPath(path, suffix string) (string, error) {
	const prefix = "/api/v1/lotes/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", erroresNotFound()
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	mid = strings.Trim(mid, "/")
	if mid == "" || strings.Contains(mid, "/") {
		return "", erroresNotFound()
	}
	return mid, nil
}

func erroresNotFound() error { return lotesector.ErrNotFound }

func rolValido(r *http.Request, roles ...string) bool {
	claims := authController.GetClaimsFromContext(r.Context())
	if claims == nil {
		return false
	}
	for _, rol := range roles {
		if claims.Role == rol {
			return true
		}
	}
	return false
}

func writeAplicarError(w http.ResponseWriter, err error) {
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

// Register guarda el cancel del loop de un lote. Si ya existía, cancela
// el anterior (evita loops duplicados).
func (r *LoopRegistry) Register(loteID string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if viejo, ok := r.byCancel[loteID]; ok {
		// Mejor esfuerzo: cancelamos el viejo.
		go func() {
			defer func() { _ = recover() }()
			viejo()
		}()
	}
	r.byCancel[loteID] = cancel
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

// evitar import no usado del predictor; algunas funciones auxiliares podrían
// usarlo en futuras versiones.
var _ = predictor.NewClientePredictor