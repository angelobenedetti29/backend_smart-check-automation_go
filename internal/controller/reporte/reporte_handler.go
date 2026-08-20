package reporte

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reporte"
	reporteService "github.com/angelobenedetti29/smart-check-automation/internal/service/reporte"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// Handler maneja las solicitudes HTTP relacionadas con reportes y KPIs gerenciales.
type Handler struct {
	service reporte.Service
}

// NewHandler crea una nueva instancia de Handler con el servicio inyectado.
func NewHandler(svc reporte.Service) *Handler {
	return &Handler{service: svc}
}

// parseFlexibleDate intenta parsear fechas en formatos comunes (RFC3339 o YYYY-MM-DD).
func parseFlexibleDate(dateStr string, isEndOfDay bool) (*time.Time, error) {
	dateStr = strings.TrimSpace(dateStr)
	if dateStr == "" {
		return nil, nil
	}

	// 1. Intentar RFC3339 completo
	if t, err := time.Parse(time.RFC3339, dateStr); err == nil {
		return &t, nil
	}

	// 2. Intentar formato fecha simple "2006-01-02"
	if t, err := time.Parse("2006-01-02", dateStr); err == nil {
		if isEndOfDay {
			t = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.UTC)
		} else {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}
		return &t, nil
	}

	return nil, errors.New("formato de fecha inválido, use YYYY-MM-DD o RFC3339")
}

// GetKPIFinanciero maneja GET /api/v1/reportes/kpi-financiero.
// Retorna el consolidado financiero de mermas en ARS junto con advertencias de cálculo parcial si aplican.
func (h *Handler) GetKPIFinanciero(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	query := r.URL.Query()

	// Parsear fecha de inicio (admite fecha_inicio, desde, inicio)
	inicioStr := query.Get("fecha_inicio")
	if inicioStr == "" {
		inicioStr = query.Get("desde")
	}
	if inicioStr == "" {
		inicioStr = query.Get("inicio")
	}
	fechaInicio, err := parseFlexibleDate(inicioStr, false)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "fecha_inicio inválida: "+err.Error(), nil)
		return
	}

	// Parsear fecha de fin (admite fecha_fin, hasta, fin)
	finStr := query.Get("fecha_fin")
	if finStr == "" {
		finStr = query.Get("hasta")
	}
	if finStr == "" {
		finStr = query.Get("fin")
	}
	fechaFin, err := parseFlexibleDate(finStr, true)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "fecha_fin inválida: "+err.Error(), nil)
		return
	}

	productoID := query.Get("producto_id")
	if productoID == "" {
		productoID = query.Get("productoId")
	}

	turno := query.Get("turno")

	filtro := reporte.FiltroKPI{
		FechaInicio: fechaInicio,
		FechaFin:    fechaFin,
		ProductoID:  productoID,
		Turno:       turno,
	}

	kpi, err := h.service.CalcularKPIFinanciero(r.Context(), filtro)
	if err != nil {
		if errors.Is(err, reporteService.ErrRangoFechasInvalido) || errors.Is(err, reporteService.ErrTurnoInvalido) {
			response.Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}

		log.Printf("[ERROR] Error al calcular KPI financiero de mermas: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al calcular el KPI financiero", nil)
		return
	}

	response.OK(w, "KPI financiero obtenido exitosamente", kpi)
}
