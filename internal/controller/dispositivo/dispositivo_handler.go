package controller

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// maxRequestBodyBytes limita el tamaño del body a 1 MB para evitar DoS por memoria.
const maxRequestBodyBytes = 1 << 20 // 1 MB

// DispositivoHandler maneja los endpoints HTTP para dispositivos Raspberry Pi.
type DispositivoHandler struct {
	service dispositivo.Service
}

// NewDispositivoHandler instancia el handler inyectando el servicio.
func NewDispositivoHandler(svc dispositivo.Service) *DispositivoHandler {
	return &DispositivoHandler{service: svc}
}

// parseQueryInt extrae un parámetro entero de la query string, devolviendo el valor por defecto si falla.
func parseQueryInt(r *http.Request, key string, defaultVal int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(raw)
	if err != nil || val < 1 {
		return defaultVal
	}
	return val
}

// HandlePing procesa POST /api/v1/dispositivos/ping.
// Recibe las métricas de la Raspberry Pi (CPU, RAM disponible, temperatura del chip)
// y actualiza el estado de salud del dispositivo.
func (h *DispositivoHandler) HandlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return
	}

	apiKey := r.Header.Get("X-API-Key")
	secret := os.Getenv("API_KEY_SECRET")
	if apiKey == "" || subtle.ConstantTimeCompare([]byte(apiKey), []byte(secret)) != 1 {
		response.Error(w, http.StatusUnauthorized, "API key inválida o ausente", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	var req dispositivo.PingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "Formato JSON inválido o body demasiado grande", nil)
		return
	}

	if err := req.Validate(); err != nil {
		response.Error(w, http.StatusUnprocessableEntity, "Datos del ping inválidos", err.Error())
		return
	}

	estado, err := h.service.ProcessPing(r.Context(), req)
	if err != nil {
		if errors.Is(err, dispositivo.ErrDispositivoNotFound) {
			response.Error(w, http.StatusUnprocessableEntity, "El dispositivo no existe en el catálogo", err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, "Error al procesar el ping del dispositivo", nil)
		return
	}

	response.JSON(w, http.StatusOK, true, "Ping recibido correctamente", estado, nil)
}

// HandleEstados maneja GET /api/v1/dispositivos.
// Devuelve el estado de salud actual (online/offline) de todos los dispositivos.
func (h *DispositivoHandler) HandleEstados(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	estados := h.service.GetAllEstados()
	response.OK(w, "Estados de dispositivos obtenidos exitosamente", estados)
}

// HandleMetricas maneja GET /api/v1/dispositivos/metricas?dispositivoId=...&page=1&pageSize=10
// Devuelve el historial paginado de métricas de un dispositivo específico.
func (h *DispositivoHandler) HandleMetricas(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	dispositivoID := r.URL.Query().Get("dispositivoId")
	if strings.TrimSpace(dispositivoID) == "" {
		response.Error(w, http.StatusBadRequest, "El parámetro dispositivoId es requerido", nil)
		return
	}

	page := parseQueryInt(r, "page", 1)
	pageSize := parseQueryInt(r, "pageSize", 10)

	result, err := h.service.GetMetricas(r.Context(), dispositivoID, page, pageSize)
	if err != nil {
		if errors.Is(err, dispositivo.ErrDispositivoNotFound) {
			response.Error(w, http.StatusNotFound, "El dispositivo no existe en el catálogo", err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, "Error al obtener métricas del dispositivo", nil)
		return
	}

	response.Paginated(w, "Métricas del dispositivo obtenidas exitosamente", result.Items, result.Total, result.Page, result.PageSize)
}
