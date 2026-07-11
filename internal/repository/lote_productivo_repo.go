package repository

import (
	"context"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5/pgxpool"

	lote "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_productivo"
)

// LoteProductivoPostgresRepository implementa lote_productivo.Repository usando pgxpool.
type LoteProductivoPostgresRepository struct {
	db *pgxpool.Pool
}

// NewLoteProductivoPostgresRepository crea un repositorio real conectado a PostgreSQL.
func NewLoteProductivoPostgresRepository(db *pgxpool.Pool) *LoteProductivoPostgresRepository {
	return &LoteProductivoPostgresRepository{db: db}
}

// GetAll devuelve una página de lotes productivos ordenados por inicio_at descendente.
func (r *LoteProductivoPostgresRepository) GetAll(page, pageSize int) (*lote.PaginatedResult, error) {
	ctx := context.Background()

	var total int
	if err := r.db.QueryRow(ctx, "SELECT COUNT(*) FROM lotes_productivos").Scan(&total); err != nil {
		return nil, fmt.Errorf("failed to count lotes: %w", err)
	}

	offset := (page - 1) * pageSize
	totalPages := int(math.Ceil(float64(total) / float64(pageSize)))
	if total == 0 {
		totalPages = 0
	}

	rows, err := r.db.Query(ctx, `
		SELECT lp.id, lp.producto_id, pr.nombre AS producto_nombre, lp.turno, lp.inicio_at, lp.fin_at,
		       lp.total_unidades, lp.correctos, lp.quemados,
		       lp.correctos_kg, lp.quemados_kg,
		       lp.temp_horno_1, lp.temp_comb_horno_1,
		       lp.temp_horno_2, lp.temp_comb_horno_2,
		       lp.velocidad_horno, lp.created_at, lp.updated_at
		FROM lotes_productivos lp
		JOIN productos pr ON lp.producto_id = pr.id
		ORDER BY lp.inicio_at DESC
		LIMIT $1 OFFSET $2
	`, pageSize, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query lotes: %w", err)
	}
	defer rows.Close()

	items := []lote.LoteProductivo{}
	for rows.Next() {
		var l lote.LoteProductivo
		if err := rows.Scan(
			&l.ID, &l.ProductoID, &l.ProductoNombre, &l.Turno, &l.InicioAt, &l.FinAt,
			&l.TotalUnidades, &l.Correctos, &l.Quemados,
			&l.CorrectosKg, &l.QuemadosKg,
			&l.TempHorno1, &l.TempCombHorno1,
			&l.TempHorno2, &l.TempCombHorno2,
			&l.VelocidadHorno, &l.CreatedAt, &l.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan lote: %w", err)
		}
		items = append(items, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return &lote.PaginatedResult{
		Items:      items,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

// GetByID returns a single LoteProductivo with its producto_nombre via JOIN.
func (r *LoteProductivoPostgresRepository) GetByID(id string) (*lote.LoteProductivo, error) {
	ctx := context.Background()

	var l lote.LoteProductivo
	err := r.db.QueryRow(ctx, `
		SELECT lp.id, lp.producto_id, pr.nombre AS producto_nombre, lp.turno, lp.inicio_at, lp.fin_at,
		       lp.total_unidades, lp.correctos, lp.quemados,
		       lp.correctos_kg, lp.quemados_kg,
		       lp.temp_horno_1, lp.temp_comb_horno_1,
		       lp.temp_horno_2, lp.temp_comb_horno_2,
		       lp.velocidad_horno, lp.created_at, lp.updated_at
		FROM lotes_productivos lp
		JOIN productos pr ON lp.producto_id = pr.id
		WHERE lp.id = $1
	`, id).Scan(
		&l.ID, &l.ProductoID, &l.ProductoNombre, &l.Turno, &l.InicioAt, &l.FinAt,
		&l.TotalUnidades, &l.Correctos, &l.Quemados,
		&l.CorrectosKg, &l.QuemadosKg,
		&l.TempHorno1, &l.TempCombHorno1,
		&l.TempHorno2, &l.TempCombHorno2,
		&l.VelocidadHorno, &l.CreatedAt, &l.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get lote by id %s: %w", id, err)
	}

	return &l, nil
}
