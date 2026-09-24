// Package controller expone los endpoints HTTP del CRUD de sectores:
// listado, alta, modificación y borrado.
package controller

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/controller/requestjson"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// maxRequestBodyBytes limita el body de los requests a 1 MB.
const maxRequestBodyBytes = 1 << 20

// SectorService es el contrato que consume el handler.
type SectorService interface {
	List(ctx context.Context) ([]sector.Sector, error)
	Create(ctx context.Context, req sector.CreateSectorRequest) (*sector.Sector, error)
	Update(ctx context.Context, id string, req sector.UpdateSectorRequest) (*sector.Sector, error)
	Delete(ctx context.Context, id string) error
}

// SectorHandler maneja los endpoints de /api/v1/sectores.
type SectorHandler struct {
	service SectorService
}

// NewSectorHandler instancia el handler inyectando el servicio.
func NewSectorHandler(svc SectorService) *SectorHandler {
	return &SectorHandler{service: svc}
}

// HandleSectores maneja GET (listar) y POST (alta) sobre /api/v1/sectores.
func (h *SectorHandler) HandleSectores(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleList(w, r)
	case http.MethodPost:
		h.handleCreate(w, r)
	default:
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
	}
}

// HandleSectorByID maneja PUT (modificar) y DELETE (eliminar) sobre
// /api/v1/sectores/{id}.
func (h *SectorHandler) HandleSectorByID(w http.ResponseWriter, r *http.Request) {
	id := extractSectorID(r.URL.Path)
	if id == "" {
		response.Error(w, http.StatusNotFound, "Recurso no encontrado", nil)
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.handleUpdate(w, r, id)
	case http.MethodDelete:
		h.handleDelete(w, r, id)
	default:
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
	}
}

// handleList devuelve todos los sectores ordenados por nombre.
func (h *SectorHandler) handleList(w http.ResponseWriter, r *http.Request) {
	sectores, err := h.service.List(r.Context())
	if err != nil {
		log.Printf("[ERROR] Error al listar sectores: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al obtener los sectores", nil)
		return
	}
	response.OK(w, "Sectores obtenidos exitosamente", sectores)
}

// handleCreate da de alta un sector con el nombre recibido.
func (h *SectorHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeCreate(w, r)
	if !ok {
		return
	}

	sec, err := h.service.Create(r.Context(), req)
	if err != nil {
		if sector.IsValidationError(err) {
			response.Error(w, http.StatusUnprocessableEntity, "Datos del sector inválidos", err.Error())
			return
		}
		log.Printf("[ERROR] Error al crear sector: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al crear el sector", nil)
		return
	}
	response.JSON(w, http.StatusCreated, true, "Sector creado exitosamente", sec, nil)
}

// handleUpdate modifica el nombre del sector indicado.
func (h *SectorHandler) handleUpdate(w http.ResponseWriter, r *http.Request, id string) {
	req, ok := decodeUpdate(w, r)
	if !ok {
		return
	}

	sec, err := h.service.Update(r.Context(), id, req)
	if err != nil {
		switch {
		case sector.IsValidationError(err):
			response.Error(w, http.StatusUnprocessableEntity, "Datos del sector inválidos", err.Error())
		case errors.Is(err, sector.ErrSectorNotFound):
			response.Error(w, http.StatusNotFound, "El sector no existe", nil)
		default:
			log.Printf("[ERROR] Error al actualizar sector: %v", err)
			response.Error(w, http.StatusInternalServerError, "Error al actualizar el sector", nil)
		}
		return
	}
	response.JSON(w, http.StatusOK, true, "Sector actualizado exitosamente", sec, nil)
}

// handleDelete elimina el sector indicado. Bloquea con 409 si tiene lotes.
func (h *SectorHandler) handleDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.service.Delete(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, sector.ErrSectorNotFound):
			response.Error(w, http.StatusNotFound, "El sector no existe", nil)
		case errors.Is(err, sector.ErrSectorConLotes):
			response.Error(w, http.StatusConflict, "No se puede eliminar el sector porque tiene lotes asociados", nil)
		default:
			log.Printf("[ERROR] Error al eliminar sector: %v", err)
			response.Error(w, http.StatusInternalServerError, "Error al eliminar el sector", nil)
		}
		return
	}
	response.JSON(w, http.StatusOK, true, "Sector eliminado exitosamente", nil, nil)
}

// decodeCreate valida Content-Type, tamaño, formato JSON y reglas de negocio del
// alta de sector.
func decodeCreate(w http.ResponseWriter, r *http.Request) (sector.CreateSectorRequest, bool) {
	var req sector.CreateSectorRequest
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return req, false
	}
	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
		return req, false
	}
	if err := req.Validate(); err != nil {
		response.Error(w, http.StatusUnprocessableEntity, "Datos del sector inválidos", err.Error())
		return req, false
	}
	return req, true
}

// decodeUpdate valida Content-Type, tamaño, formato JSON y reglas de negocio de
// la modificación de sector.
func decodeUpdate(w http.ResponseWriter, r *http.Request) (sector.UpdateSectorRequest, bool) {
	var req sector.UpdateSectorRequest
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return req, false
	}
	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
		return req, false
	}
	if err := req.Validate(); err != nil {
		response.Error(w, http.StatusUnprocessableEntity, "Datos del sector inválidos", err.Error())
		return req, false
	}
	return req, true
}

// extractSectorID obtiene el {id} de /api/v1/sectores/{id} decodificando el path
// manualmente (las rutas se registran en el subtree y PathValue no está
// disponible). Devuelve "" si el path no tiene un id válido.
func extractSectorID(path string) string {
	const prefix = "/api/v1/sectores/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}
