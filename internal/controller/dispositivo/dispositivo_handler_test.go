package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

type fakeDispositivoService struct {
	pingResp    *dispositivo.EstadoDispositivo
	pingErr     error
	pingCalls   int
	pingReq     dispositivo.PingRequest
	estados     []dispositivo.EstadoDispositivo
	metricas    *dispositivo.PaginatedResult
	metrErr     error
	createResp  *dispositivo.EstadoDispositivo
	createErr   error
	createReq   dispositivo.CreateDispositivoRequest
	createCalls int
	updateResp  *dispositivo.EstadoDispositivo
	updateErr   error
	updateReq   dispositivo.UpdateDispositivoRequest
	updateCalls int
	renameResp  *dispositivo.EstadoDispositivo
	renameErr   error
	renameID    string
	renameName  string
	renameCalls int

	renameFromPanelResp  *dispositivo.EstadoDispositivo
	renameFromPanelErr   error
	renameFromPanelID    string
	renameFromPanelName  string
	renameFromPanelCalls int

	renameURLErr   error
	renameURLID    string
	renameURLValue string
	renameURLCalls int
	revokeErr      error
	revokeActor    string
	revokeID       string
	revokeCalls    int
}

func (f *fakeDispositivoService) ProcessPing(ctx context.Context, req dispositivo.PingRequest) (*dispositivo.EstadoDispositivo, error) {
	f.pingCalls++
	f.pingReq = req
	return f.pingResp, f.pingErr
}

func (f *fakeDispositivoService) GetAllEstados() []dispositivo.EstadoDispositivo {
	return f.estados
}

func (f *fakeDispositivoService) GetMetricas(ctx context.Context, id string, page, pageSize int) (*dispositivo.PaginatedResult, error) {
	return f.metricas, f.metrErr
}

func (f *fakeDispositivoService) Create(ctx context.Context, req dispositivo.CreateDispositivoRequest) (*dispositivo.EstadoDispositivo, error) {
	f.createCalls++
	f.createReq = req
	return f.createResp, f.createErr
}

func (f *fakeDispositivoService) Update(ctx context.Context, req dispositivo.UpdateDispositivoRequest) (*dispositivo.EstadoDispositivo, error) {
	f.updateCalls++
	f.updateReq = req
	return f.updateResp, f.updateErr
}

func (f *fakeDispositivoService) Rename(ctx context.Context, deviceID, nombre string) (*dispositivo.EstadoDispositivo, error) {
	f.renameCalls++
	f.renameID = deviceID
	f.renameName = nombre
	return f.renameResp, f.renameErr
}

func (f *fakeDispositivoService) RenameFromPanel(ctx context.Context, deviceID, nombre string) (*dispositivo.EstadoDispositivo, error) {
	f.renameFromPanelCalls++
	f.renameFromPanelID = deviceID
	f.renameFromPanelName = nombre
	return f.renameFromPanelResp, f.renameFromPanelErr
}

func (f *fakeDispositivoService) UpdateRenameURL(ctx context.Context, deviceID, renameURL string) error {
	f.renameURLCalls++
	f.renameURLID = deviceID
	f.renameURLValue = renameURL
	return f.renameURLErr
}

func (f *fakeDispositivoService) Revoke(ctx context.Context, actorEmail, id string) error {
	f.revokeCalls++
	f.revokeActor = actorEmail
	f.revokeID = id
	return f.revokeErr
}

func withTestDevicePrincipal(req *http.Request) *http.Request {
	return req.WithContext(deviceauth.WithPrincipal(req.Context(), deviceauth.Principal{DeviceID: "b1c2d3e4-5678-90ab-cdef-1234567890ab"}))
}

func TestHandlePing_UnauthorizedWithoutAPIKey(t *testing.T) {

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

	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":"b1c2d3e4-5678-90ab-cdef-1234567890ab"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rec.Code)
	}
}

func TestHandlePing_InvalidJSON(t *testing.T) {

	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandlePing_ValidationError(t *testing.T) {

	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"dispositivoId":"","cpuPct":200,"memRamDisponibleMb":-1,"tempChip":500,"aiProcessorPct":150}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestHandlePing_PrincipalOverridesBodyDispositivoID(t *testing.T) {

	service := &fakeDispositivoService{}
	h := NewDispositivoHandler(service)
	body := bytes.NewBufferString(`{"dispositivoId":"not-a-uuid","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50,"aiProcessorPct":42}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if service.pingCalls != 1 {
		t.Fatalf("expected ping to reach service, got %d calls", service.pingCalls)
	}
	if service.pingReq.DispositivoID != "b1c2d3e4-5678-90ab-cdef-1234567890ab" {
		t.Fatalf("expected principal id injected, got %q", service.pingReq.DispositivoID)
	}
}

func TestHandlePing_DispositivoNoExiste(t *testing.T) {

	h := NewDispositivoHandler(&fakeDispositivoService{pingErr: dispositivo.ErrDispositivoNotFound})
	body := bytes.NewBufferString(`{"dispositivoId":"00000000-0000-0000-0000-000000000000","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50,"aiProcessorPct":42}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(deviceauth.WithPrincipal(req.Context(), deviceauth.Principal{DeviceID: "00000000-0000-0000-0000-000000000000"}))
	rec := httptest.NewRecorder()

	h.HandlePing(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestHandlePing_Success(t *testing.T) {

	now := time.Now().UTC()
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "d1",
		Nombre:        "Pi 1",
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
	req = withTestDevicePrincipal(req)
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
	sectorID := "horno-1"
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "d-nuevo",
		Nombre:        "Raspberry Pi Horno",
		SectorID:      &sectorID,
		Estado:        dispositivo.EstadoOffline,
	}
	h := NewDispositivoHandler(&fakeDispositivoService{createResp: estado})
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno","sectorId":"horno-1","type":"ENTRADA_HORNO"}`)

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
	if data["nombre"] != "Raspberry Pi Horno" {
		t.Fatalf("expected nombre in response, got %v", data["nombre"])
	}
	if data["sectorId"] != sectorID {
		t.Fatalf("expected sectorId in response, got %v", data["sectorId"])
	}
}

func TestHandleCreate_EmptyNombre(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"nombre":"   ","sectorId":"horno-1","type":"ENTRADA_HORNO"}`)

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

func TestHandleCreate_SinType(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing type, got %d", rec.Code)
	}
	if svc.createCalls != 0 {
		t.Fatalf("invalid request must not reach service, got %d calls", svc.createCalls)
	}
}

func TestHandleCreate_TypeInvalido(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno","type":"HORNO"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid type, got %d", rec.Code)
	}
	if svc.createCalls != 0 {
		t.Fatalf("invalid request must not reach service, got %d calls", svc.createCalls)
	}
}

// TestHandleUpdate_TypeInmutable verifica que el tipo no se pueda cambiar por
// PUT: al no formar parte del DTO de actualización, el decode estricto rechaza
// el campo con 400 antes de tocar el service.
func TestHandleUpdate_TypeInmutable(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"dispositivoId":"11111111-1111-1111-1111-111111111111","type":"SALIDA_HORNO"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for immutable type, got %d", rec.Code)
	}
	if svc.updateCalls != 0 {
		t.Fatalf("update must not reach service, got %d calls", svc.updateCalls)
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
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno"}`)

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
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno","type":"ENTRADA_HORNO"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestHandleUpdate_Success(t *testing.T) {
	sectorID := "horno-1"
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "d1",
		Nombre:        "Pi 1",
		SectorID:      &sectorID,
		Estado:        dispositivo.EstadoOffline,
	}
	h := NewDispositivoHandler(&fakeDispositivoService{updateResp: estado})
	body := bytes.NewBufferString(`{"dispositivoId":"11111111-1111-1111-1111-111111111111","sectorId":"horno-1"}`)

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

// TestHandleUpdate_RechazaNombre verifica que el nombre ya no sea aceptado por el
// decode estricto del panel: solo el rename autenticado del nodo puede cambiarlo.
func TestHandleUpdate_RechazaNombre(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"dispositivoId":"11111111-1111-1111-1111-111111111111","nombre":"Pi 1","sectorId":"horno-1"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for immutable nombre field, got %d", rec.Code)
	}
	if svc.updateCalls != 0 {
		t.Fatalf("invalid request must not reach service, got %d calls", svc.updateCalls)
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
	body := bytes.NewBufferString(`{"dispositivoId":"11111111-1111-1111-1111-111111111111"}`)

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
	body := bytes.NewBufferString(`{"dispositivoId":"99999999-9999-9999-9999-999999999999"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestHandleUpdate_SectorNoExiste verifica que un sectorId inexistente se
// traduzca a 400.
func TestHandleUpdate_SectorNoExiste(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{updateErr: dispositivo.ErrSectorNotFound})
	body := bytes.NewBufferString(`{"dispositivoId":"11111111-1111-1111-1111-111111111111","sectorId":"no-existe"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// TestHandleUpdate_SectorTipoDuplicado verifica que la violación del índice
// único se traduzca a 409.
func TestHandleUpdate_SectorTipoDuplicado(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{updateErr: dispositivo.ErrSectorTipoDuplicado})
	body := bytes.NewBufferString(`{"dispositivoId":"11111111-1111-1111-1111-111111111111","sectorId":"horno-1"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

// TestHandleUpdate_RechazaUbicacionLegacy verifica que el campo ubicacion ya no
// sea aceptado por el decode estricto.
func TestHandleUpdate_RechazaUbicacionLegacy(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"dispositivoId":"11111111-1111-1111-1111-111111111111","ubicacion":"Línea A"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for removed ubicacion field, got %d", rec.Code)
	}
	if svc.updateCalls != 0 {
		t.Fatalf("invalid request must not reach service, got %d calls", svc.updateCalls)
	}
}

// testJWTSecret firma las cookies de prueba para ejercitar el handler DELETE,
// que toma el actor de los claims inyectados por JWTMiddleware.
var testJWTSecret = []byte("test-secret-32-chars-exactly!!!")

// buildSupervisorCookie genera una cookie session_token válida con rol Supervisor.
func buildSupervisorCookie(t *testing.T) *http.Cookie {
	t.Helper()
	claims := &authService.Claims{
		Email: "supervisor@fermar.com.ar",
		Name:  "Supervisor Test",
		Role:  "Supervisor",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(testJWTSecret)
	if err != nil {
		t.Fatalf("error generando token de prueba: %v", err)
	}
	return &http.Cookie{Name: "session_token", Value: signed}
}

// withTestSupervisorJWT adjunta al request una cookie de Supervisor válida.
// Útil para ejercitar los handlers que dependen de JWTMiddleware.
func withTestSupervisorJWT(t *testing.T, req *http.Request) *http.Request {
	t.Helper()
	req.AddCookie(buildSupervisorCookie(t))
	return req
}

func TestHandleDelete_Success(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/dispositivos?dispositivoId=d1", nil)
	req.AddCookie(buildSupervisorCookie(t))
	rec := httptest.NewRecorder()

	authController.JWTMiddleware(testJWTSecret, h.Handle)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if svc.revokeCalls != 1 {
		t.Fatalf("expected revoke to reach service, got %d calls", svc.revokeCalls)
	}
	if svc.revokeActor != "supervisor@fermar.com.ar" {
		t.Fatalf("expected actor from claims, got %q", svc.revokeActor)
	}
	if svc.revokeID != "d1" {
		t.Fatalf("expected dispositivoId passed to service, got %q", svc.revokeID)
	}

	var resp response.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("error decoding response: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
}

// TestHandleDelete_UnauthorizedWithoutClaims verifica que sin claims JWT el
// handler responda 401 sin invocar al service.
func TestHandleDelete_UnauthorizedWithoutClaims(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/dispositivos?dispositivoId=d1", nil)
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if svc.revokeCalls != 0 {
		t.Fatalf("revoke must not reach service without claims, got %d calls", svc.revokeCalls)
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
	h := NewDispositivoHandler(&fakeDispositivoService{revokeErr: dispositivo.ErrDispositivoNotFound})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/dispositivos?dispositivoId=missing", nil)
	req.AddCookie(buildSupervisorCookie(t))
	rec := httptest.NewRecorder()

	authController.JWTMiddleware(testJWTSecret, h.Handle)(rec, req)

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
	const whep = "https://camaras.example.com/whep/horno-b"
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "d-nuevo",
		Nombre:        "Raspberry Pi Horno",
		WhepURL:       whep,
		Estado:        dispositivo.EstadoOffline,
	}
	svc := &fakeDispositivoService{createResp: estado}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno","whepUrl":"` + whep + `","type":"SALIDA_HORNO"}`)

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
	body := bytes.NewBufferString(`{"nombre":"Raspberry Pi Horno","whepUrl":"no-es-url","type":"ENTRADA_HORNO"}`)

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
	body := bytes.NewBufferString(`{"dispositivoId":"11111111-1111-1111-1111-111111111111","whepUrl":"` + whep + `"}`)

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

// fakeSecureReader implementa la superficie de lectura del catálogo con estado
// de registro para ejercitar la rama "secure" del handler.
type fakeSecureReader struct {
	reads []dispositivo.DeviceRead
}

func (f fakeSecureReader) ListDeviceReads(context.Context) ([]dispositivo.DeviceRead, error) {
	return f.reads, nil
}

// TestHandleEstados_DeviceReadExponeTipo verifica que la vista enriquecida del
// catálogo (rama secure) serialice el tipo del dispositivo.
func TestHandleEstados_DeviceReadExponeTipo(t *testing.T) {
	tipo := "ENTRADA_HORNO"
	secure := fakeSecureReader{reads: []dispositivo.DeviceRead{
		{
			EstadoDispositivo: dispositivo.EstadoDispositivo{DispositivoID: "d1", Nombre: "Pi 1"},
			Tipo:              &tipo,
			AuthStatus:        dispositivo.AuthActive,
		},
	}}
	h := NewDispositivoHandler(&fakeDispositivoService{}, secure)

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
	if item["type"] != tipo {
		t.Fatalf("expected type in catalog response, got %v", item["type"])
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

// TestHandleUpdate_RejectsStrictJSONViolationsBeforeService asserts duplicate
// and case-alias members fail at the decode boundary, before the service.
func TestHandleUpdate_RejectsStrictJSONViolationsBeforeService(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate-sectorId":  `{"dispositivoId":"11111111-1111-1111-1111-111111111111","sectorId":"horno-1","sectorId":"horno-b"}`,
		"case-alias-sectorId": `{"dispositivoId":"11111111-1111-1111-1111-111111111111","SectorId":"horno-1"}`,
		"trailing-doc":        `{"dispositivoId":"11111111-1111-1111-1111-111111111111","sectorId":"horno-1"}{"x":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeDispositivoService{}
			h := NewDispositivoHandler(svc)
			req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			h.Handle(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", rec.Code)
			}
			if svc.updateCalls != 0 {
				t.Fatalf("metadata update must not reach service, got %d calls", svc.updateCalls)
			}
		})
	}
}

// TestHandleCreate_RejectsCaseAliasNombreBeforeService covers the catalog
// metadata create path with an exact-field-name boundary.
func TestHandleCreate_RejectsCaseAliasNombreBeforeService(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos", bytes.NewBufferString(`{"Nombre":"Pi 1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.Handle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if svc.createCalls != 0 {
		t.Fatalf("metadata create must not reach service, got %d calls", svc.createCalls)
	}
}

// TestHandlePing_RejectsStrictJSONViolationsBeforeService asserts duplicate
// members and case aliases fail before ProcessPing is invoked.
func TestHandlePing_RejectsStrictJSONViolationsBeforeService(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate-dispositivoId":  `{"dispositivoId":"b1c2d3e4-5678-90ab-cdef-1234567890ab","dispositivoId":"b1c2d3e4-5678-90ab-cdef-1234567890ab","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50,"aiProcessorPct":42}`,
		"case-alias-dispositivoId": `{"DispositivoId":"b1c2d3e4-5678-90ab-cdef-1234567890ab","cpuPct":10,"memRamDisponibleMb":500,"tempChip":50,"aiProcessorPct":42}`,
		"duplicate-cpuPct":         `{"dispositivoId":"b1c2d3e4-5678-90ab-cdef-1234567890ab","cpuPct":10,"cpuPct":11,"memRamDisponibleMb":500,"tempChip":50,"aiProcessorPct":42}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeDispositivoService{}
			h := NewDispositivoHandler(svc)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			req = withTestDevicePrincipal(req)
			rec := httptest.NewRecorder()

			h.HandlePing(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", rec.Code)
			}
			if svc.pingCalls != 0 {
				t.Fatalf("ping must not reach service, got %d calls", svc.pingCalls)
			}
		})
	}
}

// TestHandleRename_UnauthorizedWithoutPrincipal verifica que sin principal
// verificado el rename responde 401 con el código invalid_device_token.
func TestHandleRename_UnauthorizedWithoutPrincipal(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"nombre":"Pi Renombrada"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos/nombre", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleRename(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleRename_WrongMethod(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/nombre", nil)
	rec := httptest.NewRecorder()

	h.HandleRename(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleRename_InvalidContentType(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"nombre":"Pi Renombrada"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos/nombre", body)
	req.Header.Set("Content-Type", "text/plain")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRename(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rec.Code)
	}
}

func TestHandleRename_InvalidBody(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"nombre":"   "}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos/nombre", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRename(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	if svc.renameCalls != 0 {
		t.Fatalf("invalid request must not reach service, got %d calls", svc.renameCalls)
	}
}

func TestHandleRename_NotFound(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{renameErr: dispositivo.ErrDispositivoNotFound})
	body := bytes.NewBufferString(`{"nombre":"Pi Renombrada"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos/nombre", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRename(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestHandleRename_Success verifica el 200 con el nombre renombrado y que el
// servicio recibe el id del principal, nunca uno del body.
func TestHandleRename_Success(t *testing.T) {
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "b1c2d3e4-5678-90ab-cdef-1234567890ab",
		Nombre:        "Pi Renombrada",
		Estado:        dispositivo.EstadoOffline,
	}
	svc := &fakeDispositivoService{renameResp: estado}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"nombre":"Pi Renombrada"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos/nombre", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRename(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if svc.renameCalls != 1 {
		t.Fatalf("expected rename to reach service, got %d calls", svc.renameCalls)
	}
	if svc.renameID != "b1c2d3e4-5678-90ab-cdef-1234567890ab" {
		t.Fatalf("expected principal id, got %q", svc.renameID)
	}
	if svc.renameName != "Pi Renombrada" {
		t.Fatalf("expected nombre passed to service, got %q", svc.renameName)
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
	if data["nombre"] != "Pi Renombrada" {
		t.Fatalf("expected renamed name in response, got %v", data["nombre"])
	}
}

func TestHandleRenameURL_WrongMethod(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dispositivos/rename-url", nil)
	rec := httptest.NewRecorder()

	h.HandleRenameURL(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleRenameURL_InvalidContentType(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"rename_url":"https://mi-tunel.trycloudflare.com"}`)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dispositivos/rename-url", body)
	req.Header.Set("Content-Type", "text/plain")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRenameURL(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rec.Code)
	}
}

func TestHandleRenameURL_UnauthorizedWithoutPrincipal(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"rename_url":"https://mi-tunel.trycloudflare.com"}`)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dispositivos/rename-url", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.HandleRenameURL(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleRenameURL_InvalidJSON(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{})
	body := bytes.NewBufferString(`{"rename_url":`)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dispositivos/rename-url", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRenameURL(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleRenameURL_MissingRenameURL(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{}`)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dispositivos/rename-url", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRenameURL(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if svc.renameURLCalls != 0 {
		t.Fatalf("invalid request must not reach service, got %d calls", svc.renameURLCalls)
	}
}

func TestHandleRenameURL_EmptyRenameURL(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"rename_url":""}`)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dispositivos/rename-url", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRenameURL(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if svc.renameURLCalls != 0 {
		t.Fatalf("invalid request must not reach service, got %d calls", svc.renameURLCalls)
	}
}

func TestHandleRenameURL_InvalidRenameURLNotAbsolute(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"rename_url":"hola"}`)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dispositivos/rename-url", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRenameURL(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	if svc.renameURLCalls != 0 {
		t.Fatalf("invalid request must not reach service, got %d calls", svc.renameURLCalls)
	}
}

func TestHandleRenameURL_NotFound(t *testing.T) {
	h := NewDispositivoHandler(&fakeDispositivoService{renameURLErr: dispositivo.ErrDispositivoNotFound})
	body := bytes.NewBufferString(`{"rename_url":"https://mi-tunel.trycloudflare.com"}`)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dispositivos/rename-url", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRenameURL(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestHandleRenameURL_Success verifica el 204 sin body y que el servicio recibe
// el id del principal, nunca uno del body.
func TestHandleRenameURL_Success(t *testing.T) {
	const renameURL = "https://mi-tunel.trycloudflare.com"
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)
	body := bytes.NewBufferString(`{"rename_url":"` + renameURL + `"}`)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/dispositivos/rename-url", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestDevicePrincipal(req)
	rec := httptest.NewRecorder()

	h.HandleRenameURL(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected empty body, got %q", rec.Body.String())
	}
	if svc.renameURLCalls != 1 {
		t.Fatalf("expected rename-url to reach service, got %d calls", svc.renameURLCalls)
	}
	if svc.renameURLID != "b1c2d3e4-5678-90ab-cdef-1234567890ab" {
		t.Fatalf("expected principal id, got %q", svc.renameURLID)
	}
	if svc.renameURLValue != renameURL {
		t.Fatalf("expected rename_url passed to service, got %q", svc.renameURLValue)
	}
}

// ---------------------------------------------------------------------------
// HandleRenameByID — push-first (errores del servicio)
// ---------------------------------------------------------------------------

func TestHandleRenameByID_DelegaARenameFromPanel(t *testing.T) {
	estado := &dispositivo.EstadoDispositivo{
		DispositivoID: "11111111-1111-1111-1111-111111111111",
		Nombre:        "Pi Nueva",
		Estado:        dispositivo.EstadoOnline,
	}
	svc := &fakeDispositivoService{renameFromPanelResp: estado}
	h := NewDispositivoHandler(svc)

	body := bytes.NewBufferString(`{"nombre":"Pi Nueva"}`)
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/dispositivos/11111111-1111-1111-1111-111111111111/nombre", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestSupervisorJWT(t, req)
	rec := httptest.NewRecorder()

	h.HandleRenameByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.renameFromPanelCalls != 1 {
		t.Fatalf("expected RenameFromPanel to be invoked, got %d calls", svc.renameFromPanelCalls)
	}
	if svc.renameFromPanelID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("expected device id from path, got %q", svc.renameFromPanelID)
	}
	if svc.renameFromPanelName != "Pi Nueva" {
		t.Fatalf("expected nombre passed to service, got %q", svc.renameFromPanelName)
	}
	// El rename Pi→Go NO debe invocarse desde el flujo panel.
	if svc.renameCalls != 0 {
		t.Fatalf("Rename must NOT be called from panel flow, got %d", svc.renameCalls)
	}
}

func TestHandleRenameByID_NotFound(t *testing.T) {
	svc := &fakeDispositivoService{renameFromPanelErr: dispositivo.ErrDispositivoNotFound}
	h := NewDispositivoHandler(svc)

	body := bytes.NewBufferString(`{"nombre":"Pi Nueva"}`)
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/dispositivos/11111111-1111-1111-1111-111111111111/nombre", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestSupervisorJWT(t, req)
	rec := httptest.NewRecorder()

	h.HandleRenameByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleRenameByID_LegacySinPush_Rechaza422(t *testing.T) {
	svc := &fakeDispositivoService{renameFromPanelErr: dispositivo.ErrRenameNotPushable}
	h := NewDispositivoHandler(svc)

	body := bytes.NewBufferString(`{"nombre":"Pi Nueva"}`)
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/dispositivos/11111111-1111-1111-1111-111111111111/nombre", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestSupervisorJWT(t, req)
	rec := httptest.NewRecorder()

	h.HandleRenameByID(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d body=%s", rec.Code, rec.Body.String())
	}
	// El operador debe saber que NO se aplicaron cambios para no quedar con
	// el nombre desactualizado en la Pi.
	if !strings.Contains(rec.Body.String(), "push de renombre") {
		t.Fatalf("expected error message to mention push de renombre, got %s", rec.Body.String())
	}
}

func TestHandleRenameByID_FallaPushDevuelve502(t *testing.T) {
	svc := &fakeDispositivoService{renameFromPanelErr: errors.New("no se pudo contactar al dispositivo para el renombre: connection refused")}
	h := NewDispositivoHandler(svc)

	body := bytes.NewBufferString(`{"nombre":"Pi Nueva"}`)
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/dispositivos/11111111-1111-1111-1111-111111111111/nombre", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestSupervisorJWT(t, req)
	rec := httptest.NewRecorder()

	h.HandleRenameByID(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on push failure, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no se aplicaron cambios") {
		t.Fatalf("expected message to clarify no changes were applied, got %s", rec.Body.String())
	}
}

func TestHandleRenameByID_PathSinID(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)

	body := bytes.NewBufferString(`{"nombre":"Pi Nueva"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos//nombre", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestSupervisorJWT(t, req)
	rec := httptest.NewRecorder()

	h.HandleRenameByID(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if svc.renameFromPanelCalls != 0 {
		t.Fatalf("service must not be called when path id is missing, got %d", svc.renameFromPanelCalls)
	}
}

func TestHandleRenameByID_PathIDInvalido(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)

	body := bytes.NewBufferString(`{"nombre":"Pi Nueva"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos/no-es-uuid/nombre", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestSupervisorJWT(t, req)
	rec := httptest.NewRecorder()

	h.HandleRenameByID(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if svc.renameFromPanelCalls != 0 {
		t.Fatalf("service must not be called with malformed uuid, got %d", svc.renameFromPanelCalls)
	}
}

func TestHandleRenameByID_NombreInvalido(t *testing.T) {
	svc := &fakeDispositivoService{}
	h := NewDispositivoHandler(svc)

	body := bytes.NewBufferString(`{"nombre":"   "}`)
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/dispositivos/11111111-1111-1111-1111-111111111111/nombre", body)
	req.Header.Set("Content-Type", "application/json")
	req = withTestSupervisorJWT(t, req)
	rec := httptest.NewRecorder()

	h.HandleRenameByID(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	if svc.renameFromPanelCalls != 0 {
		t.Fatalf("invalid nombre must not reach service, got %d calls", svc.renameFromPanelCalls)
	}
}
