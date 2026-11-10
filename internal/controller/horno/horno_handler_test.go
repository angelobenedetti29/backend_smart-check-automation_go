package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
)

// fakeAlertaRepo implementa alerta.Repository en memoria para los handlers.
type fakeAlertaRepo struct {
	guardadas []alerta.Alerta
}

func (f *fakeAlertaRepo) Save(a *alerta.Alerta) error {
	a.ID = "alerta-1"
	f.guardadas = append(f.guardadas, *a)
	return nil
}

func (f *fakeAlertaRepo) GetByHornoID(string) ([]alerta.Alerta, error) {
	return f.guardadas, nil
}

func (f *fakeAlertaRepo) Listar(context.Context, string) ([]alerta.Alerta, error) {
	return f.guardadas, nil
}

func (f *fakeAlertaRepo) Ack(context.Context, string, alerta.Usuario) (*alerta.Alerta, error) {
	return nil, alerta.ErrNotFound
}

type fakeSectorLookup struct {
	sectores map[string]*sector.Sector
}

func (f *fakeSectorLookup) GetByID(ctx context.Context, sectorID string) (*sector.Sector, error) {
	if s, ok := f.sectores[sectorID]; ok {
		return s, nil
	}
	return nil, sector.ErrSectorNotFound
}

type fakeLoteAbiertoLookup struct {
	lote *lote_sector.Lote
	err  error
}

func (f *fakeLoteAbiertoLookup) GetAbiertoBySector(ctx context.Context, sectorID string) (*lote_sector.Lote, error) {
	return f.lote, f.err
}

type fakeMetricasLookup struct {
	muestras []metricashorno.MetricaHorno
	err      error
}

func (f *fakeMetricasLookup) GetUltimasParaLote(ctx context.Context, loteID string, limite int) ([]metricashorno.MetricaHorno, error) {
	return f.muestras, f.err
}

// newGetHandler arma un handler con fakes.
func newGetHandler(sectorEntity *sector.Sector, lote *lote_sector.Lote, muestras []metricashorno.MetricaHorno, aRepo *fakeAlertaRepo) *HornoHandler {
	sectores := &fakeSectorLookup{sectores: map[string]*sector.Sector{}}
	if sectorEntity != nil {
		sectores.sectores[sectorEntity.ID] = sectorEntity
	}
	if aRepo == nil {
		aRepo = &fakeAlertaRepo{}
	}
	return NewHornoHandler(aRepo, sectores, &fakeLoteAbiertoLookup{lote: lote}, &fakeMetricasLookup{muestras: muestras})
}

func getHornoData(t *testing.T, handler *HornoHandler, url string) (int, map[string]interface{}) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	rr := httptest.NewRecorder()
	handler.GetHornoStatus(rr, req)

	var body map[string]interface{}
	if rr.Body.Len() > 0 {
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("failed to decode response JSON: %v", err)
		}
	}
	return rr.Code, body
}

func hornoDTO(t *testing.T, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, got %v", body["data"])
	}
	hornoMap, ok := data["horno"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data.horno object, got %v", data["horno"])
	}
	return hornoMap
}

func TestHornoHandler_GetHornoStatus_SectorInexistente(t *testing.T) {
	handler := newGetHandler(nil, nil, nil, nil)

	code, _ := getHornoData(t, handler, "/api/v1/horno?sector_id=horno-01")
	if code != http.StatusNotFound {
		t.Errorf("expected 404 para sector inexistente, got %d", code)
	}
}

func TestHornoHandler_GetHornoStatus_SinLote(t *testing.T) {
	s := &sector.Sector{ID: "horno-01", Nombre: "Horno 1"}
	handler := newGetHandler(s, nil, nil, nil)

	code, body := getHornoData(t, handler, "/api/v1/horno?sector_id=horno-01")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	h := hornoDTO(t, body)
	if h["estado"] != EstadoSinLote {
		t.Errorf("expected estado SIN_LOTE, got %v", h["estado"])
	}
	if h["fuente"] != FuenteSinLote {
		t.Errorf("expected fuente sin_lote, got %v", h["fuente"])
	}
	if h["temperatura"] != nil {
		t.Errorf("expected temperatura null sin telemetría, got %v", h["temperatura"])
	}
	if h["id"] != "horno-01" || h["nombre"] != "Horno 1" || h["sector_id"] != "horno-01" {
		t.Errorf("unexpected horno identity: %v", h)
	}
}

func TestHornoHandler_GetHornoStatus_SinTelemetria(t *testing.T) {
	s := &sector.Sector{ID: "horno-01", Nombre: "Horno 1"}
	lote := &lote_sector.Lote{ID: "lote-1", ProductoID: "prod-1"}
	handler := newGetHandler(s, lote, nil, nil)

	code, body := getHornoData(t, handler, "/api/v1/horno?sector_id=horno-01")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	h := hornoDTO(t, body)
	if h["estado"] != EstadoSinTelemetria {
		t.Errorf("expected estado SIN_TELEMETRIA, got %v", h["estado"])
	}
	if h["fuente"] != FuenteSinTelemetria {
		t.Errorf("expected fuente sin_telemetria, got %v", h["fuente"])
	}
	if h["lote_id"] != "lote-1" || h["producto_id"] != "prod-1" {
		t.Errorf("expected lote/producto del lote abierto, got %v", h)
	}
	if h["temperatura"] != nil || h["velocidad_cinta"] != nil {
		t.Errorf("expected T/V null sin telemetría, got %v/%v", h["temperatura"], h["velocidad_cinta"])
	}
}

func TestHornoHandler_GetHornoStatus_Activo(t *testing.T) {
	s := &sector.Sector{ID: "horno-01", Nombre: "Horno 1"}
	lote := &lote_sector.Lote{ID: "lote-1", ProductoID: "prod-1"}
	temp, vel := 170.5, 0.21
	muestras := []metricashorno.MetricaHorno{{
		TempHorno1:     &temp,
		VelocidadCinta: &vel,
		ReceivedAt:     time.Now().UTC(),
	}}
	handler := newGetHandler(s, lote, muestras, nil)

	code, body := getHornoData(t, handler, "/api/v1/horno?sector_id=horno-01")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	h := hornoDTO(t, body)
	if h["estado"] != EstadoActivo {
		t.Errorf("expected estado ACTIVO, got %v", h["estado"])
	}
	if h["fuente"] != FuenteTelemetria {
		t.Errorf("expected fuente telemetria, got %v", h["fuente"])
	}
	if h["temperatura"] != 170.5 || h["velocidad_cinta"] != 0.21 {
		t.Errorf("expected T/V reales 170.5/0.21, got %v/%v", h["temperatura"], h["velocidad_cinta"])
	}
	if h["ultimo_check"] == nil || h["antiguedad_segundos"] == nil {
		t.Errorf("expected ultimo_check y antiguedad no nulos, got %v/%v", h["ultimo_check"], h["antiguedad_segundos"])
	}
}

func TestHornoHandler_GetHornoStatus_SinDatos(t *testing.T) {
	s := &sector.Sector{ID: "horno-01", Nombre: "Horno 1"}
	lote := &lote_sector.Lote{ID: "lote-1"}
	muestras := []metricashorno.MetricaHorno{{ReceivedAt: time.Now().UTC()}}
	handler := newGetHandler(s, lote, muestras, nil)

	_, body := getHornoData(t, handler, "/api/v1/horno?sector_id=horno-01")
	h := hornoDTO(t, body)
	if h["estado"] != EstadoSinDatos {
		t.Errorf("expected estado SIN_DATOS, got %v", h["estado"])
	}
}

func TestHornoHandler_GetHornoStatus_Desactualizado(t *testing.T) {
	s := &sector.Sector{ID: "horno-01", Nombre: "Horno 1"}
	lote := &lote_sector.Lote{ID: "lote-1"}
	temp, vel := 170.5, 0.21
	muestras := []metricashorno.MetricaHorno{{
		TempHorno1:     &temp,
		VelocidadCinta: &vel,
		ReceivedAt:     time.Now().UTC().Add(-2 * time.Minute),
	}}
	handler := newGetHandler(s, lote, muestras, nil)

	_, body := getHornoData(t, handler, "/api/v1/horno?sector_id=horno-01")
	h := hornoDTO(t, body)
	if h["estado"] != EstadoDesactualizado {
		t.Errorf("expected estado DESACTUALIZADO, got %v", h["estado"])
	}
	if h["temperatura"] != 170.5 {
		t.Errorf("expected última T real 170.5, got %v", h["temperatura"])
	}
}

func TestHornoHandler_GetHornoStatus_SinParametros(t *testing.T) {
	handler := newGetHandler(nil, nil, nil, nil)

	code, _ := getHornoData(t, handler, "/api/v1/horno")
	if code != http.StatusBadRequest {
		t.Errorf("expected status 400 sin sector_id, got %d", code)
	}
}
