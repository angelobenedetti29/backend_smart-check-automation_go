package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/notificacion"
)

func TestNotificacionRepo_DestinatariosYPreferencias(t *testing.T) {
	ctx := context.Background()
	_, pool, _ := setupAlertaRepo(t)
	repo := NewNotificacionPostgresRepository(pool)

	destinatarios, err := repo.ListarDestinatarios(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, destinatarios)

	var antes *notificacion.Destinatario
	for i := range destinatarios {
		if destinatarios[i].UsuarioID == alertaTestUsuarioID {
			antes = &destinatarios[i]
			break
		}
	}
	require.NotNil(t, antes)
	require.True(t, antes.NotificarEmail, "sin fila de preferencias el email queda habilitado")
	require.True(t, antes.NotificarTelegram, "sin fila de preferencias Telegram queda habilitado")
	require.Nil(t, antes.TelegramChatID)

	chat := "123456"
	actualizado, err := repo.GuardarPreferencias(ctx, notificacion.Preferencias{
		UsuarioID:         alertaTestUsuarioID,
		TelegramChatID:    &chat,
		NotificarEmail:    false,
		NotificarTelegram: true,
	})
	require.NoError(t, err)
	require.NotNil(t, actualizado.TelegramChatID)
	require.Equal(t, chat, *actualizado.TelegramChatID)
	require.False(t, actualizado.NotificarEmail)

	obtenido, err := repo.ObtenerDestinatario(ctx, alertaTestUsuarioID)
	require.NoError(t, err)
	require.Equal(t, chat, *obtenido.TelegramChatID)
	require.False(t, obtenido.NotificarEmail)

	// Un segundo upsert reemplaza los valores.
	vacio := ""
	actualizado, err = repo.GuardarPreferencias(ctx, notificacion.Preferencias{
		UsuarioID:         alertaTestUsuarioID,
		TelegramChatID:    &vacio,
		NotificarEmail:    true,
		NotificarTelegram: false,
	})
	require.NoError(t, err)
	require.Nil(t, actualizado.TelegramChatID)
	require.True(t, actualizado.NotificarEmail)
	require.False(t, actualizado.NotificarTelegram)

	_, err = repo.ObtenerDestinatario(ctx, "00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, notificacion.ErrDestinatarioNoEncontrado)
	_, err = repo.ObtenerDestinatario(ctx, "no-es-uuid")
	require.ErrorIs(t, err, notificacion.ErrDestinatarioNoEncontrado)
}

func TestNotificacionRepo_EncolarDedupeYPendientes(t *testing.T) {
	ctx := context.Background()
	alertaRepo, pool, loteID := setupAlertaRepo(t)
	repo := NewNotificacionPostgresRepository(pool)

	a := alertaDePrueba(loteID, alerta.TipoTemperaturaHorno, "notificar")
	require.NoError(t, alertaRepo.Save(a))

	base := notificacion.Notificacion{
		AlertaID:  a.ID,
		UsuarioID: alertaTestUsuarioID,
		Asunto:    "[SmartCheck] WARNING · Temperatura de horno",
		Cuerpo:    "cuerpo",
	}
	email := base
	email.Canal = notificacion.CanalEmail
	email.Destino = alertaTestEmail
	email.CuerpoHTML = "<p>html</p>"
	telegram := base
	telegram.Canal = notificacion.CanalTelegram
	telegram.Destino = "123456"

	require.NoError(t, repo.Encolar(ctx, []notificacion.Notificacion{email, telegram}))
	require.NoError(t, repo.Encolar(ctx, []notificacion.Notificacion{email}), "el unique debe deduplicar")

	var total int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM notificaciones_outbox`).Scan(&total))
	require.Equal(t, 2, total)

	pendientes, err := repo.Pendientes(ctx, 20)
	require.NoError(t, err)
	require.Len(t, pendientes, 2)
	for _, n := range pendientes {
		if n.Canal == notificacion.CanalEmail {
			require.Equal(t, "<p>html</p>", n.CuerpoHTML)
		}
		if n.Canal == notificacion.CanalTelegram {
			require.Empty(t, n.CuerpoHTML)
		}
	}

	// Una fila futura no debe salir como pendiente.
	require.NoError(t, repo.MarcarFallida(ctx, pendientes[0].ID, "boom", time.Now().Add(time.Hour), false))
	pendientes, err = repo.Pendientes(ctx, 20)
	require.NoError(t, err)
	require.Len(t, pendientes, 1)

	require.NoError(t, repo.MarcarEnviada(ctx, pendientes[0].ID))
	var estado string
	var sentAt *time.Time
	var ultimoError *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT estado, sent_at, ultimo_error FROM notificaciones_outbox WHERE id=$1`, pendientes[0].ID,
	).Scan(&estado, &sentAt, &ultimoError))
	require.Equal(t, string(notificacion.EstadoEnviado), estado)
	require.NotNil(t, sentAt)
	require.Nil(t, ultimoError)

	// La fallida agotada pasa a FALLIDO y no vuelve a la cola.
	var fallidaID string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id::text FROM notificaciones_outbox WHERE estado='PENDIENTE' LIMIT 1`).Scan(&fallidaID))
	require.NoError(t, repo.MarcarFallida(ctx, fallidaID, "boom", time.Now(), true))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT estado, intentos, ultimo_error FROM notificaciones_outbox WHERE id=$1`, fallidaID,
	).Scan(&estado, new(int), &ultimoError))
	require.Equal(t, string(notificacion.EstadoFallido), estado)
}
