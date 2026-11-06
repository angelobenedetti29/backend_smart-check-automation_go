package repository

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
)

// EncoladorNotificaciones encola las notificaciones de una alerta recién
// persistida. Lo implementa el service de notificaciones.
type EncoladorNotificaciones interface {
	EncolarParaAlerta(ctx context.Context, a *alerta.Alerta) error
}

// AlertaPostgresRepository implementa alerta.Repository contra PostgreSQL.
type AlertaPostgresRepository struct {
	db        *pgxpool.Pool
	encolador EncoladorNotificaciones
}

// NewAlertaPostgresRepository crea un repositorio real conectado a PostgreSQL.
func NewAlertaPostgresRepository(db *pgxpool.Pool) *AlertaPostgresRepository {
	return &AlertaPostgresRepository{db: db}
}

// SetEncolador configura el encolador de notificaciones. Nil lo deshabilita.
func (r *AlertaPostgresRepository) SetEncolador(e EncoladorNotificaciones) {
	r.encolador = e
}

const insertAlertaSQL = `
INSERT INTO alertas (sector_id, lote_id, dispositivo_id, horno_id, tipo, severidad, estado, mensaje)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT DO NOTHING
RETURNING id, created_at`

// Save persiste una alerta. La interfaz no recibe contexto (compatibilidad con
// horno_service/horno_handler), así que el repositorio acota la operación con
// su propio timeout para no bloquear indefinidamente.
//
// Un conflicto con los índices parciales de deduplicación NO es error: la
// alerta ACTIVA equivalente ya existe y se descarta silenciosamente.
func (r *AlertaPostgresRepository) Save(a *alerta.Alerta) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	estado := a.Estado
	if estado == "" {
		estado = alerta.EstadoActiva
	}
	var hornoID *string
	if a.HornoID != "" {
		hornoID = &a.HornoID
	}

	err := r.db.QueryRow(ctx, insertAlertaSQL,
		a.SectorID, a.LoteID, a.DispositivoID, hornoID, a.Tipo, a.Severidad, estado, a.Mensaje,
	).Scan(&a.ID, &a.CreadaEn)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // deduplicada por un índice parcial: no es un error
	}
	if err != nil {
		return fmt.Errorf("insert alerta: %w", err)
	}

	// shortcut: el encolado no es atómico con el INSERT de la alerta. Si se
	// necesita at-least-once estricto, moverlo a un CTE/transacción única.
	if r.encolador != nil && a.ID != "" {
		encCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.encolador.EncolarParaAlerta(encCtx, a); err != nil {
			log.Printf("[ALERTA] no se pudo encolar notificaciones para alerta %s: %v", a.ID, err)
		}
	}

	return nil
}

const selectAlertaColumns = `
a.id, COALESCE(a.sector_id::text, ''), COALESCE(a.lote_id::text, ''),
COALESCE(a.dispositivo_id::text, ''), COALESCE(a.horno_id, ''), a.tipo,
a.severidad, a.estado, a.mensaje, a.created_at, a.acked_at,
COALESCE(u.email, ''), COALESCE(u.nombre, '')
FROM alertas a
LEFT JOIN usuarios u ON u.id = a.acked_by`

// GetByHornoID devuelve las últimas 50 alertas del horno, más recientes primero.
func (r *AlertaPostgresRepository) GetByHornoID(hornoID string) ([]alerta.Alerta, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := r.db.Query(ctx, `
		SELECT `+selectAlertaColumns+`
		WHERE a.horno_id = $1
		ORDER BY a.created_at DESC
		LIMIT 50`, hornoID)
	if err != nil {
		return nil, fmt.Errorf("query alertas por horno: %w", err)
	}
	return scanAlertas(rows)
}

// Listar devuelve las últimas 200 alertas, opcionalmente filtradas por estado.
func (r *AlertaPostgresRepository) Listar(ctx context.Context, estado string) ([]alerta.Alerta, error) {
	query := `SELECT ` + selectAlertaColumns
	args := []any{}
	if estado != "" {
		query += ` WHERE a.estado = $1`
		args = append(args, estado)
	}
	query += ` ORDER BY a.created_at DESC LIMIT 200`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query alertas: %w", err)
	}
	return scanAlertas(rows)
}

const ackAlertaSQL = `
UPDATE alertas
SET estado = 'ACK',
    acked_by = (SELECT id FROM usuarios WHERE email = $2),
    acked_at = now()
WHERE id = $1 AND estado = 'ACTIVA'`

// Ack reconoce una alerta ACTIVA y devuelve la fila actualizada (con el
// usuario real resuelto por email en `usuarios`).
func (r *AlertaPostgresRepository) Ack(ctx context.Context, id string, usuario alerta.Usuario) (*alerta.Alerta, error) {
	actual, err := r.getByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if actual == nil {
		return nil, alerta.ErrNotFound
	}
	if actual.Estado != alerta.EstadoActiva {
		return nil, alerta.ErrYaReconocida
	}

	tag, err := r.db.Exec(ctx, ackAlertaSQL, id, usuario.Email)
	if err != nil {
		return nil, fmt.Errorf("ack alerta: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Otra request la reconoció entre la lectura y el UPDATE.
		return nil, alerta.ErrYaReconocida
	}

	actualizada, err := r.getByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if actualizada == nil {
		return nil, alerta.ErrNotFound
	}
	return actualizada, nil
}

// getByID devuelve la alerta o (nil, nil) si no existe. Un id que no es UUID
// se trata como inexistente en lugar de propagar el error de PostgreSQL.
func (r *AlertaPostgresRepository) getByID(ctx context.Context, id string) (*alerta.Alerta, error) {
	row := r.db.QueryRow(ctx, `SELECT `+selectAlertaColumns+` WHERE a.id = $1`, id)
	var a alerta.Alerta
	if err := scanAlerta(row, &a); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || uuidInvalido(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("query alerta por id: %w", err)
	}
	return &a, nil
}

func scanAlertas(rows pgx.Rows) ([]alerta.Alerta, error) {
	defer rows.Close()

	out := []alerta.Alerta{}
	for rows.Next() {
		var a alerta.Alerta
		if err := scanAlerta(rows, &a); err != nil {
			return nil, fmt.Errorf("scan alerta: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration alertas: %w", err)
	}
	return out, nil
}

func scanAlerta(s rowScanner, a *alerta.Alerta) error {
	var sectorID, loteID, dispositivoID, ackEmail, ackNombre string
	if err := s.Scan(
		&a.ID, &sectorID, &loteID, &dispositivoID, &a.HornoID, &a.Tipo, &a.Severidad, &a.Estado,
		&a.Mensaje, &a.CreadaEn, &a.AckedAt, &ackEmail, &ackNombre,
	); err != nil {
		return err
	}
	if sectorID != "" {
		a.SectorID = &sectorID
	}
	if loteID != "" {
		a.LoteID = &loteID
	}
	if dispositivoID != "" {
		a.DispositivoID = &dispositivoID
	}
	if ackEmail != "" {
		a.AckedPor = &alerta.Usuario{Email: ackEmail, Nombre: ackNombre}
	}
	return nil
}

// uuidInvalido detecta el error de PostgreSQL por cast de UUID malformado.
func uuidInvalido(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}
