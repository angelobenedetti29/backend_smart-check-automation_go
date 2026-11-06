package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/notificacion"
)

// NotificacionPostgresRepository implementa notificacion.Repository contra PostgreSQL.
type NotificacionPostgresRepository struct {
	db *pgxpool.Pool
}

// NewNotificacionPostgresRepository crea el repositorio real de notificaciones.
func NewNotificacionPostgresRepository(db *pgxpool.Pool) *NotificacionPostgresRepository {
	return &NotificacionPostgresRepository{db: db}
}

const selectDestinatariosSQL = `
SELECT u.id::text, u.email, u.nombre, u.rol, u.activo,
       NULLIF(n.telegram_chat_id, ''),
       COALESCE(n.notificar_email, true),
       COALESCE(n.notificar_telegram, true)
FROM usuarios u
LEFT JOIN usuarios_notificaciones n ON n.usuario_id = u.id`

// ListarDestinatarios devuelve todos los usuarios (activos e inactivos) con sus
// preferencias resueltas. Sin fila de preferencias, ambos canales quedan
// habilitados y Telegram sin chat ID.
func (r *NotificacionPostgresRepository) ListarDestinatarios(ctx context.Context) ([]notificacion.Destinatario, error) {
	rows, err := r.db.Query(ctx, selectDestinatariosSQL+` ORDER BY u.rol, u.nombre`)
	if err != nil {
		return nil, fmt.Errorf("query destinatarios: %w", err)
	}
	defer rows.Close()

	out := []notificacion.Destinatario{}
	for rows.Next() {
		var d notificacion.Destinatario
		if err := rows.Scan(
			&d.UsuarioID, &d.Email, &d.Nombre, &d.Rol, &d.Activo,
			&d.TelegramChatID, &d.NotificarEmail, &d.NotificarTelegram,
		); err != nil {
			return nil, fmt.Errorf("scan destinatario: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration destinatarios: %w", err)
	}
	return out, nil
}

// ObtenerDestinatario devuelve un usuario con sus preferencias.
func (r *NotificacionPostgresRepository) ObtenerDestinatario(ctx context.Context, usuarioID string) (*notificacion.Destinatario, error) {
	row := r.db.QueryRow(ctx, selectDestinatariosSQL+` WHERE u.id = $1`, usuarioID)

	var d notificacion.Destinatario
	if err := row.Scan(
		&d.UsuarioID, &d.Email, &d.Nombre, &d.Rol, &d.Activo,
		&d.TelegramChatID, &d.NotificarEmail, &d.NotificarTelegram,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || uuidInvalido(err) {
			return nil, notificacion.ErrDestinatarioNoEncontrado
		}
		return nil, fmt.Errorf("query destinatario: %w", err)
	}
	return &d, nil
}

const upsertPreferenciasSQL = `
INSERT INTO usuarios_notificaciones (usuario_id, telegram_chat_id, notificar_email, notificar_telegram)
VALUES ($1, $2, $3, $4)
ON CONFLICT (usuario_id) DO UPDATE
SET telegram_chat_id = EXCLUDED.telegram_chat_id,
    notificar_email = EXCLUDED.notificar_email,
    notificar_telegram = EXCLUDED.notificar_telegram,
    updated_at = now()`

// GuardarPreferencias hace upsert de las preferencias y devuelve el
// destinatario actualizado.
func (r *NotificacionPostgresRepository) GuardarPreferencias(ctx context.Context, p notificacion.Preferencias) (*notificacion.Destinatario, error) {
	chatID := p.TelegramChatID
	if chatID != nil && *chatID == "" {
		chatID = nil
	}

	if _, err := r.db.Exec(ctx, upsertPreferenciasSQL, p.UsuarioID, chatID, p.NotificarEmail, p.NotificarTelegram); err != nil {
		if uuidInvalido(err) {
			return nil, notificacion.ErrDestinatarioNoEncontrado
		}
		return nil, fmt.Errorf("upsert preferencias: %w", err)
	}
	return r.ObtenerDestinatario(ctx, p.UsuarioID)
}

// Encolar inserta las notificaciones en una transacción. El unique
// (alerta_id, usuario_id, canal) hace el encolado idempotente.
func (r *NotificacionPostgresRepository) Encolar(ctx context.Context, notificaciones []notificacion.Notificacion) error {
	if len(notificaciones) == 0 {
		return nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin encolar notificaciones: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	for _, n := range notificaciones {
		if _, err := tx.Exec(ctx, `
			INSERT INTO notificaciones_outbox (alerta_id, usuario_id, canal, destino, asunto, cuerpo, cuerpo_html)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (alerta_id, usuario_id, canal) DO NOTHING`,
			n.AlertaID, n.UsuarioID, string(n.Canal), n.Destino, n.Asunto, n.Cuerpo, n.CuerpoHTML,
		); err != nil {
			return fmt.Errorf("insert notificacion: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit encolar notificaciones: %w", err)
	}
	return nil
}

// Pendientes devuelve hasta `limite` filas listas para reintentar.
//
// shortcut: asume una única instancia del backend. Si se escala horizontalmente,
// reemplazar por SELECT ... FOR UPDATE SKIP LOCKED.
func (r *NotificacionPostgresRepository) Pendientes(ctx context.Context, limite int) ([]notificacion.Notificacion, error) {
	if limite <= 0 {
		limite = 20
	}

	rows, err := r.db.Query(ctx, `
		SELECT id::text, alerta_id::text, usuario_id::text, canal, destino, asunto, cuerpo, cuerpo_html,
		       estado, intentos, proximo_intento_at, COALESCE(ultimo_error, ''), created_at, sent_at
		FROM notificaciones_outbox
		WHERE estado = 'PENDIENTE' AND proximo_intento_at <= now()
		ORDER BY proximo_intento_at
		LIMIT $1`, limite)
	if err != nil {
		return nil, fmt.Errorf("query pendientes: %w", err)
	}
	defer rows.Close()

	out := []notificacion.Notificacion{}
	for rows.Next() {
		var n notificacion.Notificacion
		var canal, estado string
		if err := rows.Scan(
			&n.ID, &n.AlertaID, &n.UsuarioID, &canal, &n.Destino, &n.Asunto, &n.Cuerpo, &n.CuerpoHTML,
			&estado, &n.Intentos, &n.ProximoIntentoAt, &n.UltimoError, &n.CreadaEn, &n.EnviadaEn,
		); err != nil {
			return nil, fmt.Errorf("scan notificacion: %w", err)
		}
		n.Canal = notificacion.Canal(canal)
		n.Estado = notificacion.Estado(estado)
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration pendientes: %w", err)
	}
	return out, nil
}

// MarcarEnviada registra el envío exitoso.
func (r *NotificacionPostgresRepository) MarcarEnviada(ctx context.Context, id string) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE notificaciones_outbox
		SET estado = 'ENVIADO', sent_at = now(), ultimo_error = NULL
		WHERE id = $1`, id); err != nil {
		return fmt.Errorf("marcar enviada: %w", err)
	}
	return nil
}

// MarcarFallida incrementa intentos, guarda el error y reprograma el envío. Si
// agotada, deja la fila en FALLIDO.
func (r *NotificacionPostgresRepository) MarcarFallida(ctx context.Context, id, ultimoError string, proximo time.Time, agotada bool) error {
	if _, err := r.db.Exec(ctx, `
		UPDATE notificaciones_outbox
		SET intentos = intentos + 1,
		    ultimo_error = $2,
		    estado = CASE WHEN $4 THEN 'FALLIDO' ELSE 'PENDIENTE' END,
		    proximo_intento_at = $3
		WHERE id = $1`, id, ultimoError, proximo, agotada); err != nil {
		return fmt.Errorf("marcar fallida: %w", err)
	}
	return nil
}
