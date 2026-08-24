package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"

	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
)

var testSecret = []byte("test-secret-32-chars-exactly!!!")

// buildValidCookie genera una cookie con un JWT válido firmado con testSecret.
func buildValidCookie(t *testing.T, role string) *http.Cookie {
	t.Helper()
	claims := &authService.Claims{
		Email: "user@fermar.com.ar",
		Name:  "Test User",
		Role:  role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "smart-check-automation",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(testSecret)
	if err != nil {
		t.Fatalf("error generando token de prueba: %v", err)
	}
	return &http.Cookie{Name: "session_token", Value: signed}
}

// buildExpiredCookie genera una cookie con un JWT ya expirado.
func buildExpiredCookie(t *testing.T) *http.Cookie {
	t.Helper()
	claims := &authService.Claims{
		Email: "user@fermar.com.ar",
		Name:  "Test User",
		Role:  "Operario",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)), // ya expiró
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString(testSecret)
	return &http.Cookie{Name: "session_token", Value: signed}
}

// dummyHandler es un handler que responde 200 si llega a ejecutarse.
func dummyHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func TestJWTMiddleware_SinCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes-productivos", nil)
	rr := httptest.NewRecorder()

	JWTMiddleware(testSecret, dummyHandler)(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestJWTMiddleware_TokenExpirado(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes-productivos", nil)
	req.AddCookie(buildExpiredCookie(t))
	rr := httptest.NewRecorder()

	JWTMiddleware(testSecret, dummyHandler)(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestJWTMiddleware_TokenAdulterado(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes-productivos", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: "token.adulterado.aqui"})
	rr := httptest.NewRecorder()

	JWTMiddleware(testSecret, dummyHandler)(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestJWTMiddleware_AlgoritmoNone_Rechazado(t *testing.T) {
	// Ataque "alg=none": el middleware debe rechazarlo aunque la firma esté "ausente"
	// jwt.UnsafeAllowNoneSignatureType es la única forma de crear estos tokens en golang-jwt
	claims := &authService.Claims{
		Email: "attacker@evil.com",
		Name:  "Attacker",
		Role:  "Administrador",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, _ := token.SignedString(jwt.UnsafeAllowNoneSignatureType)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes-productivos", nil)
	req.AddCookie(&http.Cookie{Name: "session_token", Value: signed})
	rr := httptest.NewRecorder()

	JWTMiddleware(testSecret, dummyHandler)(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code, "alg=none debe ser rechazado")
}

func TestJWTMiddleware_TokenValido_PasaAlHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes-productivos", nil)
	req.AddCookie(buildValidCookie(t, "Administrador"))
	rr := httptest.NewRecorder()

	JWTMiddleware(testSecret, dummyHandler)(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestJWTMiddleware_CualquierRolAutenticado_PasaAlHandler(t *testing.T) {
	for _, role := range []string{"Operario", "Supervisor", "Administrador"} {
		t.Run(role, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/dispositivos", nil)
			req.AddCookie(buildValidCookie(t, role))
			rr := httptest.NewRecorder()

			JWTMiddleware(testSecret, dummyHandler)(rr, req)

			assert.Equal(t, http.StatusOK, rr.Code)
		})
	}
}

func TestJWTMiddleware_ClaimsEnContexto(t *testing.T) {
	// Verificar que los claims quedan disponibles en el contexto para handlers downstream
	var capturedClaims *authService.Claims

	captureHandler := func(w http.ResponseWriter, r *http.Request) {
		capturedClaims = GetClaimsFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(buildValidCookie(t, "Supervisor"))
	rr := httptest.NewRecorder()

	JWTMiddleware(testSecret, captureHandler)(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.NotNil(t, capturedClaims)
	assert.Equal(t, "user@fermar.com.ar", capturedClaims.Email)
	assert.Equal(t, "Supervisor", capturedClaims.Role)
}

func TestRequireRole_SinClaims_401(t *testing.T) {
	// Request sin JWTMiddleware previo (sin claims en el contexto)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/horno/temperatura", nil)
	rr := httptest.NewRecorder()

	allowedRoles := []string{"Supervisor", "Administrador"}
	RequireRole(allowedRoles, dummyHandler)(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireRole_OperarioAccediendoARutaProtegida_403Forbidden(t *testing.T) {
	// Operario autenticado con JWT válido intentando modificar parámetros del horno
	req := httptest.NewRequest(http.MethodPost, "/api/v1/horno/temperatura", nil)
	req.AddCookie(buildValidCookie(t, "Operario"))
	rr := httptest.NewRecorder()

	allowedRoles := []string{"Supervisor", "Administrador"}

	// Combinación de JWTMiddleware + RequireRole
	handlerChain := JWTMiddleware(testSecret, RequireRole(allowedRoles, dummyHandler))
	handlerChain(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code, "Operario debe recibir 403 Forbidden en rutas de configuración")
}

func TestRequireRole_SupervisorAccediendoARutaProtegida_200OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/horno/temperatura", nil)
	req.AddCookie(buildValidCookie(t, "Supervisor"))
	rr := httptest.NewRecorder()

	allowedRoles := []string{"Supervisor", "Administrador"}
	handlerChain := JWTMiddleware(testSecret, RequireRole(allowedRoles, dummyHandler))
	handlerChain(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "Supervisor debe recibir 200 OK")
}

func TestRequireRole_AdministradorAccediendoARutaProtegida_200OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/horno/temperatura", nil)
	req.AddCookie(buildValidCookie(t, "Administrador"))
	rr := httptest.NewRecorder()

	allowedRoles := []string{"Supervisor", "Administrador"}
	handlerChain := JWTMiddleware(testSecret, RequireRole(allowedRoles, dummyHandler))
	handlerChain(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "Administrador debe recibir 200 OK")
}
