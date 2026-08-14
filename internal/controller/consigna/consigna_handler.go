package controller

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// maxRequestBodyBytes limita el tamaño del body a 1 MB para evitar DoS por memoria.
const maxRequestBodyBytes = 1 << 20 // 1 MB

// ConsignaHandler maneja los endpoints HTTP del envío manual de consigna
// térmica al horno (SCA-320) y la consulta de su historial de auditoría.
//
// NOTA: este endpoint todavía no requiere autenticación de usuario porque el
// login con Google OAuth 2.0 está pendiente (ver CLAUDE.md), igual que
// parametros_producto. Cuando se implemente, debería restringirse a Operario/Supervisor.
type ConsignaHandler struct {
	service consigna.Service
}

// NewConsignaHandler instancia el handler inyectando el servicio.
func NewConsignaHandler(svc consigna.Service) *ConsignaHandler {
	return &ConsignaHandler{service: svc}
}

// DispatchManual maneja POST /api/v1/horno/consigna — el operario carga
// manualmente una temperatura y velocidad de cinta objetivo desde el panel
// y el sistema las valida contra el rango seguro del producto antes de
// despacharlas al controlador físico del horno.
func (h *ConsignaHandler) DispatchManual(w http.ResponseWriter, r *http.Request) {
	// 0. Method check
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	// 1. Validate Content-Type
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return
	}

	// 2. Limit body size to prevent memory-exhaustion DoS
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	// 3. Decode JSON body
	var req consigna.ConsignaManualRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "Formato JSON inválido o body demasiado grande", nil)
		return
	}

	// 4. Apply domain validation rules (presencia, valores positivos)
	if err := req.Validate(); err != nil {
		log.Printf("[AUDIT] Validación de consigna manual fallida: %v", err)
		response.Error(w, http.StatusUnprocessableEntity, "Datos de consigna inválidos", err.Error())
		return
	}

	// 5. Despachar la consigna (valida rango seguro + envía al controlador físico)
	rec, err := h.service.DispatchManual(r.Context(), req)
	if err != nil {
		h.handleDispatchError(w, err, rec)
		return
	}

	// 6. Respond 200 OK con el resultado del despacho
	response.OK(w, "Consigna manual despachada al horno", rec)
}

// GetHistorial maneja GET /api/v1/horno/consigna/historial?loteId=... — devuelve
// el historial de auditoría de consignas (automáticas y manuales) de un lote,
// para que el panel de control pueda mostrarlo.
func (h *ConsignaHandler) GetHistorial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	loteID := r.URL.Query().Get("loteId")
	if loteID == "" {
		response.Error(w, http.StatusBadRequest, "El parámetro loteId es requerido", nil)
		return
	}

	items, err := h.service.GetHistorialByLote(r.Context(), loteID)
	if err != nil {
		log.Printf("[ERROR] Error al obtener historial de consignas del lote %s: %v", loteID, err)
		response.Error(w, http.StatusInternalServerError, "Error al obtener el historial de consignas", nil)
		return
	}

	response.OK(w, "Historial de consignas obtenido exitosamente", items)
}

// handleDispatchError traduce los errores de dominio/servicio a la respuesta
// HTTP apropiada para el despacho manual de consigna.
func (h *ConsignaHandler) handleDispatchError(w http.ResponseWriter, err error, rec *consigna.Consigna) {
	switch {
	case errors.Is(err, consigna.ErrHornoNoExiste):
		response.Error(w, http.StatusNotFound, "El horno indicado no existe", nil)
	case errors.Is(err, consigna.ErrParametrosNoExiste):
		response.Error(w, http.StatusUnprocessableEntity, "El producto no tiene parámetros de control cargados", nil)
	case errors.Is(err, consigna.ErrFueraDeRango):
		response.Error(w, http.StatusUnprocessableEntity, "Los valores solicitados están fuera del rango seguro del producto", nil)
	case errors.Is(err, consigna.ErrDispatchFallido):
		// El intento quedó auditado igual (ver ConsignaService.dispatch); se informa el rechazo del hardware.
		log.Printf("[AUDIT] Consigna manual rechazada por el controlador físico: horno=%v", rec)
		response.JSON(w, http.StatusBadGateway, false, "El controlador físico del horno rechazó la consigna", rec, nil)
	default:
		log.Printf("[ERROR] Error al despachar consigna manual: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al despachar la consigna al horno", nil)
	}
}
