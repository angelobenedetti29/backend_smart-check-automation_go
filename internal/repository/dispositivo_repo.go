package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

// Create inserta un dispositivo nuevo en el catálogo. El UUID lo genera
// PostgreSQL vía gen_random_uuid(); el método mapea id y created_at de vuelta
// al struct. Si Ubicacion/WhepURL/Tipo están vacías se inserta NULL para
// respetar las columnas nullable.
func (r *PostgresDispositivoRepository) Create(ctx context.Context, d *dispositivo.Dispositivo) error {
	const query = `
		INSERT INTO dispositivos (nombre, ubicacion, whep_url, tipo)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`

	var ubicacion *string
	if d.Ubicacion != "" {
		ubicacion = &d.Ubicacion
	}
	var whepURL *string
	if d.WhepURL != "" {
		whepURL = &d.WhepURL
	}
	var tipo *string
	if d.Tipo != nil && *d.Tipo != "" {
		tipo = d.Tipo
	}

	err := r.db.QueryRow(ctx, query, d.Nombre, ubicacion, whepURL, tipo).Scan(&d.ID, &d.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert dispositivo: %w", err)
	}
	return nil
}

// Update modifica nombre, ubicación y whep_url de un dispositivo existente. El
// tipo es inmutable: no forma parte del SET. El método mapea id y created_at de
// vuelta al struct vía RETURNING. Si Ubicacion/WhepURL están vacías se guarda
// NULL para respetar la columna nullable. Devuelve
// dispositivo.ErrDispositivoNotFound si el dispositivo no existe.
func (r *PostgresDispositivoRepository) Update(ctx context.Context, d *dispositivo.Dispositivo) error {
	const query = `
		UPDATE dispositivos
		SET nombre = $1, ubicacion = $2, whep_url = $3
		WHERE id = $4
		RETURNING id, created_at`

	var ubicacion *string
	if d.Ubicacion != "" {
		ubicacion = &d.Ubicacion
	}
	var whepURL *string
	if d.WhepURL != "" {
		whepURL = &d.WhepURL
	}

	err := r.db.QueryRow(ctx, query, d.Nombre, ubicacion, whepURL, d.ID).Scan(&d.ID, &d.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dispositivo.ErrDispositivoNotFound
		}
		return fmt.Errorf("failed to update dispositivo: %w", err)
	}
	return nil
}

// Delete elimina un dispositivo del catálogo. Las métricas asociadas se borran
// por ON DELETE CASCADE de metricas_dispositivo. Devuelve
// dispositivo.ErrDispositivoNotFound si el dispositivo no existe.
func (r *PostgresDispositivoRepository) Delete(ctx context.Context, id string) error {
	cmd, err := r.db.Exec(ctx, `DELETE FROM dispositivos WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete dispositivo: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return dispositivo.ErrDispositivoNotFound
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
			d.id, d.nombre, COALESCE(d.ubicacion, ''), d.whep_url, d.created_at, d.tipo,
			m.id, m.cpu_pct, m.mem_ram_disponible_mb, m.mem_ram_total_mb,
			m.almacenamiento_disponible_mb, m.almacenamiento_total_mb,
			m.temp_chip, m.ai_processor_pct, m.received_at
		FROM dispositivos d
		LEFT JOIN metricas_dispositivo m ON m.dispositivo_id = d.id
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
		if err := rows.Scan(&d.ID, &d.Nombre, &d.Ubicacion, &whepURL, &d.CreatedAt, &tipo, &mID, &cpu, &mem, &memTotal, &almacenamientoDisponible, &almacenamientoTotal, &tempChip, &aiProc, &received); err != nil {
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
		SELECT id, nombre, COALESCE(ubicacion, ''), whep_url, created_at, tipo
		FROM dispositivos
		WHERE id = $1
	`, id).Scan(&d.ID, &d.Nombre, &d.Ubicacion, &whepURL, &d.CreatedAt, &tipo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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
		SELECT d.id, d.nombre, COALESCE(d.ubicacion, ''), COALESCE(d.whep_url, ''),
			d.tipo, d.sector_id, d.auth_status, (d.secret_hash IS NOT NULL), d.auth_updated_at
		FROM dispositivos d
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
		if err := rows.Scan(&x.DispositivoID, &x.Nombre, &x.Ubicacion, &x.WhepURL, &tipo, &x.SectorID, &status, &x.HasSecret, &updated); err != nil {
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
