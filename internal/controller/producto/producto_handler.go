// Package controller expone el endpoint HTTP del catálogo de productos.
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// maxRequestBodyBytes limita el tamaño del body a 1 MB para evitar DoS por memoria.
const maxRequestBodyBytes = 1 << 20 // 1 MB

// ProductoService es el contrato mínimo que consume el handler.
type ProductoService interface {
	List(ctx context.Context) ([]producto.Producto, error)
	Create(ctx context.Context, req producto.ProductoRequest) (*producto.Producto, error)
	Update(ctx context.Context, id string, req producto.ProductoRequest) (*producto.Producto, error)
	Deactivate(ctx context.Context, id string) error
}

// ProductoHandler maneja el ABM del catálogo en /api/v1/productos.
type ProductoHandler struct {
	service ProductoService
}

// NewProductoHandler instancia el handler inyectando el servicio del catálogo.
func NewProductoHandler(svc ProductoService) *ProductoHandler {
	return &ProductoHandler{service: svc}
}

// Handle despacha por método: GET lista, POST crea, PUT actualiza (id en el
// path) y DELETE aplica la baja lógica. Cualquier otro método devuelve 405.
func (h *ProductoHandler) Handle(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.create(w, r)
	case http.MethodPut:
		h.update(w, r)
	case http.MethodDelete:
		h.deactivate(w, r)
	default:
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
	}
}

// list maneja GET /api/v1/productos devolviendo el catálogo completo,
// incluidos los productos inactivos (la Pi filtra por vigencia).
func (h *ProductoHandler) list(w http.ResponseWriter, r *http.Request) {
	productos, err := h.service.List(r.Context())
	if err != nil {
		log.Printf("[ERROR] Error al obtener el catálogo de productos: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al obtener el catálogo de productos", nil)
		return
	}

	response.OK(w, "Productos obtenidos exitosamente", productos)
}

// create maneja POST /api/v1/productos — alta de un producto con sus parámetros
// de control embebidos.
func (h *ProductoHandler) create(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeAndValidate(w, r)
	if !ok {
		return
	}

	p, err := h.service.Create(r.Context(), req)
	if err != nil {
		h.handleWriteError(w, err, "crear")
		return
	}

	response.JSON(w, http.StatusCreated, true, "Producto creado exitosamente", p, nil)
}

// update maneja PUT /api/v1/productos/{id} — modifica un producto existente.
func (h *ProductoHandler) update(w http.ResponseWriter, r *http.Request) {
	id := productoIDFromPath(r.URL.Path)
	if id == "" {
		response.Error(w, http.StatusNotFound, "El producto no existe", nil)
		return
	}

	req, ok := h.decodeAndValidate(w, r)
	if !ok {
		return
	}

	p, err := h.service.Update(r.Context(), id, req)
	if err != nil {
		h.handleWriteError(w, err, "actualizar")
		return
	}

	response.OK(w, "Producto actualizado exitosamente", p)
}

// deactivate maneja DELETE /api/v1/productos/{id} — baja lógica (activo=false)
// preservando los lotes históricos que referencian al producto.
func (h *ProductoHandler) deactivate(w http.ResponseWriter, r *http.Request) {
	id := productoIDFromPath(r.URL.Path)
	if id == "" {
		response.Error(w, http.StatusNotFound, "El producto no existe", nil)
		return
	}

	if err := h.service.Deactivate(r.Context(), id); err != nil {
		h.handleWriteError(w, err, "dar de baja")
		return
	}

	response.OK(w, "Producto dado de baja exitosamente", map[string]any{"id": id, "activo": false})
}

// decodeAndValidate valida Content-Type, límite de tamaño, formato JSON y reglas
// de negocio del body compartidas por create/update. Escribe la respuesta de
// error correspondiente y devuelve ok=false si algún paso falla.
func (h *ProductoHandler) decodeAndValidate(w http.ResponseWriter, r *http.Request) (producto.ProductoRequest, bool) {
	var req producto.ProductoRequest

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
		log.Printf("[AUDIT] Validación de producto fallida: %v", err)
		var validationErr *producto.ValidationError
		if errors.As(err, &validationErr) {
			response.Error(w, http.StatusUnprocessableEntity, "Datos de producto inválidos", validationErr.Fields)
			return req, false
		}
		response.Error(w, http.StatusUnprocessableEntity, "Datos de producto inválidos", err.Error())
		return req, false
	}

	return req, true
}

// handleWriteError traduce los errores de dominio/repositorio a la respuesta
// HTTP apropiada para las operaciones de escritura sobre el catálogo.
func (h *ProductoHandler) handleWriteError(w http.ResponseWriter, err error, accion string) {
	switch {
	case errors.Is(err, producto.ErrDuplicado):
		response.Error(w, http.StatusConflict, "Ya existe un producto con ese nombre", nil)
	case errors.Is(err, producto.ErrDesconocido):
		response.Error(w, http.StatusNotFound, "El producto no existe", nil)
	default:
		log.Printf("[ERROR] Error al %s producto: %v", accion, err)
		response.Error(w, http.StatusInternalServerError, "Error al guardar el producto", nil)
	}
}

// productoIDFromPath extrae el id de /api/v1/productos/{id}. Devuelve "" si la
// ruta no contiene un id o si incluye segmentos extra.
func productoIDFromPath(path string) string {
	const prefix = "/api/v1/productos/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}
