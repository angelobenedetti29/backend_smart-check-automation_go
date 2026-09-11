package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

type fakeDispositivoService struct {
	pingResp   *dispositivo.EstadoDispositivo
	pingErr    error
	pingCalls  int
	estados    []dispositivo.EstadoDispositivo
	metricas   *dispositivo.PaginatedResult
	metrErr    error
	createResp *dispositivo.EstadoDispositivo
	createErr  error
	createReq  dispositivo.CreateDispositivoRequest
	updateResp *dispositivo.EstadoDispositivo
	updateErr  error
	updateReq  dispositivo.UpdateDispositivoRequest
	deleteErr  error
}

func (f *fakeDispositivoService) ProcessPing(ctx context.Context, req dispositivo.PingRequest) (*dispositivo.EstadoDispositivo, error) {
	f.pingCalls++
	return f.pingResp, f.pingErr
}

func (f *fakeDispositivoService) GetAllEstados() []dispositivo.EstadoDispositivo {
	return f.estados
}

func (f *fakeDispositivoService) GetMetricas(ctx context.Context, id string, page, pageSize int) (*dispositivo.PaginatedResult, error) {
	return f.metricas, f.metrErr
}

func (f *fakeDispositivoService) Create(ctx context.Context, req dispositivo.CreateDispositivoRequest) (*dispositivo.EstadoDispositivo, error) {
	f.createReq = req
	return f.createResp, f.createErr
}

func (f *fakeDispositivoService) Update(ctx context.Context, req dispositivo.UpdateDispositivoRequest) (*dispositivo.EstadoDispositivo, error) {
	f.updateReq = req
	return f.updateResp, f.updateErr
}

func (f *fakeDispositivoService) Delete(ctx context.Context, id string) error {
	return f.deleteErr
}

const testAPIKey = "test-secret-key"

func TestHandlePing_UnauthorizedWithoutAPIKey(t *testing.T) {
	t.Setenv("API_KEY_SECRET", testAPIKey)

	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":"b1c2d3e4-5678-90ab-cdef-1234567890ab","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50,"aiProcessorPct":42}`)

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
	body := bytes.NewBufferString(`{"dispositivoId":"b1c2d3e4-5678-90ab-cdef-1234567890ab"}`)

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
	body := bytes.NewBufferString(`{"dispositivoId":"","cpuPct":200,"memRamDisponibleMb":-1,"tempChip":500,"aiProcessorPct":150}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestHandlePing_MalformedDispositivoIDIsClientValidationError(t *testing.T) {
	t.Setenv("API_KEY_SECRET", testAPIKey)

	service := &fakeDispositivoService{}
	h := NewDispositivoHandler(service)
	body := bytes.NewBufferString(`{"dispositivoId":"not-a-uuid","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50,"aiProcessorPct":42}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for malformed dispositivoId, got %d", rec.Code)
	}
	if service.pingCalls != 0 {
		t.Fatalf("expected malformed ping not to reach service, got %d calls", service.pingCalls)
	}
}

func TestHandlePing_DispositivoNoExiste(t *testing.T) {
	t.Setenv("API_KEY_SECRET", testAPIKey)

	h := NewDispositivoHandler(&fakeDispositivoService{pingErr: dispositivo.ErrDispositivoNotFound})
	body := bytes.NewBufferString(`{"dispositivoId":"00000000-0000-0000-0000-000000000000","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50,"aiProcessorPct":42}`)

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
			DispositivoID: "d1", CpuPct: 10, MemRamDisponibleMb: 500, TempChip: 50, AiProcessorPct: 42, ReceivedAt: now,
		},
		LastSeen: &now,
	}
	h := NewDispositivoHandler(&fakeDispositivoService{pingResp: estado})
	body := bytes.NewBufferString(`{"dispositivoId":"b1c2d3e4-5678-90ab-cdef-1234567890ab","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50,"aiProcessorPct":42}`)

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

func TestHandleCreate_Success(t *testing.T) {
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "d-nuevo",
		Nombre:        "Raspberry Pi Horno 2",
		Ubicacion:     "Línea B",
		Estado:        dispositivo.EstadoOffline,
	}
	h := NewDispositivoHandler(&fakeDispositivoService{createResp: estado})
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno 2","ubicacion":"Línea B"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
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
	if data["estado"] != dispositivo.EstadoOffline {
		t.Fatalf("expected estado=offline in response, got %v", data["estado"])
	}
	if data["nombre"] != "Raspberry Pi Horno 2" {
		t.Fatalf("expected nombre in response, got %v", data["nombre"])
	}
}

func TestHandleCreate_EmptyNombre(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"nombre":"   ","ubicacion":"Línea B"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}

	var resp response.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("error decoding response: %v", err)
	}
	if resp.Success {
		t.Fatal("expected success=false")
	}
}

func TestHandleCreate_InvalidJSON(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"nombre":`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleCreate_InvalidContentType(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno 2"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rec.Code)
	}
}

func TestHandleCreate_InternalError(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{createErr: errors.New("db caída")})
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno 2"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestHandleUpdate_Success(t *testing.T) {
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "d1",
		Nombre:        "Pi 1",
		Ubicacion:     "Línea A",
		Estado:        dispositivo.EstadoOffline,
	}
	h := NewDispositivoHandler(&fakeDispositivoService{updateResp: estado})
	body := bytes.NewBufferString(`{"dispositivoId":"d1","nombre":"Pi 1","ubicacion":"Línea A"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

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
	if data["nombre"] != "Pi 1" {
		t.Fatalf("expected nombre in response, got %v", data["nombre"])
	}
}

func TestHandleUpdate_EmptyNombre(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":"d1","nombre":"   ","ubicacion":"Línea A"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestHandleUpdate_InvalidJSON(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleUpdate_InvalidContentType(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":"d1","nombre":"Pi 1"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rec.Code)
	}
}

func TestHandleUpdate_NotFound(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{updateErr: dispositivo.ErrDispositivoNotFound})
	body := bytes.NewBufferString(`{"dispositivoId":"missing","nombre":"Pi 1"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleDelete_Success(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/dispositivos?dispositivoId=d1", nil)
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

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

func TestHandleDelete_MissingID(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/dispositivos", nil)
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleDelete_NotFound(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{deleteErr: dispositivo.ErrDispositivoNotFound})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/dispositivos?dispositivoId=missing", nil)
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandle_WrongMethod(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dispositivos", nil)
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

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

func TestHandleCreate_RoundTripWhepURL(t *testing.T) {
	const whep = "https://camaras.example.com/whep/horno-2"
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "d-nuevo",
		Nombre:        "Raspberry Pi Horno 2",
		WhepURL:       whep,
		Estado:        dispositivo.EstadoOffline,
	}
	svc := &fakeDispositivoService{createResp: estado}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno 2","whepUrl":"` + whep + `"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if svc.createReq.WhepURL != whep {
		t.Fatalf("expected handler to decode whepUrl, got %q", svc.createReq.WhepURL)
	}

	var resp response.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("error decoding response: %v", err)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, got %T", resp.Data)
	}
	if data["whepUrl"] != whep {
		t.Fatalf("expected whepUrl in response, got %v", data["whepUrl"])
	}
}

func TestHandleCreate_RejectsInvalidWhepURL(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno 2","whepUrl":"no-es-url"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid whepUrl, got %d", rec.Code)
	}
	if svc.createReq.WhepURL != "" {
		t.Fatalf("expected invalid request not to reach service, got %q", svc.createReq.WhepURL)
	}
}

func TestHandleUpdate_RoundTripWhepURL(t *testing.T) {
	const whep = "https://camaras.example.com/whep/horno-1"
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "d1",
		Nombre:        "Pi 1",
		WhepURL:       whep,
		Estado:        dispositivo.EstadoOffline,
	}
	svc := &fakeDispositivoService{updateResp: estado}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"dispositivoId":"d1","nombre":"Pi 1","whepUrl":"` + whep + `"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if svc.updateReq.WhepURL != whep {
		t.Fatalf("expected handler to decode whepUrl, got %q", svc.updateReq.WhepURL)
	}

	var resp response.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("error decoding response: %v", err)
	}
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data object, got %T", resp.Data)
	}
	if data["whepUrl"] != whep {
		t.Fatalf("expected whepUrl in response, got %v", data["whepUrl"])
	}
}

func TestHandleEstados_ExponeWhepURL(t *testing.T) {
	const whep = "https://camaras.example.com/whep/horno-1"
	estados := []dispositivo.EstadoDispositivo{
		{DispositivoID: "d1", Nombre: "Pi 1", WhepURL: whep, Estado: dispositivo.EstadoOnline},
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
	items, ok := resp.Data.([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("expected one device in data, got %T %+v", resp.Data, resp.Data)
	}
	item, ok := items[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected device object, got %T", items[0])
	}
	if item["whepUrl"] != whep {
		t.Fatalf("expected whepUrl in catalog response, got %v", item["whepUrl"])
	}
}
