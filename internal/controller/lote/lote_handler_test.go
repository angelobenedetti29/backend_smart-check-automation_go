package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote"
	loteProductivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_productivo"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// mockConsignaService implements consigna.Service (domain interface) for controlled testing.
type mockConsignaService struct {
	dispatchAutomaticoFunc func(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error)
}

func (m *mockConsignaService) DispatchAutomatico(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error) {
	return m.dispatchAutomaticoFunc(ctx, hornoID, loteID, productoID)
}

func (m *mockConsignaService) DispatchManual(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
	return nil, nil
}

func (m *mockConsignaService) GetHistorialByLote(ctx context.Context, loteID string) ([]consigna.Consigna, error) {
	return nil, nil
}

// mockLoteRepo implements lote.Repository (domain interface) for controlled testing.
type mockLoteRepo struct {
	createFunc func(ctx context.Context, l *lote.Lote) error
}

func (m *mockLoteRepo) Create(ctx context.Context, l *lote.Lote) error {
	return m.createFunc(ctx, l)
}

func validLoteJSON() []byte {
	return []byte(`{
		"id": "550e8400-e29b-41d4-a716-446655440000",
		"productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		"productoNombre": "Tostada Integral",
		"turno": "mañana",
		"inicioAt": "2026-06-02T06:00:00Z",
		"finAt": "2026-06-02T08:30:00Z",
		"totalUnidades": 1200,
		"correctos": 1150,
		"quemados": 50,
		"crudas": null,
		"correctosKg": 138.00,
		"quemadosKg": 6.00,
		"crudosKg": null,
		"tempHorno1": 210.50,
		"tempCombHorno1": null,
		"tempHorno2": 215.00,
		"tempCombHorno2": null,
		"velocidadCinta": 3.20,
		"createdAt": "2026-06-02T06:00:01Z",
		"updatedAt": "2026-06-02T08:30:05Z"
	}`)
}

// mockLoteProductivoFetcher implements LoteFetcher for controlled testing.
type mockLoteProductivoFetcher struct{}

func (m *mockLoteProductivoFetcher) GetByID(id string) (*loteProductivo.LoteProductivo, error) {
	return &loteProductivo.LoteProductivo{
		ID: id,
	}, nil
}

func setupHandlerTest(t *testing.T, repo lote.Repository) *LoteHandler {
	t.Helper()
	consignaSvc := &mockConsignaService{
		dispatchAutomaticoFunc: func(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error) {
			return &consigna.Consigna{HornoID: hornoID, Origen: consigna.OrigenAutomatico, Exitosa: true}, nil
		},
	}
	return NewLoteHandler(repo, sse.NewBroker(), &mockLoteProductivoFetcher{}, consignaSvc)
}

func executeRequest(t *testing.T, handler *LoteHandler, method string, body []byte, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/lotes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req = req.WithContext(deviceauth.WithPrincipal(req.Context(), deviceauth.Principal{DeviceID: "00000000-0000-0000-0000-000000000001", Fingerprint: "test-fingerprint"}))
	}
	rr := httptest.NewRecorder()
	handler.HandleCreateLote(rr, req)
	return rr
}

func TestHandleCreateLote_Success(t *testing.T) {
	mock := &mockLoteRepo{
		createFunc: func(ctx context.Context, l *lote.Lote) error {
			assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", l.ID)
			assert.Equal(t, 1200, l.TotalUnidades)
			assert.Equal(t, 1150, l.Correctos)
			return nil
		},
	}
	handler := setupHandlerTest(t, mock)
	rr := executeRequest(t, handler, http.MethodPost, validLoteJSON(), "test-key-fermar")

	assert.Equal(t, http.StatusCreated, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.True(t, resp["success"].(bool))
	assert.Equal(t, "Lote creado exitosamente", resp["message"])
}

func TestHandleCreateLote_ValidationError(t *testing.T) {
	mock := &mockLoteRepo{
		createFunc: func(ctx context.Context, l *lote.Lote) error {
			t.Error("Create should not be called on validation failure")
			return nil
		},
	}
	handler := setupHandlerTest(t, mock)

	// Suma excede el total: correctos(1150) + quemados(50) = 1200 > totalUnidades(100)
	invalidBody := bytes.ReplaceAll(validLoteJSON(), []byte(`"totalUnidades": 1200`), []byte(`"totalUnidades": 100`))
	rr := executeRequest(t, handler, http.MethodPost, invalidBody, "test-key-fermar")

	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.False(t, resp["success"].(bool))
	assert.Contains(t, resp["message"], "inválidos")
}

func TestHandleCreateLote_Unauthorized(t *testing.T) {
	mock := &mockLoteRepo{
		createFunc: func(ctx context.Context, l *lote.Lote) error {
			t.Error("Create should not be called without API key")
			return nil
		},
	}
	handler := setupHandlerTest(t, mock)

	// Sin API key
	rr := executeRequest(t, handler, http.MethodPost, validLoteJSON(), "")

	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.False(t, resp["success"].(bool))
}

func TestHandleCreateLote_MalformedJSON(t *testing.T) {
	mock := &mockLoteRepo{
		createFunc: func(ctx context.Context, l *lote.Lote) error {
			t.Error("Create should not be called with malformed JSON")
			return nil
		},
	}
	handler := setupHandlerTest(t, mock)

	rr := executeRequest(t, handler, http.MethodPost, []byte(`{corrupto`), "test-key-fermar")

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.False(t, resp["success"].(bool))
}

func TestHandleCreateLote_WrongMethod(t *testing.T) {
	mock := &mockLoteRepo{}
	handler := setupHandlerTest(t, mock)

	rr := executeRequest(t, handler, http.MethodGet, nil, "test-key-fermar")

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleCreateLote_RepoError(t *testing.T) {
	mock := &mockLoteRepo{
		createFunc: func(ctx context.Context, l *lote.Lote) error {
			return assert.AnError
		},
	}
	handler := setupHandlerTest(t, mock)
	rr := executeRequest(t, handler, http.MethodPost, validLoteJSON(), "test-key-fermar")

	assert.Equal(t, http.StatusInternalServerError, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.False(t, resp["success"].(bool))
}

func executeIniciarLoteRequest(t *testing.T, handler *LoteHandler, method string, body []byte, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/lotes/inicio", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req = req.WithContext(deviceauth.WithPrincipal(req.Context(), deviceauth.Principal{DeviceID: "00000000-0000-0000-0000-000000000001", Fingerprint: "test-fingerprint"}))
	}
	rr := httptest.NewRecorder()
	handler.HandleIniciarLote(rr, req)
	return rr
}

func TestHandleIniciarLote_Success(t *testing.T) {
	consignaSvc := &mockConsignaService{
		dispatchAutomaticoFunc: func(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error) {
			assert.Equal(t, "horno-01", hornoID)
			assert.Equal(t, "a1b2c3d4-5678-90ab-cdef-1234567890ab", productoID)
			assert.NotEmpty(t, loteID)
			return &consigna.Consigna{HornoID: hornoID, Origen: consigna.OrigenAutomatico, Exitosa: true}, nil
		},
	}
	handler := NewLoteHandler(&mockLoteRepo{}, sse.NewBroker(), &mockLoteProductivoFetcher{}, consignaSvc)

	body := []byte(`{"hornoId":"horno-01","productoId":"a1b2c3d4-5678-90ab-cdef-1234567890ab"}`)
	rr := executeIniciarLoteRequest(t, handler, http.MethodPost, body, "test-key-fermar")

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.True(t, resp["success"].(bool))
}

func TestHandleIniciarLote_ParametrosNoExiste(t *testing.T) {
	consignaSvc := &mockConsignaService{
		dispatchAutomaticoFunc: func(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error) {
			return nil, consigna.ErrParametrosNoExiste
		},
	}
	handler := NewLoteHandler(&mockLoteRepo{}, sse.NewBroker(), &mockLoteProductivoFetcher{}, consignaSvc)

	body := []byte(`{"hornoId":"horno-01","productoId":"producto-sin-setpoint"}`)
	rr := executeIniciarLoteRequest(t, handler, http.MethodPost, body, "test-key-fermar")

	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)
}

func TestHandleIniciarLote_HornoNoExiste(t *testing.T) {
	consignaSvc := &mockConsignaService{
		dispatchAutomaticoFunc: func(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error) {
			return nil, consigna.ErrHornoNoExiste
		},
	}
	handler := NewLoteHandler(&mockLoteRepo{}, sse.NewBroker(), &mockLoteProductivoFetcher{}, consignaSvc)

	body := []byte(`{"hornoId":"horno-inexistente","productoId":"a1b2c3d4-5678-90ab-cdef-1234567890ab"}`)
	rr := executeIniciarLoteRequest(t, handler, http.MethodPost, body, "test-key-fermar")

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandleIniciarLote_MissingFields(t *testing.T) {
	consignaSvc := &mockConsignaService{
		dispatchAutomaticoFunc: func(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error) {
			t.Error("DispatchAutomatico should not be called with missing fields")
			return nil, nil
		},
	}
	handler := NewLoteHandler(&mockLoteRepo{}, sse.NewBroker(), &mockLoteProductivoFetcher{}, consignaSvc)

	body := []byte(`{"hornoId":""}`)
	rr := executeIniciarLoteRequest(t, handler, http.MethodPost, body, "test-key-fermar")

	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)
}

func TestHandleIniciarLote_Unauthorized(t *testing.T) {
	consignaSvc := &mockConsignaService{
		dispatchAutomaticoFunc: func(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error) {
			t.Error("DispatchAutomatico should not be called without device principal")
			return nil, nil
		},
	}
	handler := NewLoteHandler(&mockLoteRepo{}, sse.NewBroker(), &mockLoteProductivoFetcher{}, consignaSvc)

	body := []byte(`{"hornoId":"horno-01","productoId":"a1b2c3d4-5678-90ab-cdef-1234567890ab"}`)
	rr := executeIniciarLoteRequest(t, handler, http.MethodPost, body, "")

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleIniciarLote_WrongMethod(t *testing.T) {
	handler := NewLoteHandler(&mockLoteRepo{}, sse.NewBroker(), &mockLoteProductivoFetcher{}, &mockConsignaService{})

	rr := executeIniciarLoteRequest(t, handler, http.MethodGet, nil, "test-key-fermar")

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

// TestHandleCreateLote_RejectsDuplicateMembersBeforeRepo asserts the strict
// boundary stops duplicate JSON members before any repository call.
func TestHandleCreateLote_RejectsDuplicateMembersBeforeRepo(t *testing.T) {
	mock := &mockLoteRepo{
		createFunc: func(ctx context.Context, l *lote.Lote) error {
			t.Error("Create should not be called on strict decode failure")
			return nil
		},
	}
	handler := setupHandlerTest(t, mock)

	body := bytes.ReplaceAll(validLoteJSON(), []byte(`"productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab"`), []byte(`"productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab","productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab"`))
	rr := executeRequest(t, handler, http.MethodPost, body, "test-key-fermar")

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestHandleIniciarLote_RejectsStrictJSONViolationsBeforeService asserts
// duplicate members and case aliases fail before DispatchAutomatico runs.
func TestHandleIniciarLote_RejectsStrictJSONViolationsBeforeService(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate-hornoId":  `{"hornoId":"horno-01","hornoId":"horno-02","productoId":"a1b2c3d4-5678-90ab-cdef-1234567890ab"}`,
		"case-alias-hornoId": `{"HornoId":"horno-01","productoId":"a1b2c3d4-5678-90ab-cdef-1234567890ab"}`,
		"trailing-doc":       `{"hornoId":"horno-01","productoId":"a1b2c3d4-5678-90ab-cdef-1234567890ab"}{"x":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			consignaSvc := &mockConsignaService{
				dispatchAutomaticoFunc: func(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error) {
					t.Error("DispatchAutomatico should not be called on strict decode failure")
					return nil, nil
				},
			}
			handler := NewLoteHandler(&mockLoteRepo{}, sse.NewBroker(), &mockLoteProductivoFetcher{}, consignaSvc)
			rr := executeIniciarLoteRequest(t, handler, http.MethodPost, []byte(body), "test-key-fermar")
			assert.Equal(t, http.StatusBadRequest, rr.Code)
		})
	}
}
