package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	sector "github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
)

// pgErrCodeInvalidTextRepresentation es el código de PostgreSQL para input
// inválido (p. ej. un UUID malformado contra una columna uuid). Compartido por
// los repositorios que traducen ese 22P02 a un error de dominio.
const pgErrCodeInvalidTextRepresentation = "22P02"

// SectorPostgresRepository implementa sector.Repository usando pgxpool.
type SectorPostgresRepository struct {
	db *pgxpool.Pool
}

// NewSectorPostgresRepository crea un repositorio real de sectores.
func NewSectorPostgresRepository(db *pgxpool.Pool) *SectorPostgresRepository {
	return &SectorPostgresRepository{db: db}
}

// GetDeviceInfo resuelve id, nombre, tipo y sector del dispositivo. SectorID
// queda nil cuando la columna es NULL. Devuelve sector.ErrSinSector si el
// dispositivo no existe o si el id no es un UUID válido.
func (r *SectorPostgresRepository) GetDeviceInfo(ctx context.Context, deviceID string) (*sector.DeviceInfo, error) {
	var (
		info     sector.DeviceInfo
		tipo     *string
		sectorID *string
	)
	err := r.db.QueryRow(ctx, `
		SELECT id, nombre, tipo, sector_id
		FROM dispositivos
		WHERE id=$1`, deviceID).Scan(&info.DeviceID, &info.Hostname, &tipo, &sectorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, sector.ErrSinSector
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeInvalidTextRepresentation {
		return nil, sector.ErrSinSector
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query device info: %w", err)
	}
	if tipo != nil {
		info.Tipo = *tipo
	}
	info.SectorID = sectorID
	return &info, nil
}

// GetByID busca un sector por su id. Devuelve sector.ErrSinSector si no existe.
func (r *SectorPostgresRepository) GetByID(ctx context.Context, sectorID string) (*sector.Sector, error) {
	var s sector.Sector
	err := r.db.QueryRow(ctx, `SELECT id, nombre FROM sectores WHERE id=$1`, sectorID).
		Scan(&s.ID, &s.Nombre)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, sector.ErrSinSector
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query sector by id: %w", err)
	}
	return &s, nil
}

// List devuelve todos los sectores ordenados por nombre.
func (r *SectorPostgresRepository) List(ctx context.Context) ([]sector.Sector, error) {
	rows, err := r.db.Query(ctx, `SELECT id, nombre FROM sectores ORDER BY nombre`)
	if err != nil {
		return nil, fmt.Errorf("failed to query sectores: %w", err)
	}
	defer rows.Close()

	out := []sector.Sector{}
	for rows.Next() {
		var s sector.Sector
		if err := rows.Scan(&s.ID, &s.Nombre); err != nil {
			return nil, fmt.Errorf("failed to scan sector: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return out, nil
}

// Create inserta un sector nuevo. Devuelve sector.ErrSectorIDExists si el id ya
// está ocupado por otro sector (violación de la clave primaria).
func (r *SectorPostgresRepository) Create(ctx context.Context, s *sector.Sector) error {
	_, err := r.db.Exec(ctx, `INSERT INTO sectores (id, nombre) VALUES ($1, $2)`, s.ID, s.Nombre)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeUniqueViolation {
			return sector.ErrSectorIDExists
		}
		return fmt.Errorf("failed to insert sector: %w", err)
	}
	return nil
}

// Update modifica el nombre de un sector existente. Devuelve
// sector.ErrSectorNotFound si el sector no existe.
func (r *SectorPostgresRepository) Update(ctx context.Context, s *sector.Sector) error {
	cmd, err := r.db.Exec(ctx, `UPDATE sectores SET nombre = $1 WHERE id = $2`, s.Nombre, s.ID)
	if err != nil {
		return fmt.Errorf("failed to update sector: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return sector.ErrSectorNotFound
	}
	return nil
}

// Delete elimina un sector siempre que no tenga lotes productivos asociados.
// La verificación y el borrado van en una sola sentencia para evitar carreras;
// si el sector no existe devuelve ErrSectorNotFound y si todavía tiene lotes
// devuelve ErrSectorConLotes.
func (r *SectorPostgresRepository) Delete(ctx context.Context, id string) error {
	cmd, err := r.db.Exec(ctx, `
		DELETE FROM sectores s
		WHERE s.id = $1
		  AND NOT EXISTS (SELECT 1 FROM lotes_productivos lp WHERE lp.sector_id = s.id)`, id)
	if err != nil {
		// La FK ON DELETE RESTRICT también protege ante una carrera: si el
		// sector empezó a tener lotes entre el chequeo y el borrado, se traduce
		// igual al error de conflicto.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeForeignKeyViolation {
			return sector.ErrSectorConLotes
		}
		return fmt.Errorf("failed to delete sector: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		var exists bool
		if err := r.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sectores WHERE id = $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("failed to check sector existence: %w", err)
		}
		if !exists {
			return sector.ErrSectorNotFound
		}
		return sector.ErrSectorConLotes
	}
	return nil
}

// ListCompaneros devuelve los demás dispositivos del sector, excluyendo al
// dispositivo indicado. tipo queda "" cuando la columna es NULL.
func (r *SectorPostgresRepository) ListCompaneros(ctx context.Context, sectorID, excludeDeviceID string) ([]sector.Companero, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, nombre, tipo
		FROM dispositivos
		WHERE sector_id=$1 AND id <> $2
		ORDER BY nombre`, sectorID, excludeDeviceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query companeros: %w", err)
	}
	defer rows.Close()

	out := []sector.Companero{}
	for rows.Next() {
		var (
			c    sector.Companero
			tipo *string
		)
		if err := rows.Scan(&c.DeviceID, &c.Hostname, &tipo); err != nil {
			return nil, fmt.Errorf("failed to scan companero: %w", err)
		}
		if tipo != nil {
			c.Tipo = *tipo
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return out, nil
}
