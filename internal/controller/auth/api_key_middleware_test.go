package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

const testAPIKey = "test-api-key-super-secret"

func TestAPIKeyMiddleware_HeaderAusente_401(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lotes", nil)
	rr := httptest.NewRecorder()

	APIKeyMiddleware(testAPIKey, dummyHandler)(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAPIKeyMiddleware_HeaderIncorrecto_401(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lotes", nil)
	req.Header.Set("X-API-Key", "clave-incorrecta")
	rr := httptest.NewRecorder()

	APIKeyMiddleware(testAPIKey, dummyHandler)(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAPIKeyMiddleware_HeaderValido_200(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lotes", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()

	APIKeyMiddleware(testAPIKey, dummyHandler)(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}
