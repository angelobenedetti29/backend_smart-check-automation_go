package dualauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
)

// stubStore resuelve un device id fijo para cualquier hash de secret.
type stubStore struct {
	id  string
	err error
}

func (s stubStore) LookupActiveBySecretHash(context.Context, string) (string, error) {
	return s.id, s.err
}

// spyHandler registra si fue invocado y qué identidad quedó en el contexto.
type spyHandler struct {
	called    bool
	principal deviceauth.Principal
	hasPrin   bool
	claims    *authService.Claims
}

func (h *spyHandler) handle(w http.ResponseWriter, r *http.Request) {
	h.called = true
	h.principal, h.hasPrin = deviceauth.PrincipalFromContext(r.Context())
	h.claims = authController.GetClaimsFromContext(r.Context())
	w.WriteHeader(http.StatusOK)
}

// signSessionToken construye un JWT HS256 real equivalente al emitido por el
// login humano, firmado con el secret de prueba.
func signSessionToken(t *testing.T, secret []byte) string {
	t.Helper()
	claims := authService.Claims{
		Email: "operario@fermar.com.ar",
		Name:  "Operario",
		Role:  "Operario",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	require.NoError(t, err)
	return token
}

func newRequest(method, target string) *http.Request {
	return httptest.NewRequest(method, target, nil)
}

func TestDualAuthValidDeviceBearerInvokesNextWithPrincipal(t *testing.T) {
	secret := []byte("test-secret")
	spy := &spyHandler{}
	r := newRequest(http.MethodGet, "/api/v1/lotes")
	r.Header.Set("Authorization", "Bearer s3cr3t")
	rec := httptest.NewRecorder()

	DualAuth(&deviceauth.Verifier{Store: stubStore{id: "dev-42"}}, secret, spy.handle)(rec, r)

	require.True(t, spy.called)
	assert.Equal(t, http.StatusOK, rec.Code)
	require.True(t, spy.hasPrin)
	assert.Equal(t, "dev-42", spy.principal.DeviceID)
	assert.Nil(t, spy.claims)
}

func TestDualAuthInvalidDeviceBearerResponds401FlatBody(t *testing.T) {
	secret := []byte("test-secret")
	cases := map[string]string{
		"unknown-device":  "Bearer unknown",
		"malformed":       "Basic abc",
		"missing-token":   "Bearer",
		"multiple-values": "Bearer a",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			spy := &spyHandler{}
			r := newRequest(http.MethodGet, "/api/v1/lotes")
			r.Header.Set("Authorization", header)
			if name == "multiple-values" {
				r.Header.Add("Authorization", "Bearer b")
			}
			rec := httptest.NewRecorder()

			DualAuth(&deviceauth.Verifier{Store: stubStore{}}, secret, spy.handle)(rec, r)

			assert.False(t, spy.called)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
			assert.Equal(t, invalidDeviceTokenBody, rec.Body.String())
		})
	}
}

func TestDualAuthNilVerifierWithAuthorizationRejects(t *testing.T) {
	spy := &spyHandler{}
	r := newRequest(http.MethodGet, "/api/v1/lotes")
	r.Header.Set("Authorization", "Bearer s3cr3t")
	rec := httptest.NewRecorder()

	DualAuth(nil, []byte("test-secret"), spy.handle)(rec, r)

	assert.False(t, spy.called)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, invalidDeviceTokenBody, rec.Body.String())
}

func TestDualAuthValidOAuthCookieInvokesNextWithClaims(t *testing.T) {
	secret := []byte("test-secret")
	spy := &spyHandler{}
	r := newRequest(http.MethodGet, "/api/v1/lotes")
	r.AddCookie(&http.Cookie{Name: "session_token", Value: signSessionToken(t, secret)})
	rec := httptest.NewRecorder()

	DualAuth(&deviceauth.Verifier{Store: stubStore{id: "dev-42"}}, secret, spy.handle)(rec, r)

	require.True(t, spy.called)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, spy.hasPrin)
	require.NotNil(t, spy.claims)
	assert.Equal(t, "operario@fermar.com.ar", spy.claims.Email)
	assert.Equal(t, "Operario", spy.claims.Role)
}

func TestDualAuthWithoutCredentialsResponds401Envelope(t *testing.T) {
	spy := &spyHandler{}
	r := newRequest(http.MethodGet, "/api/v1/lotes")
	rec := httptest.NewRecorder()

	DualAuth(&deviceauth.Verifier{Store: stubStore{id: "dev-42"}}, []byte("test-secret"), spy.handle)(rec, r)

	assert.False(t, spy.called)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestDualAuthInvalidBearerDoesNotFallBackToValidCookie verifica que un header
// Authorization presente pero inválido no degrade la credencial hacia la cookie.
func TestDualAuthInvalidBearerDoesNotFallBackToValidCookie(t *testing.T) {
	secret := []byte("test-secret")
	spy := &spyHandler{}
	r := newRequest(http.MethodGet, "/api/v1/lotes")
	r.Header.Set("Authorization", "Bearer invalid")
	r.AddCookie(&http.Cookie{Name: "session_token", Value: signSessionToken(t, secret)})
	rec := httptest.NewRecorder()

	DualAuth(&deviceauth.Verifier{Store: stubStore{}}, secret, spy.handle)(rec, r)

	assert.False(t, spy.called)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, invalidDeviceTokenBody, rec.Body.String())
}

func TestDualAuthInvalidCookieResponds401(t *testing.T) {
	spy := &spyHandler{}
	r := newRequest(http.MethodGet, "/api/v1/lotes")
	r.AddCookie(&http.Cookie{Name: "session_token", Value: "not-a-jwt"})
	rec := httptest.NewRecorder()

	DualAuth(&deviceauth.Verifier{Store: stubStore{id: "dev-42"}}, []byte("test-secret"), spy.handle)(rec, r)

	assert.False(t, spy.called)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
