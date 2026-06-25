package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote"
)

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

func setupHandlerTest(t *testing.T, repo lote.Repository) *LoteHandler {
	t.Helper()
	t.Setenv("API_KEY_SECRET", "test-key-fermar")
	return NewLoteHandler(repo)
}

func executeRequest(t *testing.T, handler *LoteHandler, method string, body []byte, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/lotes", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
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
