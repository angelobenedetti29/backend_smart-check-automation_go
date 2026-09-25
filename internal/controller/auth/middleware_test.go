package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"

	userDomain "github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	"github.com/angelobenedetti29/smart-check-automation/internal/guard"
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

// stubUserRepository is a minimal userDomain.Repository for rate-limit tests.
type stubUserRepository struct {
	byEmail map[string]*userDomain.User
}

func (s stubUserRepository) FindByEmail(_ context.Context, email string) (*userDomain.User, error) {
	if u, ok := s.byEmail[email]; ok {
		return u, nil
	}
	return nil, userDomain.ErrUserNotFound
}

func (s stubUserRepository) FindByID(context.Context, string) (*userDomain.User, error) {
	return nil, nil
}
func (s stubUserRepository) FindAll(context.Context) ([]*userDomain.User, error) { return nil, nil }
func (s stubUserRepository) Create(context.Context, *userDomain.User) error      { return nil }
func (s stubUserRepository) Update(context.Context, *userDomain.User) error      { return nil }

func userRecord(id, email, role string) *userDomain.User {
	return &userDomain.User{ID: id, Email: email, Rol: role, Activo: true}
}

// requestWithClaims injects the JWT claims the way JWTMiddleware would, so the
// test exercises RateLimitByUser specifically.
func requestWithClaims(method, target, email, role string) *http.Request {
	claims := &authService.Claims{Email: email, Role: role}
	ctx := context.WithValue(context.Background(), claimsKey, claims)
	return httptest.NewRequest(method, target, nil).WithContext(ctx)
}

// TestRateLimitByUser_ReadsDoNotConsumeMutationQuota is the regression: GETs
// through the management chain must not spend the write allowance.
func TestRateLimitByUser_ReadsDoNotConsumeMutationQuota(t *testing.T) {
	limiter := guard.NewLimiter(0, 1) // no refill, burst 1
	repo := stubUserRepository{byEmail: map[string]*userDomain.User{"op@fermar.com.ar": userRecord("u-1", "op@fermar.com.ar", "Supervisor")}}
	calls := 0
	next := func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusOK) }
	chain := RateLimitByUser(repo, limiter, next)

	for i := 0; i < 10; i++ {
		rw := httptest.NewRecorder()
		chain(rw, requestWithClaims(http.MethodGet, "/api/v1/dispositivos", "op@fermar.com.ar", "Supervisor"))
		assert.Equal(t, http.StatusOK, rw.Code, "read #%d must not be limited", i)
	}

	mutation := httptest.NewRecorder()
	chain(mutation, requestWithClaims(http.MethodPost, "/api/v1/dispositivos/dev-1/reprovision", "op@fermar.com.ar", "Supervisor"))
	assert.Equal(t, http.StatusOK, mutation.Code, "the first mutation after reads must be allowed")
	assert.Equal(t, 11, calls, "10 reads + 1 mutation must reach next")

	second := httptest.NewRecorder()
	chain(second, requestWithClaims(http.MethodPost, "/api/v1/dispositivos/dev-1/reprovision", "op@fermar.com.ar", "Supervisor"))
	assert.Equal(t, http.StatusTooManyRequests, second.Code, "second mutation must exhaust the burst-1 quota")
	assert.Equal(t, 11, calls, "limited mutation must not reach next")
}

// TestRateLimitByUser_TwoUsersSharePeerRemainIndependent verifies the key is
// the database user UUID, not the transport peer.
func TestRateLimitByUser_TwoUsersSharePeerRemainIndependent(t *testing.T) {
	limiter := guard.NewLimiter(0, 1)
	repo := stubUserRepository{byEmail: map[string]*userDomain.User{
		"a@fermar.com.ar": userRecord("u-a", "a@fermar.com.ar", "Supervisor"),
		"b@fermar.com.ar": userRecord("u-b", "b@fermar.com.ar", "Supervisor"),
	}}
	next := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	chain := RateLimitByUser(repo, limiter, next)

	first := httptest.NewRecorder()
	r1 := requestWithClaims(http.MethodPost, "/api/v1/dispositivos/dev-1/reprovision", "a@fermar.com.ar", "Supervisor")
	r1.RemoteAddr = "203.0.113.7:4444"
	chain(first, r1)
	assert.Equal(t, http.StatusOK, first.Code)

	// Same peer address, different user: its own bucket must still have a token.
	second := httptest.NewRecorder()
	r2 := requestWithClaims(http.MethodPost, "/api/v1/dispositivos/dev-1/reprovision", "b@fermar.com.ar", "Supervisor")
	r2.RemoteAddr = "203.0.113.7:4444"
	chain(second, r2)
	assert.Equal(t, http.StatusOK, second.Code, "user B must not be throttled by user A on the same peer")

	exhausted := httptest.NewRecorder()
	chain(exhausted, r1)
	assert.Equal(t, http.StatusTooManyRequests, exhausted.Code, "user A's own second mutation must be limited")
}

// TestRateLimitByUser_InactiveUserRejected preserves the fail-closed DB check.
func TestRateLimitByUser_InactiveUserRejected(t *testing.T) {
	limiter := guard.NewLimiter(1, 1)
	inactive := userRecord("u-1", "off@fermar.com.ar", "Supervisor")
	inactive.Activo = false
	repo := stubUserRepository{byEmail: map[string]*userDomain.User{"off@fermar.com.ar": inactive}}
	called := false
	chain := RateLimitByUser(repo, limiter, func(w http.ResponseWriter, r *http.Request) { called = true })

	rw := httptest.NewRecorder()
	chain(rw, requestWithClaims(http.MethodPost, "/api/v1/dispositivos/dev-1/reprovision", "off@fermar.com.ar", "Supervisor"))
	assert.Equal(t, http.StatusUnauthorized, rw.Code)
	assert.False(t, called)
}

// TestRateLimitByUser_MissingClaimsRejected preserves the 401 for no session.
func TestRateLimitByUser_MissingClaimsRejected(t *testing.T) {
	limiter := guard.NewLimiter(1, 1)
	repo := stubUserRepository{byEmail: map[string]*userDomain.User{}}
	called := false
	chain := RateLimitByUser(repo, limiter, func(w http.ResponseWriter, r *http.Request) { called = true })

	rw := httptest.NewRecorder()
	chain(rw, httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/dev-1/reprovision", nil))
	assert.Equal(t, http.StatusUnauthorized, rw.Code)
	assert.False(t, called)
}
