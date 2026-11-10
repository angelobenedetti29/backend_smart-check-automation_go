// Package controller expone GET /api/v1/reportes/oee: cálculo de OEE estándar
// (Availability, Performance, Quality) para un período ART, global y por
// sector.
package controller

import (
	"context"
	"errors"
	"net/http"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/oee"
	oeeService "github.com/angelobenedetti29/smart-check-automation/internal/service/oee"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// OEEService es el contrato que consume el handler.
type OEEService interface {
	OEE(ctx context.Context, q oeeService.Query) (*oee.Resumen, error)
}

// OEEDeps agrupa las dependencias del handler.
type OEEDeps struct {
	Service OEEService
}

// HandleOEE maneja GET /api/v1/reportes/oee. El JWT (cualquier rol) se valida
// en el middleware antes de llegar acá. Sólo admite filtro por sector.
func HandleOEE(deps OEEDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}

		q := r.URL.Query()
		resumen, err := deps.Service.OEE(r.Context(), oeeService.Query{
			Desde:    q.Get("desde"),
			Hasta:    q.Get("hasta"),
			SectorID: q.Get("sector_id"),
		})
		if err != nil {
			if esErrorValidacionOEE(err) {
				response.Error(w, http.StatusBadRequest, err.Error(), nil)
				return
			}
			response.Error(w, http.StatusInternalServerError, "Error al calcular el OEE", nil)
			return
		}
		response.OK(w, "OEE del período", resumen)
	}
}

func esErrorValidacionOEE(err error) bool {
	return errors.Is(err, oee.ErrFechaInvalida) ||
		errors.Is(err, oee.ErrDesdeMayorHasta) ||
		errors.Is(err, oee.ErrRangoExcedido)
}
