package notificacion

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	domain "github.com/angelobenedetti29/smart-check-automation/internal/domain/notificacion"
)

func TestProbarAlertaTodosLosTipos(t *testing.T) {
	repo := &fakeRepo{destinatarios: []domain.Destinatario{
		{UsuarioID: "u1", Email: "a@x.com", Activo: true, TelegramChatID: chatPtr("123456")},
		{UsuarioID: "u2", Email: "b@x.com", Activo: true},
	}}
	email := &fakeEmail{}
	telegram := &fakeTelegram{}
	svc := NewService(repo, email, telegram, "https://panel.test", "")

	for _, tipo := range TiposAlertaPrueba() {
		require.NoError(t, svc.ProbarAlerta(context.Background(), "u1", domain.CanalEmail, tipo), tipo)
		require.Contains(t, email.asunto, "[SmartCheck]", tipo)
		require.Contains(t, email.cuerpo, "[PRUEBA]", tipo)
		require.NotEmpty(t, email.html, tipo)

		require.NoError(t, svc.ProbarAlerta(context.Background(), "u1", domain.CanalTelegram, tipo), tipo)
		require.Contains(t, telegram.texto, "[PRUEBA]", tipo)
		require.Contains(t, telegram.texto, "https://panel.test/alertas", tipo)
	}
	require.Equal(t, len(TiposAlertaPrueba()), email.calls)
	require.Equal(t, len(TiposAlertaPrueba()), telegram.calls)

	// Se acepta minúscula y la severidad real del tipo se respeta.
	require.NoError(t, svc.ProbarAlerta(context.Background(), "u1", domain.CanalEmail, "precalentamiento_timeout"))
	require.Contains(t, email.asunto, "CRITICAL")

	require.ErrorIs(t, svc.ProbarAlerta(context.Background(), "u1", domain.CanalEmail, "NO_EXISTE"), domain.ErrTipoAlertaInvalido)
	require.ErrorIs(t, svc.ProbarAlerta(context.Background(), "u2", domain.CanalTelegram, "DISPOSITIVO_OFFLINE"), domain.ErrSinDestino)
	require.ErrorIs(t, svc.ProbarAlerta(context.Background(), "desconocido", domain.CanalEmail, "DISPOSITIVO_OFFLINE"), domain.ErrDestinatarioNoEncontrado)
	require.ErrorIs(t, NewService(repo, nil, nil, "", "").ProbarAlerta(context.Background(), "u1", domain.CanalEmail, "DISPOSITIVO_OFFLINE"), domain.ErrCanalNoConfigurado)
}
