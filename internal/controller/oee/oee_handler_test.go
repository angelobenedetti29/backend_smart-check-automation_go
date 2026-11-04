package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/oee"
	oeeService "github.com/angelobenedetti29/smart-check-automation/internal/service/oee"
)

type fakeOEEService struct {
	query   oeeService.Query
	resumen *oee.Resumen
	err     error
}

func (f *fakeOEEService) OEE(_ context.Context, q oeeService.Query) (*oee.Resumen, error) {
	f.query = q
	if f.err != nil {
		return nil, f.err
	}
	if f.resumen != nil {
		return f.resumen, nil
	}
	return &oee.Resumen{}, nil
}

func TestHandleOEE_200(t *testing.T) {
	svc := &fakeOEEService{resumen: &oee.Resumen{
		Periodo:   oee.PeriodoResumen{Desde: "2026-11-01", Hasta: "2026-11-04", Timezone: oee.Timezone},
		Global:    oee.ResumenSector{SectorID: "", SectorNombre: "Todos", Motivos: []string{}, Calculable: true},
		PorSector: []oee.ResumenSector{},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/oee?desde=2026-11-01&hasta=2026-11-04&sector_id=s1", nil)
	rr := httptest.NewRecorder()
	HandleOEE(OEEDeps{Service: svc})(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "2026-11-01", svc.query.Desde)
	require.Equal(t, "2026-11-04", svc.query.Hasta)
	require.Equal(t, "s1", svc.query.SectorID)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, true, body["success"])
	require.Equal(t, "OEE del período", body["message"])
	data := body["data"].(map[string]interface{})
	global := data["global"].(map[string]interface{})
	require.Equal(t, true, global["calculable"])
	require.Len(t, data["por_sector"], 0)
}

func TestHandleOEE_400Validaciones(t *testing.T) {
	casos := []struct {
		nombre string
		err    error
	}{
		{"fecha", oee.ErrFechaInvalida},
		{"desde>hasta", oee.ErrDesdeMayorHasta},
		{"rango", oee.ErrRangoExcedido},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			svc := &fakeOEEService{err: c.err}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/oee", nil)
			rr := httptest.NewRecorder()
			HandleOEE(OEEDeps{Service: svc})(rr, req)
			require.Equal(t, http.StatusBadRequest, rr.Code)
		})
	}
}

func TestHandleOEE_500(t *testing.T) {
	svc := &fakeOEEService{err: context.DeadlineExceeded}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/oee", nil)
	rr := httptest.NewRecorder()
	HandleOEE(OEEDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestHandleOEE_405(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reportes/oee", nil)
	rr := httptest.NewRecorder()
	HandleOEE(OEEDeps{Service: &fakeOEEService{}})(rr, req)
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}
