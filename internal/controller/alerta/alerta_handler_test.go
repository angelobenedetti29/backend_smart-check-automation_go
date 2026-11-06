package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
)

const alertasTestJWTSecret = "test-secret-32-chars-alertas!!!"

// fakeAlertaRepo es un doble configurable del alerta.Repository.
type fakeAlertaRepo struct {
	lista     []alerta.Alerta
	listarErr error
	ackResp   *alerta.Alerta
	ackErr    error
	ackID     string
	ackUser   alerta.Usuario
}

func (f *fakeAlertaRepo) Save(*alerta.Alerta) error { return nil }

func (f *fakeAlertaRepo) GetByHornoID(string) ([]alerta.Alerta, error) { return f.lista, nil }

func (f *fakeAlertaRepo) Listar(context.Context, string) ([]alerta.Alerta, error) {
	return f.lista, f.listarErr
}

func (f *fakeAlertaRepo) Ack(_ context.Context, id string, usuario alerta.Usuario) (*alerta.Alerta, error) {
	f.ackID = id
	f.ackUser = usuario
	return f.ackResp, f.ackErr
}

// cookiePara firma un JWT válido con el rol indicado.
func cookiePara(t *testing.T, email, rol string) *http.Cookie {
	t.Helper()
	claims := &authService.Claims{
		Email: email,
		Name:  "Test",
		Role:  rol,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "smart-check-automation",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(alertasTestJWTSecret))
	require.NoError(t, err)
	return &http.Cookie{Name: "session_token", Value: signed}
}

// decodeJSON decodifica el envelope de respuesta.
func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	return body
}

// TestHandleListar_ArrayShape verifica 200 y que `data` sea un array.
func TestHandleListar_ArrayShape(t *testing.T) {
	repo := &fakeAlertaRepo{lista: []alerta.Alerta{
		{ID: "a1", Tipo: alerta.TipoTemperaturaHorno, Estado: alerta.EstadoActiva},
		{ID: "a2", Tipo: alerta.TipoPorcentajeQuemadosAlto, Estado: alerta.EstadoActiva},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alertas?estado=ACTIVA", nil)
	rr := httptest.NewRecorder()
	HandleListar(AlertaDeps{Repo: repo})(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	body := decodeJSON(t, rr)
	data, ok := body["data"].([]interface{})
	require.True(t, ok, "data debe ser un array")
	require.Len(t, data, 2)
}

// TestHandleListar_EstadoInvalido400 verifica que un estado fuera del enum se
// rechaza antes de consultar el repositorio.
func TestHandleListar_EstadoInvalido400(t *testing.T) {
	repo := &fakeAlertaRepo{}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/alertas?estado=CUALQUIERA", nil)
	rr := httptest.NewRecorder()
	HandleListar(AlertaDeps{Repo: repo})(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestHandleAck_RolInsuficiente verifica el guard de rol del handler encadenado.
func TestHandleAck_RolInsuficiente(t *testing.T) {
	deps := AlertaDeps{Repo: &fakeAlertaRepo{}}
	chain := authController.JWTMiddleware([]byte(alertasTestJWTSecret),
		authController.RequireRole([]string{user.RoleSupervisor, user.RoleAdmin}, HandleAck(deps)))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alertas/a1/ack", nil)
	req.AddCookie(cookiePara(t, "operario@fermar.com.ar", user.RoleOperario))
	rr := httptest.NewRecorder()
	chain(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
}

// TestHandleAck_NotFound verifica el mapeo del sentinel a 404.
func TestHandleAck_NotFound(t *testing.T) {
	repo := &fakeAlertaRepo{ackErr: alerta.ErrNotFound}
	deps := AlertaDeps{Repo: repo}
	chain := authController.JWTMiddleware([]byte(alertasTestJWTSecret),
		authController.RequireRole([]string{user.RoleSupervisor, user.RoleAdmin}, HandleAck(deps)))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alertas/00000000-0000-0000-0000-000000000000/ack", nil)
	req.AddCookie(cookiePara(t, "supervisor@fermar.com.ar", user.RoleSupervisor))
	rr := httptest.NewRecorder()
	chain(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
}

// TestHandleAck_YaReconocida verifica el mapeo del sentinel a 409.
func TestHandleAck_YaReconocida(t *testing.T) {
	repo := &fakeAlertaRepo{ackErr: alerta.ErrYaReconocida}
	deps := AlertaDeps{Repo: repo}
	chain := authController.JWTMiddleware([]byte(alertasTestJWTSecret),
		authController.RequireRole([]string{user.RoleSupervisor, user.RoleAdmin}, HandleAck(deps)))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alertas/a1/ack", nil)
	req.AddCookie(cookiePara(t, "supervisor@fermar.com.ar", user.RoleSupervisor))
	rr := httptest.NewRecorder()
	chain(rr, req)

	require.Equal(t, http.StatusConflict, rr.Code)
}

// TestHandleAck_PopulaAckedPor verifica que la respuesta incluye el usuario
// que reconoció la alerta y que el email del JWT llega al repositorio.
func TestHandleAck_PopulaAckedPor(t *testing.T) {
	ack := &alerta.Alerta{
		ID:       "a1",
		Tipo:     alerta.TipoTemperaturaHorno,
		Estado:   alerta.EstadoAck,
		AckedPor: &alerta.Usuario{Email: "supervisor@fermar.com.ar", Nombre: "Supervisor Real"},
	}
	repo := &fakeAlertaRepo{ackResp: ack}
	deps := AlertaDeps{Repo: repo}
	chain := authController.JWTMiddleware([]byte(alertasTestJWTSecret),
		authController.RequireRole([]string{user.RoleSupervisor, user.RoleAdmin}, HandleAck(deps)))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/alertas/a1/ack", nil)
	req.AddCookie(cookiePara(t, "supervisor@fermar.com.ar", user.RoleSupervisor))
	rr := httptest.NewRecorder()
	chain(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "a1", repo.ackID)
	require.Equal(t, "supervisor@fermar.com.ar", repo.ackUser.Email)

	body := decodeJSON(t, rr)
	data, ok := body["data"].(map[string]interface{})
	require.True(t, ok)
	ackedPor, ok := data["acked_por"].(map[string]interface{})
	require.True(t, ok, "la respuesta debe incluir acked_por")
	require.Equal(t, "supervisor@fermar.com.ar", ackedPor["email"])
	require.Equal(t, "Supervisor Real", ackedPor["nombre"])
}
