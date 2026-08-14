package controller

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
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

// Handle despacha GET /api/v1/dispositivos (listar estados), POST (alta), PUT
// (modificar) y DELETE (eliminar) sobre la misma ruta según el método HTTP.
//
// NOTA: el alta y la modificación todavía no requieren autenticación de usuario
// porque el login con Google OAuth 2.0 está pendiente (ver CLAUDE.md). Cuando se
// implemente, POST/PUT/DELETE deben quedar restringidos a usuarios con rol
// Operador/Supervisor. No se reusa X-API-Key: esa clave es para las Raspberry Pi,
// no para el panel del operador.
func (h *DispositivoHandler) Handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.HandleEstados(w, r)
	case http.MethodPost:
		h.HandleCreate(w, r)
	case http.MethodPut:
		h.HandleUpdate(w, r)
	case http.MethodDelete:
		h.HandleDelete(w, r)
	default:
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
	}
}

// HandleCreate maneja POST /api/v1/dispositivos — alta de un dispositivo del
// catálogo por nombre/ubicación desde el panel del operador.
func (h *DispositivoHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeAndValidate(w, r)
	if !ok {
		return
	}

	estado, err := h.service.Create(r.Context(), req)
	if err != nil {
		log.Printf("[ERROR] Error al crear dispositivo: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al crear el dispositivo", nil)
		return
	}

	response.JSON(w, http.StatusCreated, true, "Dispositivo creado exitosamente", estado, nil)
}

// HandleUpdate maneja PUT /api/v1/dispositivos — modifica nombre/ubicación de un
// dispositivo existente, identificado por dispositivoId en el body.
func (h *DispositivoHandler) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeAndValidateUpdate(w, r)
	if !ok {
		return
	}

	estado, err := h.service.Update(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, dispositivo.ErrDispositivoNotFound):
			response.Error(w, http.StatusNotFound, "El dispositivo no existe en el catálogo", err.Error())
		case dispositivo.IsValidationError(err):
			response.Error(w, http.StatusUnprocessableEntity, "Datos del dispositivo inválidos", err.Error())
		default:
			log.Printf("[ERROR] Error al actualizar dispositivo: %v", err)
			response.Error(w, http.StatusInternalServerError, "Error al actualizar el dispositivo", nil)
		}
		return
	}

	response.JSON(w, http.StatusOK, true, "Dispositivo actualizado exitosamente", estado, nil)
}

// HandleDelete maneja DELETE /api/v1/dispositivos?dispositivoId=... — elimina un
// dispositivo del catálogo y de la caché de estado.
func (h *DispositivoHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	dispositivoID := r.URL.Query().Get("dispositivoId")
	if strings.TrimSpace(dispositivoID) == "" {
		response.Error(w, http.StatusBadRequest, "El parámetro dispositivoId es requerido", nil)
		return
	}

	if err := h.service.Delete(r.Context(), dispositivoID); err != nil {
		if errors.Is(err, dispositivo.ErrDispositivoNotFound) {
			response.Error(w, http.StatusNotFound, "El dispositivo no existe en el catálogo", err.Error())
			return
		}
		log.Printf("[ERROR] Error al eliminar dispositivo: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al eliminar el dispositivo", nil)
		return
	}

	response.JSON(w, http.StatusOK, true, "Dispositivo eliminado exitosamente", nil, nil)
}

// decodeAndValidate valida Content-Type, límite de tamaño, formato JSON y reglas
// de negocio del body del alta. Escribe la respuesta de error correspondiente y
// devuelve ok=false si algún paso falla.
func (h *DispositivoHandler) decodeAndValidate(w http.ResponseWriter, r *http.Request) (dispositivo.CreateDispositivoRequest, bool) {
	var req dispositivo.CreateDispositivoRequest

	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return req, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "Formato JSON inválido o body demasiado grande", nil)
		return req, false
	}

	if err := req.Validate(); err != nil {
		response.Error(w, http.StatusUnprocessableEntity, "Datos del dispositivo inválidos", err.Error())
		return req, false
	}

	return req, true
}

// decodeAndValidateUpdate valida Content-Type, límite de tamaño, formato JSON y
// reglas de negocio del body de la modificación (PUT). Escribe la respuesta de
// error correspondiente y devuelve ok=false si algún paso falla.
func (h *DispositivoHandler) decodeAndValidateUpdate(w http.ResponseWriter, r *http.Request) (dispositivo.UpdateDispositivoRequest, bool) {
	var req dispositivo.UpdateDispositivoRequest

	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return req, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "Formato JSON inválido o body demasiado grande", nil)
		return req, false
	}

	if err := req.Validate(); err != nil {
		response.Error(w, http.StatusUnprocessableEntity, "Datos del dispositivo inválidos", err.Error())
		return req, false
	}

	return req, true
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
