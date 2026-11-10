// Package controller expone GET /api/v1/reportes/resumen,
// GET /api/v1/reportes/lotes, GET /api/v1/reportes/trazabilidad y
// GET /api/v1/reportes/alertas: agregados, detalle, trazabilidad y export de
// alertas para un período ART, con filtros opcionales por sector, producto y
// turno.
package controller

import (
	"context"
	"errors"
	"net/http"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reportes"
	reportesService "github.com/angelobenedetti29/smart-check-automation/internal/service/reportes"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// ReportesService es el contrato que consume el handler.
type ReportesService interface {
	ImpactoEconomico(ctx context.Context, q reportesService.Query) (*reportes.ReporteEconomico, error)
	Resumen(ctx context.Context, q reportesService.Query) (*reportes.Resumen, error)
	Lotes(ctx context.Context, q reportesService.Query) (*reportes.PaginaLotes, error)
	Trazabilidad(ctx context.Context, q reportesService.Query) (*reportes.Trazabilidad, error)
	Alertas(ctx context.Context, q reportesService.Query) (*reportes.PaginaAlertas, error)
}

// ReportesDeps agrupa las dependencias del handler.
type ReportesDeps struct {
	Service ReportesService
}

func HandleImpactoEconomico(deps ReportesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		q := r.URL.Query()
		data, err := deps.Service.ImpactoEconomico(r.Context(), reportesService.Query{Desde: q.Get("desde"), Hasta: q.Get("hasta"), SectorID: q.Get("sector_id"), ProductoID: q.Get("producto_id"), Turno: q.Get("turno")})
		if err != nil {
			if esErrorValidacion(err) {
				response.Error(w, http.StatusBadRequest, err.Error(), nil)
			} else {
				response.Error(w, http.StatusInternalServerError, "Error al calcular el impacto económico", nil)
			}
			return
		}
		response.OK(w, "Impacto económico de las mermas", data)
	}
}

// HandleResumen maneja GET /api/v1/reportes/resumen. El JWT (cualquier rol) se
// valida en el middleware antes de llegar acá.
func HandleResumen(deps ReportesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}

		q := r.URL.Query()
		resumen, err := deps.Service.Resumen(r.Context(), reportesService.Query{
			Desde:      q.Get("desde"),
			Hasta:      q.Get("hasta"),
			SectorID:   q.Get("sector_id"),
			ProductoID: q.Get("producto_id"),
			Turno:      q.Get("turno"),
		})
		if err != nil {
			if esErrorValidacion(err) {
				response.Error(w, http.StatusBadRequest, err.Error(), nil)
				return
			}
			response.Error(w, http.StatusInternalServerError, "Error al generar el resumen", nil)
			return
		}
		response.OK(w, "Resumen generado", resumen)
	}
}

func esErrorValidacion(err error) bool {
	return errors.Is(err, reportes.ErrFechaInvalida) ||
		errors.Is(err, reportes.ErrDesdeMayorHasta) ||
		errors.Is(err, reportes.ErrRangoExcedido) ||
		errors.Is(err, reportes.ErrTurnoInvalido) ||
		errors.Is(err, reportes.ErrProductoIDInvalido) ||
		errors.Is(err, reportes.ErrLimiteInvalido) ||
		errors.Is(err, reportes.ErrCursorInvalido) ||
		errors.Is(err, reportes.ErrEstadoInvalido)
}

// HandleTrazabilidad maneja GET /api/v1/reportes/trazabilidad?lote_id=<uuid>:
// snapshot completo del lote para el PDF de trazabilidad. El JWT (cualquier
// rol) se valida en el middleware antes de llegar acá.
func HandleTrazabilidad(deps ReportesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}

		trazabilidad, err := deps.Service.Trazabilidad(r.Context(), reportesService.Query{
			LoteID: r.URL.Query().Get("lote_id"),
		})
		if err != nil {
			switch {
			case errors.Is(err, reportes.ErrLoteIDInvalido):
				response.Error(w, http.StatusBadRequest, err.Error(), nil)
			case errors.Is(err, reportes.ErrLoteNoEncontrado):
				response.Error(w, http.StatusNotFound, err.Error(), nil)
			default:
				response.Error(w, http.StatusInternalServerError, "Error al generar la trazabilidad", nil)
			}
			return
		}
		response.OK(w, "Trazabilidad del lote", trazabilidad)
	}
}

// HandleAlertas maneja GET /api/v1/reportes/alertas: export de alertas del
// período con filtros y sin el tope operativo de /api/v1/alertas. El JWT
// (cualquier rol) se valida en el middleware antes de llegar acá.
func HandleAlertas(deps ReportesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}

		q := r.URL.Query()
		pagina, err := deps.Service.Alertas(r.Context(), reportesService.Query{
			Desde:      q.Get("desde"),
			Hasta:      q.Get("hasta"),
			SectorID:   q.Get("sector_id"),
			ProductoID: q.Get("producto_id"),
			Turno:      q.Get("turno"),
			Estado:     q.Get("estado"),
			Limite:     q.Get("limite"),
		})
		if err != nil {
			if esErrorValidacion(err) {
				response.Error(w, http.StatusBadRequest, err.Error(), nil)
				return
			}
			response.Error(w, http.StatusInternalServerError, "Error al obtener las alertas del reporte", nil)
			return
		}
		response.OK(w, "Alertas del período", pagina)
	}
}

// HandleLotes maneja GET /api/v1/reportes/lotes: detalle paginado por cursor
// de los lotes del período, con kg y métricas derivadas. El JWT (cualquier
// rol) se valida en el middleware antes de llegar acá.
func HandleLotes(deps ReportesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}

		q := r.URL.Query()
		pagina, err := deps.Service.Lotes(r.Context(), reportesService.Query{
			Desde:      q.Get("desde"),
			Hasta:      q.Get("hasta"),
			SectorID:   q.Get("sector_id"),
			ProductoID: q.Get("producto_id"),
			Turno:      q.Get("turno"),
			Limite:     q.Get("limite"),
			AntesDe:    q.Get("antes_de"),
		})
		if err != nil {
			if esErrorValidacion(err) {
				response.Error(w, http.StatusBadRequest, err.Error(), nil)
				return
			}
			response.Error(w, http.StatusInternalServerError, "Error al obtener los lotes del reporte", nil)
			return
		}
		response.PaginatedWithCursor(w, "Lotes del reporte obtenidos", pagina.Lotes,
			pagina.Total, 1, pagina.Limite, pagina.SiguienteCursor)
	}
}
