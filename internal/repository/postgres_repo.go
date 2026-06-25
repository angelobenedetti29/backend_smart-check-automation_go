package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote"
)

// PostgresRepository implements lote.Repository using a pgxpool connection pool.
type PostgresRepository struct {
	db *pgxpool.Pool
}

// NewPostgresRepository creates a new repository backed by the given pool.
func NewPostgresRepository(db *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// Create inserts a new productive batch into the lotes_productivos table.
// If the Lote already carries an ID it is used directly; otherwise
// PostgreSQL generates one via gen_random_uuid().
func (r *PostgresRepository) Create(ctx context.Context, l *lote.Lote) error {
	const query = `
		INSERT INTO lotes_productivos (
			id, producto_id, turno, inicio_at, fin_at,
			total_unidades, correctos, quemados, crudas,
			correctos_kg, quemados_kg, crudos_kg,
			temp_horno_1, temp_comb_horno_1,
			temp_horno_2, temp_comb_horno_2,
			velocidad_cinta,
			created_at, updated_at
		) VALUES (
			CASE WHEN $1 = '' THEN gen_random_uuid() ELSE $1::uuid END,
			$2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12,
			$13, $14,
			$15, $16,
			$17,
			$18, $19
		)
		RETURNING id`

	err := r.db.QueryRow(ctx, query,
		l.ID,
		l.ProductoID,
		l.Turno,
		l.InicioAt,
		l.FinAt,
		l.TotalUnidades,
		l.Correctos,
		l.Quemados,
		l.Crudas,
		l.CorrectosKg,
		l.QuemadosKg,
		l.CrudosKg,
		l.TempHorno1,
		l.TempCombHorno1,
		l.TempHorno2,
		l.TempCombHorno2,
		l.VelocidadCinta,
		l.CreatedAt,
		l.UpdatedAt,
	).Scan(&l.ID)
	if err != nil {
		return fmt.Errorf("failed to insert lote: %w", err)
	}
	return nil
}
