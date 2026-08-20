package reporte

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reporte"
	reporteService "github.com/angelobenedetti29/smart-check-automation/internal/service/reporte"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

type mockReporteService struct {
	kpiToReturn   *reporte.KPIFinanciero
	errToReturn   error
	capturedFiltro reporte.FiltroKPI
}

func (m *mockReporteService) CalcularKPIFinanciero(ctx context.Context, filtro reporte.FiltroKPI) (*reporte.KPIFinanciero, error) {
	m.capturedFiltro = filtro
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return m.kpiToReturn, nil
}

func TestGetKPIFinanciero_Success(t *testing.T) {
	expectedKPI := &reporte.KPIFinanciero{
		TotalImpactoEconomico: 15420.50,
		Moneda:                "ARS",
		TotalMermasUnidades:   45,
		TotalMermasConCosto:   45,
		TotalLotes:            10,
		TieneCostosFaltantes:  false,
		CalculadoAt:           time.Now().UTC(),
	}

	svc := &mockReporteService{kpiToReturn: expectedKPI}
	handler := NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/kpi-financiero?desde=2026-08-01&hasta=2026-08-20&turno=mañana&producto_id=prod-123", nil)
	rec := httptest.NewRecorder()

	handler.GetKPIFinanciero(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp response.Response
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.True(t, resp.Success)

	// Validar que se capturaron los filtros
	assert.Equal(t, "prod-123", svc.capturedFiltro.ProductoID)
	assert.Equal(t, "mañana", svc.capturedFiltro.Turno)
	require.NotNil(t, svc.capturedFiltro.FechaInicio)
	require.NotNil(t, svc.capturedFiltro.FechaFin)
	assert.Equal(t, 2026, svc.capturedFiltro.FechaInicio.Year())
	assert.Equal(t, time.Month(8), svc.capturedFiltro.FechaInicio.Month())
	assert.Equal(t, 1, svc.capturedFiltro.FechaInicio.Day())
}

func TestGetKPIFinanciero_MethodNotAllowed(t *testing.T) {
	svc := &mockReporteService{}
	handler := NewHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/reportes/kpi-financiero", nil)
	rec := httptest.NewRecorder()

	handler.GetKPIFinanciero(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestGetKPIFinanciero_InvalidDateFormat(t *testing.T) {
	svc := &mockReporteService{}
	handler := NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/kpi-financiero?fecha_inicio=fecha-invalida", nil)
	rec := httptest.NewRecorder()

	handler.GetKPIFinanciero(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetKPIFinanciero_ServiceValidationError(t *testing.T) {
	svc := &mockReporteService{errToReturn: reporteService.ErrRangoFechasInvalido}
	handler := NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/kpi-financiero", nil)
	rec := httptest.NewRecorder()

	handler.GetKPIFinanciero(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetKPIFinanciero_InternalServerError(t *testing.T) {
	svc := &mockReporteService{errToReturn: errors.New("db connection failure")}
	handler := NewHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/reportes/kpi-financiero", nil)
	rec := httptest.NewRecorder()

	handler.GetKPIFinanciero(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
