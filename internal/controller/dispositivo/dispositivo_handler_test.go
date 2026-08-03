package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

type fakeDispositivoService struct {
	pingResp *dispositivo.EstadoDispositivo
	pingErr  error
	estados  []dispositivo.EstadoDispositivo
	metricas *dispositivo.PaginatedResult
	metrErr  error
}

func (f *fakeDispositivoService) ProcessPing(ctx context.Context, req dispositivo.PingRequest) (*dispositivo.EstadoDispositivo, error) {
	return f.pingResp, f.pingErr
}

func (f *fakeDispositivoService) GetAllEstados() []dispositivo.EstadoDispositivo {
	return f.estados
}

func (f *fakeDispositivoService) GetMetricas(ctx context.Context, id string, page, pageSize int) (*dispositivo.PaginatedResult, error) {
	return f.metricas, f.metrErr
}

const testAPIKey = "test-secret-key"

func TestHandlePing_UnauthorizedWithoutAPIKey(t *testing.T) {
	t.Setenv("API_KEY_SECRET", testAPIKey)

	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":"d1","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandlePing_WrongMethod(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dispositivos/ping", nil)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandlePing_InvalidContentType(t *testing.T) {
	t.Setenv("API_KEY_SECRET", testAPIKey)

	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":"d1"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rec.Code)
	}
}

func TestHandlePing_InvalidJSON(t *testing.T) {
	t.Setenv("API_KEY_SECRET", testAPIKey)

	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandlePing_ValidationError(t *testing.T) {
	t.Setenv("API_KEY_SECRET", testAPIKey)

	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":"","cpuPct":200,"memRamDisponibleMb":-1,"tempChip":500}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestHandlePing_DispositivoNoExiste(t *testing.T) {
	t.Setenv("API_KEY_SECRET", testAPIKey)

	h := NewDispositivoHandler(&fakeDispositivoService{pingErr: dispositivo.ErrDispositivoNotFound})
	body := bytes.NewBufferString(`{"dispositivoId":"missing","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestHandlePing_Success(t *testing.T) {
	t.Setenv("API_KEY_SECRET", testAPIKey)

	now := time.Now().UTC()
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "d1",
		Nombre:        "Pi 1",
		Ubicacion:     "Línea A",
		Estado:        dispositivo.EstadoOnline,
		UltimaMetrica: &dispositivo.MetricaDispositivo{
			DispositivoID: "d1", CpuPct: 10, MemRamDisponibleMb: 500, TempChip: 50, ReceivedAt: now,
		},
		LastSeen: &now,
	}
	h := NewDispositivoHandler(&fakeDispositivoService{pingResp: estado})
	body := bytes.NewBufferString(`{"dispositivoId":"d1","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp response.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("error decoding response: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}

	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, got %T", resp.Data)
	}
	if data["estado"] != dispositivo.EstadoOnline {
		t.Fatalf("expected estado=online in response, got %v", data["estado"])
	}
}

func TestHandleEstados_Success(t *testing.T) {
	estados := []dispositivo.EstadoDispositivo{
		{DispositivoID: "d1", Nombre: "Pi 1", Estado: dispositivo.EstadoOnline},
	}
	h := NewDispositivoHandler(&fakeDispositivoService{estados: estados})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dispositivos", nil)
	rec := httptest.NewRecorder()

	h.HandleEstados(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp response.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("error decoding response: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
}

func TestHandleEstados_WrongMethod(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", nil)
	rec := httptest.NewRecorder()

	h.HandleEstados(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleMetricas_MissingDispositivoID(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dispositivos/metricas", nil)
	rec := httptest.NewRecorder()

	h.HandleMetricas(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleMetricas_DispositivoNoExiste(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{metrErr: dispositivo.ErrDispositivoNotFound})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dispositivos/metricas?dispositivoId=missing", nil)
	rec := httptest.NewRecorder()

	h.HandleMetricas(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleMetricas_Success(t *testing.T) {
	now := time.Now().UTC()
	result := &dispositivo.PaginatedResult{
		Items: []dispositivo.MetricaDispositivo{
			{ID: "m1", DispositivoID: "d1", CpuPct: 10, MemRamDisponibleMb: 500, TempChip: 50, ReceivedAt: now},
		},
		Total:    1,
		Page:     1,
		PageSize: 10,
	}
	h := NewDispositivoHandler(&fakeDispositivoService{metricas: result})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dispositivos/metricas?dispositivoId=d1&page=1&pageSize=10", nil)
	rec := httptest.NewRecorder()

	h.HandleMetricas(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp response.PaginatedResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("error decoding response: %v", err)
	}
	if resp.Total != 1 || resp.Page != 1 || resp.PageSize != 10 {
		t.Fatalf("unexpected pagination metadata: total=%d page=%d pageSize=%d", resp.Total, resp.Page, resp.PageSize)
	}
}
