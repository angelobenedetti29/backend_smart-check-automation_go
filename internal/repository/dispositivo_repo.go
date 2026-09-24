package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
)

// PostgresDispositivoRepository implementa dispositivo.Repository usando pgxpool.
type PostgresDispositivoRepository struct {
	db *pgxpool.Pool
}

// NewPostgresDispositivoRepository crea un repositorio real conectado a PostgreSQL.
func NewPostgresDispositivoRepository(db *pgxpool.Pool) *PostgresDispositivoRepository {
	return &PostgresDispositivoRepository{db: db}
}

// normalizeSectorID trata un sectorId vacío como "sin sector" (NULL).
func normalizeSectorID(sectorID *string) *string {
	if sectorID == nil {
		return nil
	}
	if strings.TrimSpace(*sectorID) == "" {
		return nil
	}
	return sectorID
}

// translateDispositivoWriteError traduce las violaciones de constraints de
// PostgreSQL al error de dominio correspondiente: una FK rota significa que el
// sector no existe (400) y una violación de uq_dispositivos_sector_tipo que el
// sector ya tiene un dispositivo de ese tipo (409).
func translateDispositivoWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgErrCodeForeignKeyViolation:
			return dispositivo.ErrSectorNotFound
		case pgErrCodeUniqueViolation:
			if pgErr.ConstraintName == "uq_dispositivos_sector_tipo" {
				return dispositivo.ErrSectorTipoDuplicado
			}
		}
	}
	return fmt.Errorf("failed to persist dispositivo: %w", err)
}

// Create inserta un dispositivo nuevo en el catálogo. El UUID lo genera
// PostgreSQL vía gen_random_uuid(); el método mapea id y created_at de vuelta
// al struct. Si SectorID/WhepURL/Tipo están vacías se inserta NULL para
// respetar las columnas nullable.
func (r *PostgresDispositivoRepository) Create(ctx context.Context, d *dispositivo.Dispositivo) error {
	const query = `
		INSERT INTO dispositivos (nombre, sector_id, whep_url, tipo)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`

	var whepURL *string
	if d.WhepURL != "" {
		whepURL = &d.WhepURL
	}
	var tipo *string
	if d.Tipo != nil && *d.Tipo != "" {
		tipo = d.Tipo
	}

	err := r.db.QueryRow(ctx, query, d.Nombre, normalizeSectorID(d.SectorID), whepURL, tipo).Scan(&d.ID, &d.CreatedAt)
	if err != nil {
		return translateDispositivoWriteError(err)
	}
	return nil
}

// Update modifica nombre, sector y whep_url de un dispositivo existente. El
// tipo es inmutable: no forma parte del SET. El método mapea id y created_at de
// vuelta al struct vía RETURNING. Si SectorID/WhepURL están vacías se guarda
// NULL para respetar la columna nullable. Devuelve
// dispositivo.ErrDispositivoNotFound si el dispositivo no existe,
// dispositivo.ErrSectorNotFound si el sector no existe y
// dispositivo.ErrSectorTipoDuplicado si el sector ya tiene ese tipo.
func (r *PostgresDispositivoRepository) Update(ctx context.Context, d *dispositivo.Dispositivo) error {
	const query = `
		UPDATE dispositivos
		SET nombre = $1, sector_id = $2, whep_url = $3
		WHERE id = $4
		RETURNING id, created_at`

	var whepURL *string
	if d.WhepURL != "" {
		whepURL = &d.WhepURL
	}

	err := r.db.QueryRow(ctx, query, d.Nombre, normalizeSectorID(d.SectorID), whepURL, d.ID).Scan(&d.ID, &d.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dispositivo.ErrDispositivoNotFound
		}
		return translateDispositivoWriteError(err)
	}
	return nil
}

// Revoke da de baja lógica un dispositivo: revoca su credencial (secret_hash a
// NULL + auth_status='revoked'), lo desasigna del sector y deja traza de
// auditoría con el actor resuelto por email. No borra la fila para no violar las
// FKs ON DELETE RESTRICT (auditoría, lotes, historial de consignas/eventos) ni
// perder el historial de telemetría. Es idempotente: repetir sobre un
// dispositivo ya revocado devuelve nil y no duplica la traza de auditoría.
// Devuelve dispositivo.ErrDispositivoNotFound si el dispositivo no existe (o el
// id no es un UUID válido).
func (r *PostgresDispositivoRepository) Revoke(ctx context.Context, actorEmail, id string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("revoke dispositivo: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var oldStatus string
	err = tx.QueryRow(ctx, `SELECT auth_status FROM dispositivos WHERE id=$1 FOR UPDATE`, id).Scan(&oldStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return dispositivo.ErrDispositivoNotFound
	}
	if err != nil {
		// Un id que no es UUID válido produce 22P02; se traduce a "no encontrado".
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeInvalidTextRepresentation {
			return dispositivo.ErrDispositivoNotFound
		}
		return fmt.Errorf("revoke dispositivo: load: %w", err)
	}

	// El actor debe existir y estar activo; sin traza fiable no se revoca.
	var actorID string
	if err := tx.QueryRow(ctx, `SELECT id FROM usuarios WHERE email=$1 AND activo=true`, actorEmail).Scan(&actorID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("revoke dispositivo: actor not found")
		}
		return fmt.Errorf("revoke dispositivo: actor: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE dispositivos
		SET auth_status = 'revoked',
			secret_hash = NULL,
			sector_id = NULL,
			auth_updated_at = clock_timestamp()
		WHERE id = $1`, id); err != nil {
		return fmt.Errorf("revoke dispositivo: update: %w", err)
	}

	// Idempotencia: si ya estaba revocado, se evita duplicar la traza.
	if oldStatus != string(dispositivo.AuthRevoked) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO device_lifecycle_audit (actor_id, action, dispositivo_id, old_status, new_status)
			VALUES ($1, 'revoke', $2, $3, 'revoked')`, actorID, id, oldStatus); err != nil {
			return fmt.Errorf("revoke dispositivo: audit: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("revoke dispositivo: commit: %w", err)
	}
	return nil
}

// InsertMetrica inserta un registro de telemetría en el historial append-only.
func (r *PostgresDispositivoRepository) InsertMetrica(ctx context.Context, m *dispositivo.MetricaDispositivo) error {
	const query = `
		INSERT INTO metricas_dispositivo (
			dispositivo_id, cpu_pct, mem_ram_disponible_mb, mem_ram_total_mb,
			almacenamiento_disponible_mb, almacenamiento_total_mb,
			temp_chip, ai_processor_pct, received_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`

	err := r.db.QueryRow(ctx, query,
		m.DispositivoID,
		m.CpuPct,
		m.MemRamDisponibleMb,
		m.MemRamTotalMb,
		m.AlmacenamientoDisponibleMb,
		m.AlmacenamientoTotalMb,
		m.TempChip,
		m.AiProcessorPct,
		m.ReceivedAt,
	).Scan(&m.ID)
	if err != nil {
		return fmt.Errorf("failed to insert metrica_dispositivo: %w", err)
	}
	return nil
}

// GetDispositivosConUltimaMetrica devuelve todos los dispositivos del catálogo
// junto con su última métrica registrada (si existe), usando DISTINCT ON para
// obtener la fila más reciente de metricas_dispositivo por dispositivo.
func (r *PostgresDispositivoRepository) GetDispositivosConUltimaMetrica(ctx context.Context) ([]dispositivo.DispositivoConUltimaMetrica, error) {
	rows, err := r.db.Query(ctx, `
		SELECT DISTINCT ON (d.id)
			d.id, d.nombre, d.sector_id, d.whep_url, d.created_at, d.tipo,
			m.id, m.cpu_pct, m.mem_ram_disponible_mb, m.mem_ram_total_mb,
			m.almacenamiento_disponible_mb, m.almacenamiento_total_mb,
			m.temp_chip, m.ai_processor_pct, m.received_at
		FROM dispositivos d
		LEFT JOIN metricas_dispositivo m ON m.dispositivo_id = d.id
		WHERE d.auth_status <> 'revoked'
		ORDER BY d.id, m.received_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query dispositivos con ultima metrica: %w", err)
	}
	defer rows.Close()

	items := []dispositivo.DispositivoConUltimaMetrica{}
	for rows.Next() {
		var (
			d                        dispositivo.Dispositivo
			whepURL                  *string
			tipo                     *string
			m                        dispositivo.MetricaDispositivo
			mID                      *string
			cpu                      *float64
			mem                      *float64
			memTotal                 *float64
			almacenamientoDisponible *float64
			almacenamientoTotal      *float64
			tempChip                 *float64
			aiProc                   *float64
			received                 *time.Time
		)
		if err := rows.Scan(&d.ID, &d.Nombre, &d.SectorID, &whepURL, &d.CreatedAt, &tipo, &mID, &cpu, &mem, &memTotal, &almacenamientoDisponible, &almacenamientoTotal, &tempChip, &aiProc, &received); err != nil {
			return nil, fmt.Errorf("failed to scan dispositivo con ultima metrica: %w", err)
		}
		if whepURL != nil {
			d.WhepURL = *whepURL
		}
		d.Tipo = tipo

		item := dispositivo.DispositivoConUltimaMetrica{Dispositivo: d}
		if mID != nil {
			m.ID = *mID
			m.DispositivoID = d.ID
			m.CpuPct = *cpu
			m.MemRamDisponibleMb = *mem
			m.MemRamTotalMb = memTotal
			m.AlmacenamientoDisponibleMb = almacenamientoDisponible
			m.AlmacenamientoTotalMb = almacenamientoTotal
			m.TempChip = *tempChip
			m.AiProcessorPct = *aiProc
			m.ReceivedAt = *received
			item.UltimaMetrica = &m
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return items, nil
}

// GetDispositivoByID busca un dispositivo por su ID en el catálogo.
// Devuelve dispositivo.ErrDispositivoNotFound si no existe.
func (r *PostgresDispositivoRepository) GetDispositivoByID(ctx context.Context, id string) (*dispositivo.Dispositivo, error) {
	var d dispositivo.Dispositivo
	var whepURL *string
	var tipo *string
	err := r.db.QueryRow(ctx, `
		SELECT id, nombre, sector_id, whep_url, created_at, tipo
		FROM dispositivos
		WHERE id = $1 AND auth_status <> 'revoked'
	`, id).Scan(&d.ID, &d.Nombre, &d.SectorID, &whepURL, &d.CreatedAt, &tipo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, dispositivo.ErrDispositivoNotFound
		}
		// Un id que no es UUID válido produce 22P02; se traduce a "no encontrado"
		// en vez de escalar como 500.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeInvalidTextRepresentation {
			return nil, dispositivo.ErrDispositivoNotFound
		}
		return nil, fmt.Errorf("failed to query dispositivo by id: %w", err)
	}
	if whepURL != nil {
		d.WhepURL = *whepURL
	}
	d.Tipo = tipo

	return &d, nil
}

// GetMetricasByDispositivo devuelve una página del historial de métricas de un
// dispositivo, ordenadas por received_at descendente.
func (r *PostgresDispositivoRepository) GetMetricasByDispositivo(ctx context.Context, dispositivoID string, page, pageSize int) (*dispositivo.PaginatedResult, error) {
	var total int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM metricas_dispositivo
		WHERE dispositivo_id = $1
	`, dispositivoID).Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count metricas_dispositivo: %w", err)
	}

	offset := (page - 1) * pageSize

	rows, err := r.db.Query(ctx, `
		SELECT id, dispositivo_id, cpu_pct, mem_ram_disponible_mb, mem_ram_total_mb,
			almacenamiento_disponible_mb, almacenamiento_total_mb,
			temp_chip, ai_processor_pct, received_at
		FROM metricas_dispositivo
		WHERE dispositivo_id = $1
		ORDER BY received_at DESC
		LIMIT $2 OFFSET $3
	`, dispositivoID, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query metricas_dispositivo: %w", err)
	}
	defer rows.Close()

	items := []dispositivo.MetricaDispositivo{}
	for rows.Next() {
		var (
			m                        dispositivo.MetricaDispositivo
			memTotal                 *float64
			almacenamientoDisponible *float64
			almacenamientoTotal      *float64
		)
		if err := rows.Scan(
			&m.ID, &m.DispositivoID, &m.CpuPct, &m.MemRamDisponibleMb,
			&memTotal, &almacenamientoDisponible, &almacenamientoTotal,
			&m.TempChip, &m.AiProcessorPct, &m.ReceivedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan metrica_dispositivo: %w", err)
		}
		m.MemRamTotalMb = memTotal
		m.AlmacenamientoDisponibleMb = almacenamientoDisponible
		m.AlmacenamientoTotalMb = almacenamientoTotal
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return &dispositivo.PaginatedResult{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// ListDeviceReads devuelve el catálogo con el estado de registro de cada
// dispositivo (auth_status, si tiene secret y cuándo se actualizó). El estado de
// salud se inicializa como offline: el caché en memoria lo hidrata luego.
func (r *PostgresDispositivoRepository) ListDeviceReads(ctx context.Context) ([]dispositivo.DeviceRead, error) {
	rows, err := r.db.Query(ctx, `
		SELECT d.id, d.nombre, COALESCE(d.whep_url, ''),
			d.tipo, d.sector_id, d.auth_status, (d.secret_hash IS NOT NULL), d.auth_updated_at
		FROM dispositivos d
		WHERE d.auth_status <> 'revoked'
		ORDER BY d.nombre
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query device reads: %w", err)
	}
	defer rows.Close()

	out := []dispositivo.DeviceRead{}
	for rows.Next() {
		var (
			x       dispositivo.DeviceRead
			tipo    *string
			status  string
			updated *time.Time
		)
		if err := rows.Scan(&x.DispositivoID, &x.Nombre, &x.WhepURL, &tipo, &x.SectorID, &status, &x.HasSecret, &updated); err != nil {
			return nil, fmt.Errorf("failed to scan device read: %w", err)
		}
		x.Tipo = tipo
		x.EstadoDispositivo.Tipo = tipo
		x.AuthStatus = dispositivo.AuthStatus(status)
		x.AuthUpdatedAt = updated
		x.Estado = dispositivo.EstadoOffline
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return out, nil
}
