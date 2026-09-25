package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/bcrypt"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	authServicePkg "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
)

// testHandler construye un AuthHandler con un service fake usando reflexión de interfaz.
// Nota: como AuthHandler tiene *AuthService (concreto), usamos la misma técnica
// que el resto del proyecto (mock de la interfaz del dominio, no del service).
// Aquí testeamos el handler completo con un mock funcional del service subyacente.

func TestLoginHandler_MetodoGet_Rechazado(t *testing.T) {
	handler := &AuthHandler{svc: nil} // svc no se llama con método incorrecto
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/google", nil)
	rr := httptest.NewRecorder()

	handler.LoginWithGoogle(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestLoginHandler_BodyVacio_400(t *testing.T) {
	handler := &AuthHandler{svc: nil}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.LoginWithGoogle(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestLoginHandler_ContentTypeIncorrecto_415(t *testing.T) {
	handler := &AuthHandler{svc: nil}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google", bytes.NewReader([]byte(`{"googleToken":"abc"}`)))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()

	handler.LoginWithGoogle(rr, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
}

func TestLoginHandler_TokenInvalido_401(t *testing.T) {
	// Usar mocks de dominio para aislar el handler
	verifier := &mockVerifier{err: user.ErrInvalidToken}
	repo := &mockRepo{err: nil}
	svc := newMockAuthService(verifier, repo)
	handler := NewAuthHandler(svc)

	body, _ := json.Marshal(map[string]string{"googleToken": "token-invalido"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.LoginWithGoogle(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.False(t, resp["success"].(bool))
}

func TestLoginHandler_UsuarioNoCorporativo_401(t *testing.T) {
	verifier := &mockVerifier{claims: &user.GoogleClaims{Email: "externo@gmail.com", Name: "Ext"}}
	repo := &mockRepo{err: user.ErrUserNotFound}
	svc := newMockAuthService(verifier, repo)
	handler := NewAuthHandler(svc)

	body, _ := json.Marshal(map[string]string{"googleToken": "token-valido"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.LoginWithGoogle(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.False(t, resp["success"].(bool))
	// Mensaje genérico — no debe revelar si el email existe
	assert.NotContains(t, resp["message"], "registrado")
}

func TestLoginHandler_Exitoso_SeteaCookie(t *testing.T) {
	verifier := &mockVerifier{claims: &user.GoogleClaims{Email: "admin@fermar.com.ar", Name: "Admin"}}
	repo := &mockRepo{u: &user.User{Email: "admin@fermar.com.ar", Nombre: "Admin Fermar", Rol: user.RoleAdmin}}
	svc := newMockAuthService(verifier, repo)
	handler := NewAuthHandler(svc)

	body, _ := json.Marshal(map[string]string{"googleToken": "token-valido"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/google", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.LoginWithGoogle(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// Verificar que se seteó la cookie de sesión
	cookies := rr.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session_token" {
			sessionCookie = c
			break
		}
	}
	assert.NotNil(t, sessionCookie, "debe existir la cookie session_token")
	assert.NotEmpty(t, sessionCookie.Value)
	assert.True(t, sessionCookie.HttpOnly, "cookie debe ser HttpOnly")
	assert.True(t, sessionCookie.Secure, "cookie debe ser Secure")
	assert.Equal(t, http.SameSiteStrictMode, sessionCookie.SameSite)
	assert.Greater(t, sessionCookie.MaxAge, 0)
}

func TestLogoutHandler_LimpiaCookie(t *testing.T) {
	handler := &AuthHandler{svc: nil}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	rr := httptest.NewRecorder()

	handler.Logout(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	cookies := rr.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session_token" {
			sessionCookie = c
			break
		}
	}
	assert.NotNil(t, sessionCookie)
	assert.Equal(t, -1, sessionCookie.MaxAge, "MaxAge=-1 instruye al browser a eliminar la cookie")
	assert.Empty(t, sessionCookie.Value)
}

func TestLocalLoginHandler_MetodoGet_Rechazado(t *testing.T) {
	handler := &AuthHandler{svc: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login", nil)
	rr := httptest.NewRecorder()

	handler.Login(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestLocalLoginHandler_ContentTypeIncorrecto_415(t *testing.T) {
	handler := &AuthHandler{svc: nil}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader([]byte(`{"email":"a@b.com","password":"123"}`)))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()

	handler.Login(rr, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
}

func TestLocalLoginHandler_CamposFaltantes_400(t *testing.T) {
	handler := &AuthHandler{svc: nil}

	// Email faltante
	body, _ := json.Marshal(map[string]string{"password": "123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Login(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)

	// Password faltante
	body2, _ := json.Marshal(map[string]string{"email": "test@fermar.com.ar"})
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()

	handler.Login(rr2, req2)
	assert.Equal(t, http.StatusBadRequest, rr2.Code)
}

func TestLocalLoginHandler_CredencialesInvalidas_401(t *testing.T) {
	repo := &mockRepo{err: user.ErrUserNotFound}
	svc := newMockAuthService(nil, repo)
	handler := NewAuthHandler(svc)

	body, _ := json.Marshal(map[string]string{"email": "invalido@fermar.com.ar", "password": "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Login(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.False(t, resp["success"].(bool))
}

func TestLocalLoginHandler_Exitoso_SeteaCookie(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	validHash := string(hash)
	repo := &mockRepo{u: &user.User{Email: "admin@fermar.com.ar", Nombre: "Admin Fermar", Rol: user.RoleAdmin, PasswordHash: validHash}}
	svc := newMockAuthService(nil, repo)
	handler := NewAuthHandler(svc)

	body, _ := json.Marshal(map[string]string{"email": "admin@fermar.com.ar", "password": "password123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Login(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	cookies := rr.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session_token" {
			sessionCookie = c
			break
		}
	}
	assert.NotNil(t, sessionCookie, "debe existir la cookie session_token")
	assert.NotEmpty(t, sessionCookie.Value)
	assert.True(t, sessionCookie.HttpOnly, "cookie debe ser HttpOnly")
	assert.True(t, sessionCookie.Secure, "cookie debe ser Secure")
	assert.Equal(t, http.SameSiteStrictMode, sessionCookie.SameSite)
	assert.Greater(t, sessionCookie.MaxAge, 0)
}

// --- Tests de deadline excedido (timeout controlado) ---

// expiredRequest devuelve una request con un contexto ya vencido. Al envolverlo
// con context.WithTimeout en el handler, el deadline derivado queda vencido de
// inmediato, lo que dispara el camino de timeout sin esperar 10s reales.
func expiredRequest(t *testing.T, method, target string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	t.Cleanup(cancel)

	return req.WithContext(ctx)
}

func TestLocalLoginHandler_DeadlineExcedido_504(t *testing.T) {
	repo := &mockRepo{err: context.DeadlineExceeded}
	svc := newMockAuthService(nil, repo)
	handler := NewAuthHandler(svc)

	body, _ := json.Marshal(map[string]string{"email": "admin@fermar.com.ar", "password": "password123"})
	req := expiredRequest(t, http.MethodPost, "/api/v1/auth/login", body)
	rr := httptest.NewRecorder()

	handler.Login(rr, req)

	assert.Equal(t, http.StatusGatewayTimeout, rr.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.False(t, resp["success"].(bool))
}

func TestLoginHandler_DeadlineExcedido_504(t *testing.T) {
	// El verifier devuelve context.DeadlineExceeded: el service debe preservarlo
	// (no convertirlo en ErrInvalidToken) para que el handler responda 504.
	verifier := &mockVerifier{err: context.DeadlineExceeded}
	svc := newMockAuthService(verifier, &mockRepo{})
	handler := NewAuthHandler(svc)

	body, _ := json.Marshal(map[string]string{"googleToken": "token-cualquiera"})
	req := expiredRequest(t, http.MethodPost, "/api/v1/auth/google", body)
	rr := httptest.NewRecorder()

	handler.LoginWithGoogle(rr, req)

	assert.Equal(t, http.StatusGatewayTimeout, rr.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.False(t, resp["success"].(bool))
}

// --- Helpers para construir un AuthService real con mocks de dominio ---

type mockVerifier struct {
	claims *user.GoogleClaims
	err    error
}

func (m *mockVerifier) Verify(_ context.Context, _ string) (*user.GoogleClaims, error) {
	return m.claims, m.err
}

type mockRepo struct {
	u   *user.User
	err error
}

func (m *mockRepo) FindByEmail(_ context.Context, _ string) (*user.User, error) {
	return m.u, m.err
}

func (m *mockRepo) FindByID(_ context.Context, _ string) (*user.User, error) {
	return m.u, m.err
}

func (m *mockRepo) FindAll(_ context.Context) ([]*user.User, error) {
	return []*user.User{m.u}, m.err
}

func (m *mockRepo) Create(_ context.Context, _ *user.User) error {
	return m.err
}

func (m *mockRepo) Update(_ context.Context, _ *user.User) error {
	return m.err
}

func newMockAuthService(v user.GoogleVerifier, r user.Repository) *authServicePkg.AuthService {
	return authServicePkg.NewAuthService(v, r, "test-secret-32-chars-exactly!!!")
}
