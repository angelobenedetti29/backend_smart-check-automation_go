package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
)

// mockConsignaService implements consigna.Service (domain interface) for controlled testing.
type mockConsignaService struct {
	dispatchManualFunc func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error)
	getHistorialFunc   func(ctx context.Context, loteID string) ([]consigna.Consigna, error)
}

func (m *mockConsignaService) DispatchAutomatico(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error) {
	return nil, nil
}

func (m *mockConsignaService) DispatchManual(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
	return m.dispatchManualFunc(ctx, req)
}

func (m *mockConsignaService) GetHistorialByLote(ctx context.Context, loteID string) ([]consigna.Consigna, error) {
	return m.getHistorialFunc(ctx, loteID)
}

func validManualJSON() []byte {
	return []byte(`{
		"hornoId": "horno-01",
		"productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		"temperaturaObjetivo": 170.0,
		"velocidadCintaObjetivo": 0.20,
		"usuario": "operario-demo"
	}`)
}

func executeDispatchRequest(t *testing.T, handler *ConsignaHandler, method string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/horno/consigna", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.DispatchManual(rr, req)
	return rr
}

func TestDispatchManual_Success(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			assert.Equal(t, "horno-01", req.HornoID)
			assert.Equal(t, 170.0, req.TemperaturaObjetivo)
			return &consigna.Consigna{HornoID: req.HornoID, Origen: consigna.OrigenManual, Exitosa: true}, nil
		},
	}
	handler := NewConsignaHandler(svc)
	rr := executeDispatchRequest(t, handler, http.MethodPost, validManualJSON())

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.True(t, resp["success"].(bool))
}

func TestDispatchManual_FueraDeRango(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			return nil, consigna.ErrFueraDeRango
		},
	}
	handler := NewConsignaHandler(svc)
	rr := executeDispatchRequest(t, handler, http.MethodPost, validManualJSON())

	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.False(t, resp["success"].(bool))
	assert.Contains(t, resp["message"], "rango seguro")
}

func TestDispatchManual_HornoNoExiste(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			return nil, consigna.ErrHornoNoExiste
		},
	}
	handler := NewConsignaHandler(svc)
	rr := executeDispatchRequest(t, handler, http.MethodPost, validManualJSON())

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestDispatchManual_ParametrosNoExiste(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			return nil, consigna.ErrParametrosNoExiste
		},
	}
	handler := NewConsignaHandler(svc)
	rr := executeDispatchRequest(t, handler, http.MethodPost, validManualJSON())

	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)
}

func TestDispatchManual_DispatchFallido(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			motivo := "timeout del controlador físico (simulado)"
			return &consigna.Consigna{HornoID: req.HornoID, Origen: consigna.OrigenManual, Exitosa: false, MotivoError: &motivo}, consigna.ErrDispatchFallido
		},
	}
	handler := NewConsignaHandler(svc)
	rr := executeDispatchRequest(t, handler, http.MethodPost, validManualJSON())

	assert.Equal(t, http.StatusBadGateway, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.False(t, resp["success"].(bool))
}

func TestDispatchManual_ValidationError(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			t.Error("DispatchManual should not be called on validation failure")
			return nil, nil
		},
	}
	handler := NewConsignaHandler(svc)

	invalidBody := []byte(`{"hornoId":"","productoId":"","temperaturaObjetivo":0,"velocidadCintaObjetivo":0}`)
	rr := executeDispatchRequest(t, handler, http.MethodPost, invalidBody)

	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)
}

func TestDispatchManual_MalformedJSON(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			t.Error("DispatchManual should not be called with malformed JSON")
			return nil, nil
		},
	}
	handler := NewConsignaHandler(svc)
	rr := executeDispatchRequest(t, handler, http.MethodPost, []byte(`{corrupto`))

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestDispatchManual_WrongMethod(t *testing.T) {
	handler := NewConsignaHandler(&mockConsignaService{})
	rr := executeDispatchRequest(t, handler, http.MethodGet, nil)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestGetHistorial_Success(t *testing.T) {
	svc := &mockConsignaService{
		getHistorialFunc: func(ctx context.Context, loteID string) ([]consigna.Consigna, error) {
			assert.Equal(t, "lote-1", loteID)
			return []consigna.Consigna{{HornoID: "horno-01", LoteID: &loteID, Origen: consigna.OrigenManual}}, nil
		},
	}
	handler := NewConsignaHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/horno/consigna/historial?loteId=lote-1", nil)
	rr := httptest.NewRecorder()
	handler.GetHistorial(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestGetHistorial_MissingLoteID(t *testing.T) {
	handler := NewConsignaHandler(&mockConsignaService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/horno/consigna/historial", nil)
	rr := httptest.NewRecorder()
	handler.GetHistorial(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGetHistorial_WrongMethod(t *testing.T) {
	handler := NewConsignaHandler(&mockConsignaService{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/horno/consigna/historial?loteId=lote-1", nil)
	rr := httptest.NewRecorder()
	handler.GetHistorial(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}
