package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/notificacion"
	notificacionService "github.com/angelobenedetti29/smart-check-automation/internal/service/notificacion"
)

type fakeService struct {
	destinatarios []notificacion.Destinatario
	listErr       error
	updateResp    *notificacion.Destinatario
	updateErr     error
	updateID      string
	updatePatch   notificacion.PreferenciasPatch
	probarID      string
	probarCanal   notificacion.Canal
	probarErr     error
	cfg           notificacionService.Configuracion
}

func (f *fakeService) ListarDestinatarios(ctx context.Context) ([]notificacion.Destinatario, error) {
	return f.destinatarios, f.listErr
}

func (f *fakeService) ActualizarPreferencias(ctx context.Context, usuarioID string, patch notificacion.PreferenciasPatch) (*notificacion.Destinatario, error) {
	f.updateID, f.updatePatch = usuarioID, patch
	return f.updateResp, f.updateErr
}

func (f *fakeService) Probar(ctx context.Context, usuarioID string, canal notificacion.Canal) error {
	f.probarID, f.probarCanal = usuarioID, canal
	return f.probarErr
}

func (f *fakeService) Configuracion() notificacionService.Configuracion { return f.cfg }

type envelope struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
	Errors  any            `json:"errors"`
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	var e envelope
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&e))
	return e
}

func TestHandleColeccion(t *testing.T) {
	svc := &fakeService{
		destinatarios: []notificacion.Destinatario{{
			UsuarioID: "u1", Email: "a@x.com", Nombre: "Admin", Rol: "Administrador", Activo: true,
			NotificarEmail: true, NotificarTelegram: true,
		}},
		cfg: notificacionService.Configuracion{EmailConfigurado: true, TelegramConfigurado: false, Remitente: "alertas@x.com"},
	}
	h := NewHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleColeccion(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/notificaciones", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	e := decodeEnvelope(t, rec)
	require.True(t, e.Success)
	require.Len(t, e.Data["destinatarios"], 1)
	canales := e.Data["canales"].(map[string]any)
	require.Equal(t, true, canales["email"].(map[string]any)["configurado"])
	require.Equal(t, false, canales["telegram"].(map[string]any)["configurado"])
	require.Equal(t, "alertas@x.com", canales["email"].(map[string]any)["remitente"])
}

func TestHandleColeccion_MetodoInvalido(t *testing.T) {
	h := NewHandler(&fakeService{})
	rec := httptest.NewRecorder()
	h.HandleColeccion(rec, httptest.NewRequest(http.MethodPost, "/api/v1/admin/notificaciones", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandleUsuario(t *testing.T) {
	chat := "123456"
	svc := &fakeService{updateResp: &notificacion.Destinatario{UsuarioID: "u1", TelegramChatID: &chat}}
	h := NewHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/notificaciones/u1",
		strings.NewReader(`{"telegram_chat_id":"123456","notificar_email":false}`))
	req.Header.Set("Content-Type", "application/json")
	h.HandleUsuario(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "u1", svc.updateID)
	require.NotNil(t, svc.updatePatch.TelegramChatID)
	require.Equal(t, "123456", *svc.updatePatch.TelegramChatID)
	require.NotNil(t, svc.updatePatch.NotificarEmail)
	require.False(t, *svc.updatePatch.NotificarEmail)
}

func TestHandleUsuario_Errores(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
	}{
		{"no encontrado", notificacion.ErrDestinatarioNoEncontrado, http.StatusNotFound},
		{"chat inválido", notificacion.ErrChatIDInvalido, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(&fakeService{updateErr: tc.err})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/notificaciones/u1", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			h.HandleUsuario(rec, req)
			require.Equal(t, tc.code, rec.Code)
		})
	}
}

func TestHandleUsuario_ContentTypeYPath(t *testing.T) {
	h := NewHandler(&fakeService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/notificaciones/u1", strings.NewReader(`{}`))
	h.HandleUsuario(rec, req)
	require.Equal(t, http.StatusUnsupportedMediaType, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/admin/notificaciones/", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	h.HandleUsuario(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleTest(t *testing.T) {
	svc := &fakeService{}
	h := NewHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/notificaciones/test",
		strings.NewReader(`{"canal":"telegram","usuario_id":"u1"}`))
	req.Header.Set("Content-Type", "application/json")
	h.HandleTest(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, notificacion.CanalTelegram, svc.probarCanal)
	require.Equal(t, "u1", svc.probarID)
}

func TestHandleTest_Errores(t *testing.T) {
	tests := []struct {
		name string
		body string
		err  error
		code int
	}{
		{"canal inválido", `{"canal":"SMS","usuario_id":"u1"}`, nil, http.StatusBadRequest},
		{"sin usuario", `{"canal":"EMAIL"}`, nil, http.StatusBadRequest},
		{"no configurado", `{"canal":"EMAIL","usuario_id":"u1"}`, notificacion.ErrCanalNoConfigurado, http.StatusConflict},
		{"sin destino", `{"canal":"EMAIL","usuario_id":"u1"}`, notificacion.ErrSinDestino, http.StatusBadRequest},
		{"no encontrado", `{"canal":"EMAIL","usuario_id":"u1"}`, notificacion.ErrDestinatarioNoEncontrado, http.StatusNotFound},
		{"proveedor", `{"canal":"EMAIL","usuario_id":"u1"}`, errors.New("boom"), http.StatusBadGateway},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(&fakeService{probarErr: tc.err})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/notificaciones/test", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			h.HandleTest(rec, req)
			require.Equal(t, tc.code, rec.Code)
		})
	}
}
