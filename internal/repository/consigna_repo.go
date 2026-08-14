package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
)

// ConsignaPostgresRepository implementa consigna.Repository usando pgxpool.
// Persiste el historial de auditoría de consignas despachadas (o intentadas)
// al controlador físico del horno, tanto de origen automático como manual.
type ConsignaPostgresRepository struct {
	db *pgxpool.Pool
}

// NewConsignaPostgresRepository crea un repositorio real conectado a PostgreSQL.
func NewConsignaPostgresRepository(db *pgxpool.Pool) *ConsignaPostgresRepository {
	return &ConsignaPostgresRepository{db: db}
}

const selectHistorialConsignasColumns = `
	id, horno_id, lote_id, producto_id,
	temperatura_objetivo, velocidad_cinta_objetivo,
	origen, usuario, exitosa, motivo_error,
	temperatura_previa, velocidad_cinta_previa, creada_en`

// Save inserta un nuevo registro de auditoría en historial_consignas.
// Devuelve consigna.ErrParametrosNoExiste si producto_id no existe en el catálogo
// (violación de FK), aunque en la práctica el Service ya valida su existencia antes.
func (r *ConsignaPostgresRepository) Save(ctx context.Context, c *consigna.Consigna) error {
	const query = `
		INSERT INTO historial_consignas (
			horno_id, lote_id, producto_id,
			temperatura_objetivo, velocidad_cinta_objetivo,
			origen, usuario, exitosa, motivo_error,
			temperatura_previa, velocidad_cinta_previa
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)
		RETURNING id, creada_en`

	err := r.db.QueryRow(ctx, query,
		c.HornoID,
		c.LoteID,
		c.ProductoID,
		c.TemperaturaObjetivo,
		c.VelocidadCintaObjetivo,
		c.Origen,
		c.Usuario,
		c.Exitosa,
		c.MotivoError,
		c.TemperaturaPrevia,
		c.VelocidadCintaPrevia,
	).Scan(&c.ID, &c.CreadaEn)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeForeignKeyViolation {
			return consigna.ErrParametrosNoExiste
		}
		return fmt.Errorf("failed to insert historial_consignas: %w", err)
	}
	return nil
}

// GetByLoteID devuelve el historial de consignas de un lote, ordenado del más reciente al más antiguo.
func (r *ConsignaPostgresRepository) GetByLoteID(ctx context.Context, loteID string) ([]consigna.Consigna, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+selectHistorialConsignasColumns+`
		FROM historial_consignas
		WHERE lote_id = $1
		ORDER BY creada_en DESC
	`, loteID)
	if err != nil {
		return nil, fmt.Errorf("failed to query historial_consignas by lote_id: %w", err)
	}
	defer rows.Close()

	return scanHistorialConsignas(rows)
}

// GetByHornoID devuelve el historial de consignas de un horno, ordenado del más reciente al más antiguo, hasta limit filas.
func (r *ConsignaPostgresRepository) GetByHornoID(ctx context.Context, hornoID string, limit int) ([]consigna.Consigna, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := r.db.Query(ctx, `
		SELECT `+selectHistorialConsignasColumns+`
		FROM historial_consignas
		WHERE horno_id = $1
		ORDER BY creada_en DESC
		LIMIT $2
	`, hornoID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query historial_consignas by horno_id: %w", err)
	}
	defer rows.Close()

	return scanHistorialConsignas(rows)
}

// scanHistorialConsignas escanea las filas resultantes de una consulta sobre historial_consignas.
func scanHistorialConsignas(rows pgx.Rows) ([]consigna.Consigna, error) {
	items := []consigna.Consigna{}
	for rows.Next() {
		var c consigna.Consigna
		if err := rows.Scan(
			&c.ID, &c.HornoID, &c.LoteID, &c.ProductoID,
			&c.TemperaturaObjetivo, &c.VelocidadCintaObjetivo,
			&c.Origen, &c.Usuario, &c.Exitosa, &c.MotivoError,
			&c.TemperaturaPrevia, &c.VelocidadCintaPrevia, &c.CreadaEn,
		); err != nil {
			return nil, fmt.Errorf("failed to scan historial_consignas: %w", err)
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return items, nil
}
