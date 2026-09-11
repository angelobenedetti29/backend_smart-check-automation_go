package user

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	userdomain "github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	userService "github.com/angelobenedetti29/smart-check-automation/internal/service/user"
)

// blockingUserRepo simula un repositorio que nunca responde y respeta el
// deadline del contexto, devolviendo context.DeadlineExceeded a través de
// handler → service → repo.
type blockingUserRepo struct{}

func (blockingUserRepo) FindByEmail(ctx context.Context, _ string) (*userdomain.User, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingUserRepo) FindByID(ctx context.Context, _ string) (*userdomain.User, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingUserRepo) FindAll(ctx context.Context) ([]*userdomain.User, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingUserRepo) Create(ctx context.Context, _ *userdomain.User) error {
	<-ctx.Done()
	return ctx.Err()
}

func (blockingUserRepo) Update(ctx context.Context, _ *userdomain.User) error {
	<-ctx.Done()
	return ctx.Err()
}

// expiredRequest devuelve una request con un contexto ya vencido para forzar
// de inmediato el camino de timeout del handler (sin esperar 10s reales).
func expiredRequest(t *testing.T, method, target string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	t.Cleanup(cancel)

	return req.WithContext(ctx)
}

func TestListUsers_DeadlineExcedido_504(t *testing.T) {
	handler := NewUserHandler(userService.NewService(blockingUserRepo{}))

	req := expiredRequest(t, http.MethodGet, "/api/v1/admin/usuarios", nil)
	rr := httptest.NewRecorder()

	handler.ListUsers(rr, req)

	assert.Equal(t, http.StatusGatewayTimeout, rr.Code)
	assert.Contains(t, rr.Body.String(), "Tiempo de espera agotado")
}

func TestCreateUser_DeadlineExcedido_504(t *testing.T) {
	handler := NewUserHandler(userService.NewService(blockingUserRepo{}))

	body := []byte(`{"email":"nuevo@fermar.com.ar","nombre":"Nuevo","rol":"Operario","password":"password123"}`)
	req := expiredRequest(t, http.MethodPost, "/api/v1/admin/usuarios", body)
	rr := httptest.NewRecorder()

	handler.CreateUser(rr, req)

	assert.Equal(t, http.StatusGatewayTimeout, rr.Code)
	assert.Contains(t, rr.Body.String(), "Tiempo de espera agotado")
}

func TestUpdateUser_DeadlineExcedido_504(t *testing.T) {
	handler := NewUserHandler(userService.NewService(blockingUserRepo{}))

	body := []byte(`{"rol":"Supervisor","activo":true}`)
	req := expiredRequest(t, http.MethodPatch, "/api/v1/admin/usuarios/usr-123", body)
	rr := httptest.NewRecorder()

	handler.UpdateUser(rr, req)

	assert.Equal(t, http.StatusGatewayTimeout, rr.Code)
	assert.Contains(t, rr.Body.String(), "Tiempo de espera agotado")
}
