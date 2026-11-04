// Package controller expone los endpoints de importación CSV de planificación,
// paradas y energía (fase 1 de OEE).
package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/planificacion"
	service "github.com/angelobenedetti29/smart-check-automation/internal/service/planificacion"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// prefijoListado es el subtree que atiende GET y DELETE de planificación.
const prefijoListado = "/api/v1/planificacion/"

const (
	// maxImportFileBytes es el límite del archivo CSV (5 MB).
	maxImportFileBytes = 5 << 20
	// maxImportBodyBytes deja margen para los boundaries del multipart.
	maxImportBodyBytes = maxImportFileBytes + 1<<20
)

// PlanificacionService es el contrato mínimo que consume el handler.
type PlanificacionService interface {
	ImportarPlan(ctx context.Context, input io.Reader) (planificacion.ImportResult, error)
	ImportarParadas(ctx context.Context, input io.Reader) (planificacion.ImportResult, error)
	ImportarEnergia(ctx context.Context, input io.Reader) (planificacion.ImportResult, error)

	ListarPlan(ctx context.Context, q service.Query) (planificacion.Listado[planificacion.PlanItem], error)
	ListarParadas(ctx context.Context, q service.Query) (planificacion.Listado[planificacion.ParadaItem], error)
	ListarEnergia(ctx context.Context, q service.Query) (planificacion.Listado[planificacion.EnergiaItem], error)

	EliminarPlan(ctx context.Context, id string) error
	EliminarParada(ctx context.Context, id string) error
	EliminarEnergia(ctx context.Context, id string) error
}

// Handler maneja las importaciones CSV de planificación.
type Handler struct {
	service PlanificacionService
}

// NewHandler instancia el handler inyectando el servicio de planificación.
func NewHandler(svc PlanificacionService) *Handler {
	return &Handler{service: svc}
}

// HandlePlanImport maneja POST /api/v1/planificacion/plan/import.
func (h *Handler) HandlePlanImport(w http.ResponseWriter, r *http.Request) {
	data, ok := readCSVBody(w, r)
	if !ok {
		return
	}
	res, err := h.service.ImportarPlan(r.Context(), bytes.NewReader(data))
	h.respond(w, err, "Plan importado", res)
}

// HandleParadasImport maneja POST /api/v1/planificacion/paradas/import.
func (h *Handler) HandleParadasImport(w http.ResponseWriter, r *http.Request) {
	data, ok := readCSVBody(w, r)
	if !ok {
		return
	}
	res, err := h.service.ImportarParadas(r.Context(), bytes.NewReader(data))
	h.respond(w, err, "Paradas importadas", res)
}

// HandleEnergiaImport maneja POST /api/v1/planificacion/energia/import.
func (h *Handler) HandleEnergiaImport(w http.ResponseWriter, r *http.Request) {
	data, ok := readCSVBody(w, r)
	if !ok {
		return
	}
	res, err := h.service.ImportarEnergia(r.Context(), bytes.NewReader(data))
	h.respond(w, err, "Energía importada", res)
}

// HandleSubtree maneja /api/v1/planificacion/{plan|paradas|energia}[/{id}]:
// GET lista el período y DELETE {id} borra un registro. Los POST de import
// tienen rutas exactas propias y no pasan por acá.
func (h *Handler) HandleSubtree(w http.ResponseWriter, r *http.Request) {
	partes := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, prefijoListado), "/"), "/")
	tipo := partes[0]
	id := ""
	if len(partes) > 1 {
		id = partes[1]
	}
	if tipo == "" || len(partes) > 2 {
		response.Error(w, http.StatusNotFound, "Recurso no encontrado", nil)
		return
	}

	switch r.Method {
	case http.MethodGet:
		if id != "" {
			response.Error(w, http.StatusNotFound, "Recurso no encontrado", nil)
			return
		}
		h.listar(w, r, tipo)
	case http.MethodDelete:
		if id == "" {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		h.eliminar(w, r, tipo, id)
	default:
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
	}
}

// listar despacha el GET según el tipo del recurso.
func (h *Handler) listar(w http.ResponseWriter, r *http.Request, tipo string) {
	q := r.URL.Query()
	query := service.Query{
		Desde:    q.Get("desde"),
		Hasta:    q.Get("hasta"),
		SectorID: q.Get("sector_id"),
		Limite:   q.Get("limite"),
	}
	switch tipo {
	case "plan":
		res, err := h.service.ListarPlan(r.Context(), query)
		h.responderListado(w, err, "Plan obtenido", res)
	case "paradas":
		res, err := h.service.ListarParadas(r.Context(), query)
		h.responderListado(w, err, "Paradas obtenidas", res)
	case "energia":
		res, err := h.service.ListarEnergia(r.Context(), query)
		h.responderListado(w, err, "Energía obtenida", res)
	default:
		response.Error(w, http.StatusNotFound, "Recurso no encontrado", nil)
	}
}

// eliminar despacha el DELETE según el tipo del recurso.
func (h *Handler) eliminar(w http.ResponseWriter, r *http.Request, tipo, id string) {
	var err error
	switch tipo {
	case "plan":
		err = h.service.EliminarPlan(r.Context(), id)
	case "paradas":
		err = h.service.EliminarParada(r.Context(), id)
	case "energia":
		err = h.service.EliminarEnergia(r.Context(), id)
	default:
		response.Error(w, http.StatusNotFound, "Recurso no encontrado", nil)
		return
	}
	switch {
	case err == nil:
		response.OK(w, "Registro eliminado", nil)
	case errors.Is(err, planificacion.ErrIDInvalido):
		response.Error(w, http.StatusBadRequest, err.Error(), nil)
	case errors.Is(err, planificacion.ErrNoEncontrado):
		response.Error(w, http.StatusNotFound, err.Error(), nil)
	default:
		log.Printf("[ERROR] Error al eliminar registro de planificación: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al eliminar el registro", nil)
	}
}

func (h *Handler) responderListado(w http.ResponseWriter, err error, message string, data any) {
	if err == nil {
		response.OK(w, message, data)
		return
	}
	if esErrorValidacion(err) {
		response.Error(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	log.Printf("[ERROR] Error al listar planificación: %v", err)
	response.Error(w, http.StatusInternalServerError, "Error al obtener los datos", nil)
}

func esErrorValidacion(err error) bool {
	return errors.Is(err, planificacion.ErrFechaInvalida) ||
		errors.Is(err, planificacion.ErrDesdeMayorHasta) ||
		errors.Is(err, planificacion.ErrRangoExcedido) ||
		errors.Is(err, planificacion.ErrLimiteInvalido)
}

// readCSVBody valida método, multipart y tamaño, y devuelve el archivo `file`.
// Escribe la respuesta de error y devuelve ok=false si algo falla.
func readCSVBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return nil, false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxImportBodyBytes)
	if err := r.ParseMultipartForm(maxImportFileBytes); err != nil {
		response.Error(w, http.StatusBadRequest, "Se esperaba un multipart/form-data con el archivo CSV", nil)
		return nil, false
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		response.Error(w, http.StatusBadRequest, "Se requiere el archivo CSV en el campo 'file'", nil)
		return nil, false
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxImportFileBytes+1))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "No se pudo leer el archivo CSV", nil)
		return nil, false
	}
	if len(data) > maxImportFileBytes {
		response.Error(w, http.StatusBadRequest, "El archivo supera el límite de 5 MB", nil)
		return nil, false
	}
	return data, true
}

// respond traduce el resultado del service a la respuesta HTTP.
func (h *Handler) respond(w http.ResponseWriter, err error, message string, res planificacion.ImportResult) {
	if err == nil {
		response.OK(w, message, res)
		return
	}
	if errors.Is(err, planificacion.ErrFormatoCSV) {
		response.Error(w, http.StatusBadRequest, "Archivo CSV inválido", err.Error())
		return
	}
	log.Printf("[ERROR] Error al importar CSV de planificación: %v", err)
	response.Error(w, http.StatusInternalServerError, "Error al importar el archivo CSV", nil)
}
