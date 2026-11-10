package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	loteSectorService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
)

type fakeLookupCalidad struct {
	lote     *lotesector.Lote
	sectorID string
}

func (f fakeLookupCalidad) SectorDelDispositivo(context.Context, string) (*loteSectorService.SectorInfo, error) {
	return &loteSectorService.SectorInfo{SectorID: f.sectorID}, nil
}

func (f fakeLookupCalidad) GetByID(context.Context, string) (*lotesector.Lote, error) {
	return f.lote, nil
}

type fakeRepoCalidad struct {
	guardadas []alerta.Alerta
	duplicada bool
}

func (f *fakeRepoCalidad) Save(a *alerta.Alerta) error {
	if f.duplicada {
		return nil
	}
	a.ID = "alerta-1"
	f.guardadas = append(f.guardadas, *a)
	return nil
}
func (f *fakeRepoCalidad) GetByHornoID(string) ([]alerta.Alerta, error) { return nil, nil }
func (f *fakeRepoCalidad) Listar(context.Context, string) ([]alerta.Alerta, error) {
	return nil, nil
}
func (f *fakeRepoCalidad) Ack(context.Context, string, alerta.Usuario) (*alerta.Alerta, error) {
	return nil, nil
}

func postCalidad(h *AlertaCalidadHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lotes/lote-1/alertas-calidad", strings.NewReader(body))
	req = req.WithContext(deviceauth.WithPrincipal(req.Context(), deviceauth.Principal{DeviceID: "dev-1"}))
	rec := httptest.NewRecorder()
	h.Handle(rec, req)
	return rec
}

func TestAlertaCalidadCreaAlertaReal(t *testing.T) {
	repo := &fakeRepoCalidad{}
	h := NewAlertaCalidadHandler(repo, fakeLookupCalidad{
		lote:     &lotesector.Lote{ID: "lote-1", SectorID: "sec-1", Estado: lotesector.EstadoAbierto},
		sectorID: "sec-1",
	})

	rec := postCalidad(h, `{"tipo":"porcentaje_quemados_alto","porcentaje":12.3,"umbral":10,"ventana_segundos":10}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, repo.guardadas, 1)
	a := repo.guardadas[0]
	require.Equal(t, alerta.TipoPorcentajeQuemadosAlto, a.Tipo)
	require.Equal(t, "sec-1", *a.SectorID)
	require.Equal(t, "lote-1", *a.LoteID)
	require.Contains(t, a.Mensaje, "12.3%")
	require.NotContains(t, a.Mensaje, "PRUEBA")
}

func TestAlertaCalidadRechazos(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", SectorID: "sec-1", Estado: lotesector.EstadoAbierto}
	repo := &fakeRepoCalidad{}
	h := NewAlertaCalidadHandler(repo, fakeLookupCalidad{lote: lote, sectorID: "sec-1"})

	require.Equal(t, http.StatusBadRequest, postCalidad(h, `{"tipo":"TEMPERATURA_HORNO","porcentaje":1,"umbral":1}`).Code)
	require.Equal(t, http.StatusBadRequest, postCalidad(h, `{"tipo":"PORCENTAJE_CRUDOS_ALTO","porcentaje":150,"umbral":5}`).Code)

	ajeno := NewAlertaCalidadHandler(repo, fakeLookupCalidad{lote: lote, sectorID: "otro"})
	require.Equal(t, http.StatusForbidden, postCalidad(ajeno, `{"tipo":"PORCENTAJE_CRUDOS_ALTO","porcentaje":9,"umbral":5}`).Code)

	cerrado := NewAlertaCalidadHandler(repo, fakeLookupCalidad{
		lote:     &lotesector.Lote{ID: "lote-1", SectorID: "sec-1", Estado: lotesector.EstadoCerrado},
		sectorID: "sec-1",
	})
	require.Equal(t, http.StatusConflict, postCalidad(cerrado, `{"tipo":"PORCENTAJE_CRUDOS_ALTO","porcentaje":9,"umbral":5}`).Code)
	require.Empty(t, repo.guardadas)
}

func TestAlertaCalidadDuplicadaNoCreaOtra(t *testing.T) {
	repo := &fakeRepoCalidad{duplicada: true}
	h := NewAlertaCalidadHandler(repo, fakeLookupCalidad{
		lote:     &lotesector.Lote{ID: "lote-1", SectorID: "sec-1", Estado: lotesector.EstadoAbierto},
		sectorID: "sec-1",
	})
	rec := postCalidad(h, `{"tipo":"PORCENTAJE_CRUDOS_ALTO","porcentaje":9,"umbral":5}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"creada":false`)
}
