package controller

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/controller/requestjson"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// maxRequestBodyBytes limita el tamaño del body a 1 MB para evitar DoS por memoria.
const maxRequestBodyBytes = 1 << 20 // 1 MB

// DispositivoHandler maneja los endpoints HTTP para dispositivos Raspberry Pi.
type DispositivoHandler struct {
	service dispositivo.Service
	secure  interface {
		ListDeviceReads(context.Context) ([]dispositivo.DeviceRead, error)
	}
}

// NewDispositivoHandler instancia el handler inyectando el servicio.
func NewDispositivoHandler(svc dispositivo.Service, secure ...interface {
	ListDeviceReads(context.Context) ([]dispositivo.DeviceRead, error)
}) *DispositivoHandler {
	h := &DispositivoHandler{service: svc}
	if len(secure) > 0 {
		h.secure = secure[0]
	}
	return h
}

// Handle despacha GET /api/v1/dispositivos (listar estados), POST (alta), PUT
// (modificar) y DELETE (eliminar) sobre la misma ruta según el método HTTP.
//
// La autenticación JWT de POST/PUT/DELETE se aplica en el wiring de rutas, sin
// restricción de rol. No se reusa X-API-Key: esa clave es exclusiva de las
// Raspberry Pi y no del panel del operador.
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

	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
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

	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
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
// Recibe las métricas de la Raspberry Pi (CPU, RAM, almacenamiento y temperatura
// del chip) y actualiza el estado de salud del dispositivo.
func (h *DispositivoHandler) HandlePing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return
	}

	principal, ok := deviceauth.PrincipalFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Token de dispositivo inválido", map[string]string{"code": "invalid_device_token"})
		return
	}

	var req dispositivo.PingRequest
	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
		return
	}

	// El id del dispositivo es autoritativo del principal: el nodo puede omitirlo
	// en el body y el servidor inyecta el suyo antes de validar.
	req.DispositivoID = principal.DeviceID

	if err := req.Validate(); err != nil {
		response.Error(w, http.StatusUnprocessableEntity, "Datos del ping inválidos", err.Error())
		return
	}

	estado, err := h.service.ProcessPing(r.Context(), req)
	if err != nil {
		if errors.Is(err, deviceauth.ErrInvalidToken) {
			response.Error(w, http.StatusUnauthorized, "Token de dispositivo inválido", map[string]string{"code": "invalid_device_token"})
			return
		}
		if errors.Is(err, dispositivo.ErrDispositivoNotFound) {
			response.Error(w, http.StatusUnprocessableEntity, "El dispositivo no existe en el catálogo", err.Error())
			return
		}
		response.Error(w, http.StatusInternalServerError, "Error al procesar el ping del dispositivo", nil)
		return
	}

	response.JSON(w, http.StatusOK, true, "Ping recibido correctamente", estado, nil)
}

// HandleRename maneja PUT /api/v1/dispositivos/nombre — la propia Raspberry,
// autenticada con su secret, cambia únicamente su nombre. El id del dispositivo
// es siempre el del principal verificado, nunca el del body.
func (h *DispositivoHandler) HandleRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return
	}

	principal, ok := deviceauth.PrincipalFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Token de dispositivo inválido", map[string]string{"code": "invalid_device_token"})
		return
	}

	var req dispositivo.RenameDispositivoRequest
	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
		return
	}

	if err := req.Validate(); err != nil {
		response.Error(w, http.StatusUnprocessableEntity, "Datos del dispositivo inválidos", err.Error())
		return
	}

	estado, err := h.service.Rename(r.Context(), principal.DeviceID, req.Nombre)
	if err != nil {
		if errors.Is(err, dispositivo.ErrDispositivoNotFound) {
			response.Error(w, http.StatusNotFound, "El dispositivo no existe en el catálogo", err.Error())
			return
		}
		log.Printf("[ERROR] Error al renombrar dispositivo: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al renombrar el dispositivo", nil)
		return
	}

	response.JSON(w, http.StatusOK, true, "Dispositivo renombrado exitosamente", estado, nil)
}

// HandleEstados maneja GET /api/v1/dispositivos.
// Devuelve el estado de salud actual (online/offline) de todos los dispositivos.
func (h *DispositivoHandler) HandleEstados(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	if h.secure != nil {
		reads, err := h.secure.ListDeviceReads(r.Context())
		if err != nil {
			response.Error(w, http.StatusServiceUnavailable, "No se pudo consultar el estado de seguridad de los dispositivos", map[string]string{"code": "auth_store_unavailable"})
			return
		}
		telemetry := make(map[string]dispositivo.EstadoDispositivo)
		for _, state := range h.service.GetAllEstados() {
			telemetry[state.DispositivoID] = state
		}
		out := make([]dispositivo.DeviceRead, 0, len(reads))
		for _, read := range reads {
			read.Estado = dispositivo.EstadoOffline
			if state, ok := telemetry[read.DispositivoID]; ok {
				read.Estado = state.Estado
				read.UltimaMetrica = state.UltimaMetrica
				read.LastSeen = state.LastSeen
			}
			out = append(out, read)
		}
		noStore(w)
		response.OK(w, "Estados de dispositivos obtenidos exitosamente", out)
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

// noStore marca la respuesta como no cacheable para datos sensibles de seguridad.
func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }
