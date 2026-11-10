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

func (m *mockConsignaService) DispatchManual(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
	return m.dispatchManualFunc(ctx, req)
}

func (m *mockConsignaService) GetHistorialByLote(ctx context.Context, loteID string) ([]consigna.Consigna, error) {
	return m.getHistorialFunc(ctx, loteID)
}

func validManualJSON() []byte {
	return []byte(`{
		"loteId": "lote-1",
		"productoId": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		"temperaturaObjetivo": 170.0,
		"velocidadCintaObjetivo": 0.20,
		"usuario": "operario-demo"
	}`)
}

func validSectorJSON() []byte {
	return []byte(`{
		"loteId": "lote-1",
		"sectorId": "sector-x",
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
			assert.Equal(t, "lote-1", *req.LoteID)
			assert.Equal(t, 170.0, req.TemperaturaObjetivo)
			return &consigna.Consigna{LoteID: req.LoteID, HornoID: "sector-1", Origen: consigna.OrigenManual, Exitosa: true}, nil
		},
	}
	handler := NewConsignaHandler(svc)
	rr := executeDispatchRequest(t, handler, http.MethodPost, validManualJSON())

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.True(t, resp["success"].(bool))
	assert.Equal(t, "Consigna encolada para la Pi", resp["message"])
}

func TestDispatchManual_SectorID(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			assert.Equal(t, "sector-x", req.OvenID())
			return &consigna.Consigna{HornoID: req.OvenID(), Origen: consigna.OrigenManual, Exitosa: true}, nil
		},
	}
	handler := NewConsignaHandler(svc)
	rr := executeDispatchRequest(t, handler, http.MethodPost, validSectorJSON())

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, "sector-x", data["hornoId"])
}

func TestDispatchManual_SinLoteID(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			t.Error("DispatchManual should not be called without loteId")
			return nil, nil
		},
	}
	handler := NewConsignaHandler(svc)
	body := []byte(`{"productoId":"a1b2c3d4-5678-90ab-cdef-1234567890ab","temperaturaObjetivo":170,"velocidadCintaObjetivo":0.2}`)
	rr := executeDispatchRequest(t, handler, http.MethodPost, body)

	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)
}

func dispatchErrorCase(t *testing.T, err error) *httptest.ResponseRecorder {
	t.Helper()
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			return nil, err
		},
	}
	return executeDispatchRequest(t, NewConsignaHandler(svc), http.MethodPost, validManualJSON())
}

func TestDispatchManual_ErroresDeLote(t *testing.T) {
	casos := []struct {
		nombre  string
		err     error
		status  int
		mensaje string
	}{
		{"lote requerido", consigna.ErrLoteRequerido, http.StatusUnprocessableEntity, "El lote es requerido para enviar una consigna manual"},
		{"lote inexistente", consigna.ErrLoteNoExiste, http.StatusNotFound, "El lote no existe"},
		{"lote no abierto", consigna.ErrLoteNoAbierto, http.StatusConflict, "El lote no está abierto"},
		{"lote no manual", consigna.ErrLoteNoManual, http.StatusConflict, "El lote no está en modo MANUAL"},
		{"sector mismatch", consigna.ErrSectorMismatch, http.StatusUnprocessableEntity, "El sector no coincide con el del lote"},
		{"sector requerido", consigna.ErrSectorRequerido, http.StatusUnprocessableEntity, "El lote no tiene sector asignado"},
		{"parámetros", consigna.ErrParametrosNoExiste, http.StatusUnprocessableEntity, "El producto no tiene parámetros de control cargados"},
		{"fuera de rango", consigna.ErrFueraDeRango, http.StatusUnprocessableEntity, "Los valores solicitados están fuera del rango seguro del producto"},
	}
	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			rr := dispatchErrorCase(t, tc.err)
			assert.Equal(t, tc.status, rr.Code)

			var resp map[string]interface{}
			assert.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
			assert.False(t, resp["success"].(bool))
			assert.Contains(t, resp["message"], tc.mensaje)
		})
	}
}

func TestDispatchManual_EncoladoFallido(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			motivo := "db down"
			return &consigna.Consigna{LoteID: req.LoteID, Origen: consigna.OrigenManual, Exitosa: false, MotivoError: &motivo}, consigna.ErrEncoladoFallido
		},
	}
	handler := NewConsignaHandler(svc)
	rr := executeDispatchRequest(t, handler, http.MethodPost, validManualJSON())

	assert.Equal(t, http.StatusBadGateway, rr.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.False(t, resp["success"].(bool))
	assert.Contains(t, resp["message"], "No se pudo encolar la consigna para la Pi")
}

func TestDispatchManual_ValidationError(t *testing.T) {
	svc := &mockConsignaService{
		dispatchManualFunc: func(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
			t.Error("DispatchManual should not be called on validation failure")
			return nil, nil
		},
	}
	handler := NewConsignaHandler(svc)

	invalidBody := []byte(`{"productoId":"","temperaturaObjetivo":0,"velocidadCintaObjetivo":0}`)
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
			return []consigna.Consigna{{HornoID: "sector-1", LoteID: &loteID, Origen: consigna.OrigenManual}}, nil
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
