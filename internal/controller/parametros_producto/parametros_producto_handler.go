package controller

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	parametrosproducto "github.com/angelobenedetti29/smart-check-automation/internal/domain/parametros_producto"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// maxRequestBodyBytes limita el tamaño del body a 1 MB para evitar DoS por memoria.
const maxRequestBodyBytes = 1 << 20 // 1 MB

// ParametrosProductoHandler maneja los endpoints HTTP del ABM de parámetros y
// umbrales de control por producto.
//
// NOTA: estos endpoints todavía no requieren autenticación de usuario porque el
// login con Google OAuth 2.0 está pendiente (ver CLAUDE.md). Cuando se implemente,
// GET/POST/PUT deben quedar restringidos a usuarios con rol Supervisor.
type ParametrosProductoHandler struct {
	service parametrosproducto.Service
}

// NewParametrosProductoHandler instancia el handler inyectando el servicio.
func NewParametrosProductoHandler(svc parametrosproducto.Service) *ParametrosProductoHandler {
	return &ParametrosProductoHandler{service: svc}
}

// Handle despacha GET /api/v1/parametros-producto (listar), POST (alta) y
// PUT (modificar) sobre la misma ruta según el método HTTP.
func (h *ParametrosProductoHandler) Handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getAll(w, r)
	case http.MethodPost:
		h.create(w, r)
	case http.MethodPut:
		h.update(w, r)
	default:
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
	}
}

// getAll maneja GET /api/v1/parametros-producto — lista todos los sets de
// parámetros configurados, para el panel de configuración del Supervisor.
func (h *ParametrosProductoHandler) getAll(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.GetAll(r.Context())
	if err != nil {
		log.Printf("[ERROR] Error al obtener parametros_producto: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al obtener los parámetros por producto", nil)
		return
	}

	response.OK(w, "Parámetros por producto obtenidos exitosamente", items)
}

// create maneja POST /api/v1/parametros-producto — alta de un nuevo set de
// parámetros para una variedad de producto que todavía no tiene uno cargado.
func (h *ParametrosProductoHandler) create(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeAndValidate(w, r)
	if !ok {
		return
	}

	p, err := h.service.Create(r.Context(), req)
	if err != nil {
		h.handleWriteError(w, err, "crear")
		return
	}

	response.JSON(w, http.StatusCreated, true, "Parámetros de producto creados exitosamente", p, nil)
}

// update maneja PUT /api/v1/parametros-producto — modifica los rangos de un
// producto existente (identificado por productoId en el body). El sistema aplica
// estas reglas de inmediato a los próximos lotes de ese producto.
func (h *ParametrosProductoHandler) update(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeAndValidate(w, r)
	if !ok {
		return
	}

	p, err := h.service.Update(r.Context(), req)
	if err != nil {
		h.handleWriteError(w, err, "actualizar")
		return
	}

	response.JSON(w, http.StatusOK, true, "Parámetros de producto actualizados exitosamente", p, nil)
}

// decodeAndValidate valida Content-Type, límite de tamaño, formato JSON y reglas
// de negocio del body compartidas por create/update. Escribe la respuesta de error
// correspondiente y devuelve ok=false si algún paso falla.
func (h *ParametrosProductoHandler) decodeAndValidate(w http.ResponseWriter, r *http.Request) (parametrosproducto.ParametroProductoRequest, bool) {
	var req parametrosproducto.ParametroProductoRequest

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
		log.Printf("[AUDIT] Validación de parametros_producto fallida: %v", err)
		response.Error(w, http.StatusUnprocessableEntity, "Datos de parámetros inválidos", err.Error())
		return req, false
	}

	return req, true
}

// handleWriteError traduce los errores de dominio/repositorio a la respuesta HTTP
// apropiada para las operaciones de alta y modificación.
func (h *ParametrosProductoHandler) handleWriteError(w http.ResponseWriter, err error, accion string) {
	switch {
	case errors.Is(err, parametrosproducto.ErrNotFound):
		response.Error(w, http.StatusNotFound, "No existe un set de parámetros para el producto indicado", nil)
	case errors.Is(err, parametrosproducto.ErrYaExiste):
		response.Error(w, http.StatusConflict, "Ya existe un set de parámetros para este producto", nil)
	case errors.Is(err, parametrosproducto.ErrProductoNoExiste):
		response.Error(w, http.StatusUnprocessableEntity, "El producto referenciado no existe en el catálogo", nil)
	default:
		log.Printf("[ERROR] Error al %s parametros_producto: %v", accion, err)
		response.Error(w, http.StatusInternalServerError, "Error al guardar los parámetros de producto", nil)
	}
}
