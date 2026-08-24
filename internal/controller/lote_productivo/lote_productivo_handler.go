package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	lote "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_productivo"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// LoteProductivoHandler maneja los endpoints HTTP para lotes productivos.
type LoteProductivoHandler struct {
	service lote.Service
}

// NewLoteProductivoHandler instancia el handler inyectando el servicio.
func NewLoteProductivoHandler(svc lote.Service) *LoteProductivoHandler {
	return &LoteProductivoHandler{service: svc}
}

// GetAll maneja GET /api/v1/lotes-productivos?productoId=...&page=1&pageSize=10.
// productoId es opcional: si viene, filtra los lotes de ese producto.
func (h *LoteProductivoHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	productoID := strings.TrimSpace(r.URL.Query().Get("productoId"))
	page := parseQueryInt(r, "page", 1)
	pageSize := parseQueryInt(r, "pageSize", 10)

	result, err := h.service.GetAll(productoID, page, pageSize)
	if err != nil {
		if errors.Is(err, lote.ErrProductoNoExiste) {
			response.Error(w, http.StatusNotFound, "El producto no existe en el catálogo", nil)
			return
		}
		response.Error(w, http.StatusInternalServerError, "Error al obtener lotes productivos", err.Error())
		return
	}

	response.Paginated(w, "Lotes productivos obtenidos exitosamente", result.Items, result.Total, result.Page, result.PageSize)
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
