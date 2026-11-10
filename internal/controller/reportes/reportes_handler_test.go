package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reportes"
	reportesService "github.com/angelobenedetti29/smart-check-automation/internal/service/reportes"
)

type fakeReportesService struct {
	query        reportesService.Query
	resumen      *reportes.Resumen
	pagina       *reportes.PaginaLotes
	trazabilidad *reportes.Trazabilidad
	alertas      *reportes.PaginaAlertas
	err          error
}

func (f *fakeReportesService) Alertas(_ context.Context, q reportesService.Query) (*reportes.PaginaAlertas, error) {
	f.query = q
	if f.err != nil {
		return nil, f.err
	}
	if f.alertas != nil {
		return f.alertas, nil
	}
	return &reportes.PaginaAlertas{Alertas: []reportes.AlertaReporte{}}, nil
}

func (f *fakeReportesService) Trazabilidad(_ context.Context, q reportesService.Query) (*reportes.Trazabilidad, error) {
	f.query = q
	if f.err != nil {
		return nil, f.err
	}
	if f.trazabilidad != nil {
		return f.trazabilidad, nil
	}
	return &reportes.Trazabilidad{}, nil
}

func (f *fakeReportesService) Resumen(_ context.Context, q reportesService.Query) (*reportes.Resumen, error) {
	f.query = q
	if f.err != nil {
		return nil, f.err
	}
	if f.resumen != nil {
		return f.resumen, nil
	}
	return &reportes.Resumen{}, nil
}

func (f *fakeReportesService) Lotes(_ context.Context, q reportesService.Query) (*reportes.PaginaLotes, error) {
	f.query = q
	if f.err != nil {
		return nil, f.err
	}
	if f.pagina != nil {
		return f.pagina, nil
	}
	return &reportes.PaginaLotes{Lotes: []reportes.LoteReporte{}, Limite: 100}, nil
}

func TestHandleAlertas_200(t *testing.T) {
	svc := &fakeReportesService{alertas: &reportes.PaginaAlertas{
		Total:    1,
		Truncada: false,
		Alertas: []reportes.AlertaReporte{{
			ID:        "a1b2c3d4-5678-90ab-cdef-1234567890ab",
			Severidad: "CRITICAL",
			Estado:    "ACK",
			Mensaje:   "alta temp",
		}},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/alertas?desde=2026-10-01&hasta=2026-10-31&sector_id=horno-1&estado=ACK&limite=500", nil)
	rr := httptest.NewRecorder()
	HandleAlertas(ReportesDeps{Service: svc})(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "2026-10-01", svc.query.Desde)
	require.Equal(t, "2026-10-31", svc.query.Hasta)
	require.Equal(t, "horno-1", svc.query.SectorID)
	require.Equal(t, "ACK", svc.query.Estado)
	require.Equal(t, "500", svc.query.Limite)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, true, body["success"])
	require.Equal(t, "Alertas del período", body["message"])
	data := body["data"].(map[string]interface{})
	require.Equal(t, float64(1), data["total"])
	require.Equal(t, false, data["truncada"])
	require.Len(t, data["alertas"], 1)
}

func TestHandleAlertas_400Validaciones(t *testing.T) {
	casos := []struct {
		nombre string
		err    error
	}{
		{"fecha", reportes.ErrFechaInvalida},
		{"estado", reportes.ErrEstadoInvalido},
		{"limite", reportes.ErrLimiteInvalido},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			svc := &fakeReportesService{err: c.err}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/alertas", nil)
			rr := httptest.NewRecorder()
			HandleAlertas(ReportesDeps{Service: svc})(rr, req)
			require.Equal(t, http.StatusBadRequest, rr.Code)
		})
	}
}

func TestHandleAlertas_500(t *testing.T) {
	svc := &fakeReportesService{err: context.DeadlineExceeded}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/alertas", nil)
	rr := httptest.NewRecorder()
	HandleAlertas(ReportesDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestHandleAlertas_405(t *testing.T) {
	svc := &fakeReportesService{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reportes/alertas", nil)
	rr := httptest.NewRecorder()
	HandleAlertas(ReportesDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleResumen_200(t *testing.T) {
	svc := &fakeReportesService{resumen: &reportes.Resumen{
		Periodo: reportes.PeriodoResumen{Desde: "2026-10-01", Hasta: "2026-10-31", Timezone: reportes.Timezone},
		Produccion: reportes.Produccion{
			PorTurno:    []reportes.GrupoProduccion{},
			PorSector:   []reportes.GrupoProduccion{},
			PorProducto: []reportes.GrupoProduccion{},
		},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/resumen?desde=2026-10-01&hasta=2026-10-31&sector_id=horno-1&turno=tarde", nil)
	rr := httptest.NewRecorder()
	HandleResumen(ReportesDeps{Service: svc})(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "2026-10-01", svc.query.Desde)
	require.Equal(t, "2026-10-31", svc.query.Hasta)
	require.Equal(t, "horno-1", svc.query.SectorID)
	require.Equal(t, "tarde", svc.query.Turno)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, true, body["success"])
	require.Equal(t, "Resumen generado", body["message"])
}

func TestHandleResumen_400Validaciones(t *testing.T) {
	casos := []struct {
		nombre string
		err    error
	}{
		{"fecha", reportes.ErrFechaInvalida},
		{"desde>hasta", reportes.ErrDesdeMayorHasta},
		{"rango", reportes.ErrRangoExcedido},
		{"turno", reportes.ErrTurnoInvalido},
		{"producto", reportes.ErrProductoIDInvalido},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			svc := &fakeReportesService{err: c.err}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/resumen", nil)
			rr := httptest.NewRecorder()
			HandleResumen(ReportesDeps{Service: svc})(rr, req)
			require.Equal(t, http.StatusBadRequest, rr.Code)
		})
	}
}

func TestHandleResumen_500(t *testing.T) {
	svc := &fakeReportesService{err: context.DeadlineExceeded}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/resumen", nil)
	rr := httptest.NewRecorder()
	HandleResumen(ReportesDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestHandleResumen_405(t *testing.T) {
	svc := &fakeReportesService{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reportes/resumen", nil)
	rr := httptest.NewRecorder()
	HandleResumen(ReportesDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleLotes_200(t *testing.T) {
	svc := &fakeReportesService{pagina: &reportes.PaginaLotes{
		Lotes: []reportes.LoteReporte{{
			ID:             "a1b2c3d4-5678-90ab-cdef-1234567890ab",
			SectorID:       "horno-1",
			SectorNombre:   "Horno 1",
			ProductoNombre: "Tostada Integral",
			Estado:         "CERRADO",
		}},
		Total:           1,
		Limite:          100,
		SiguienteCursor: "cursor-abc",
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/lotes?desde=2026-10-01&hasta=2026-10-31&sector_id=horno-1&turno=tarde&limite=100&antes_de=cursor-abc", nil)
	rr := httptest.NewRecorder()
	HandleLotes(ReportesDeps{Service: svc})(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "2026-10-01", svc.query.Desde)
	require.Equal(t, "2026-10-31", svc.query.Hasta)
	require.Equal(t, "horno-1", svc.query.SectorID)
	require.Equal(t, "tarde", svc.query.Turno)
	require.Equal(t, "100", svc.query.Limite)
	require.Equal(t, "cursor-abc", svc.query.AntesDe)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, true, body["success"])
	require.Equal(t, "Lotes del reporte obtenidos", body["message"])
	require.Equal(t, "cursor-abc", body["siguiente_cursor"])
	require.Equal(t, float64(1), body["total"])
	require.Equal(t, float64(100), body["pageSize"])
	require.Len(t, body["data"], 1)
}

func TestHandleLotes_400Validaciones(t *testing.T) {
	casos := []struct {
		nombre string
		err    error
	}{
		{"fecha", reportes.ErrFechaInvalida},
		{"desde>hasta", reportes.ErrDesdeMayorHasta},
		{"rango", reportes.ErrRangoExcedido},
		{"turno", reportes.ErrTurnoInvalido},
		{"producto", reportes.ErrProductoIDInvalido},
		{"limite", reportes.ErrLimiteInvalido},
		{"cursor", reportes.ErrCursorInvalido},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			svc := &fakeReportesService{err: c.err}
			req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/lotes", nil)
			rr := httptest.NewRecorder()
			HandleLotes(ReportesDeps{Service: svc})(rr, req)
			require.Equal(t, http.StatusBadRequest, rr.Code)
		})
	}
}

func TestHandleLotes_500(t *testing.T) {
	svc := &fakeReportesService{err: context.DeadlineExceeded}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/lotes", nil)
	rr := httptest.NewRecorder()
	HandleLotes(ReportesDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestHandleLotes_405(t *testing.T) {
	svc := &fakeReportesService{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reportes/lotes", nil)
	rr := httptest.NewRecorder()
	HandleLotes(ReportesDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleTrazabilidad_200(t *testing.T) {
	svc := &fakeReportesService{trazabilidad: &reportes.Trazabilidad{
		Lote:       reportes.LoteTrazabilidad{LoteReporte: reportes.LoteReporte{ID: "a1b2c3d4-5678-90ab-cdef-1234567890ab"}},
		Decisiones: []reportes.DecisionTrazabilidad{},
		Consignas:  []reportes.ConsignaTrazabilidad{},
		Alertas:    []reportes.AlertaTrazabilidad{},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/trazabilidad?lote_id=a1b2c3d4-5678-90ab-cdef-1234567890ab", nil)
	rr := httptest.NewRecorder()
	HandleTrazabilidad(ReportesDeps{Service: svc})(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "a1b2c3d4-5678-90ab-cdef-1234567890ab", svc.query.LoteID)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, true, body["success"])
	require.Equal(t, "Trazabilidad del lote", body["message"])
	require.NotNil(t, body["data"])
}

func TestHandleTrazabilidad_400LoteIDInvalido(t *testing.T) {
	svc := &fakeReportesService{err: reportes.ErrLoteIDInvalido}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/trazabilidad", nil)
	rr := httptest.NewRecorder()
	HandleTrazabilidad(ReportesDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleTrazabilidad_404LoteNoEncontrado(t *testing.T) {
	svc := &fakeReportesService{err: reportes.ErrLoteNoEncontrado}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/trazabilidad?lote_id=a1b2c3d4-5678-90ab-cdef-1234567890ab", nil)
	rr := httptest.NewRecorder()
	HandleTrazabilidad(ReportesDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandleTrazabilidad_500(t *testing.T) {
	svc := &fakeReportesService{err: context.DeadlineExceeded}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/trazabilidad?lote_id=a1b2c3d4-5678-90ab-cdef-1234567890ab", nil)
	rr := httptest.NewRecorder()
	HandleTrazabilidad(ReportesDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestHandleTrazabilidad_405(t *testing.T) {
	svc := &fakeReportesService{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/reportes/trazabilidad", nil)
	rr := httptest.NewRecorder()
	HandleTrazabilidad(ReportesDeps{Service: svc})(rr, req)
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}
