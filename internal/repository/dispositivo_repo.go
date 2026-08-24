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
// al struct. Si Ubicacion está vacía se inserta NULL para respetar la columna
// nullable.
func (r *PostgresDispositivoRepository) Create(ctx context.Context, d *dispositivo.Dispositivo) error {
	const query = `
		INSERT INTO dispositivos (nombre, ubicacion)
		VALUES ($1, $2)
		RETURNING id, created_at`

	var ubicacion *string
	if d.Ubicacion != "" {
		ubicacion = &d.Ubicacion
	}

	err := r.db.QueryRow(ctx, query, d.Nombre, ubicacion).Scan(&d.ID, &d.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert dispositivo: %w", err)
	}
	return nil
}

// Update modifica nombre y ubicación de un dispositivo existente. El método
// mapea id y created_at de vuelta al struct vía RETURNING. Si Ubicacion está
// vacía se guarda NULL para respetar la columna nullable. Devuelve
// dispositivo.ErrDispositivoNotFound si el dispositivo no existe.
func (r *PostgresDispositivoRepository) Update(ctx context.Context, d *dispositivo.Dispositivo) error {
	const query = `
		UPDATE dispositivos
		SET nombre = $1, ubicacion = $2
		WHERE id = $3
		RETURNING id, created_at`

	var ubicacion *string
	if d.Ubicacion != "" {
		ubicacion = &d.Ubicacion
	}

	err := r.db.QueryRow(ctx, query, d.Nombre, ubicacion, d.ID).Scan(&d.ID, &d.CreatedAt)
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
			d.id, d.nombre, d.ubicacion, d.created_at,
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
		if err := rows.Scan(&d.ID, &d.Nombre, &d.Ubicacion, &d.CreatedAt, &mID, &cpu, &mem, &memTotal, &almacenamientoDisponible, &almacenamientoTotal, &tempChip, &aiProc, &received); err != nil {
			return nil, fmt.Errorf("failed to scan dispositivo con ultima metrica: %w", err)
		}

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
	err := r.db.QueryRow(ctx, `
		SELECT id, nombre, ubicacion, created_at
		FROM dispositivos
		WHERE id = $1
	`, id).Scan(&d.ID, &d.Nombre, &d.Ubicacion, &d.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, dispositivo.ErrDispositivoNotFound
		}
		return nil, fmt.Errorf("failed to query dispositivo by id: %w", err)
	}

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
