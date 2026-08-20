package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	parametrosproducto "github.com/angelobenedetti29/smart-check-automation/internal/domain/parametros_producto"
)

// Códigos de error de PostgreSQL relevantes (ver https://www.postgresql.org/docs/current/errcodes-appendix.html)
const (
	pgErrCodeUniqueViolation     = "23505"
	pgErrCodeForeignKeyViolation = "23503"
)

// ParametrosProductoPostgresRepository implementa parametros_producto.Repository usando pgxpool.
type ParametrosProductoPostgresRepository struct {
	db *pgxpool.Pool
}

// NewParametrosProductoPostgresRepository crea un repositorio real conectado a PostgreSQL.
func NewParametrosProductoPostgresRepository(db *pgxpool.Pool) *ParametrosProductoPostgresRepository {
	return &ParametrosProductoPostgresRepository{db: db}
}

const selectParametrosProductoColumns = `
	pp.id, pp.producto_id, pr.nombre AS producto_nombre,
	pp.peso_referencia_kg, pp.tolerancia_peso_pct,
	pp.dimension_base_cm, pp.tolerancia_dimension_cm,
	pp.temp_min, pp.temp_max,
	pp.velocidad_cinta_min, pp.velocidad_cinta_max,
	pp.temp_setpoint, pp.velocidad_cinta_setpoint,
	pp.costo_unitario,
	pp.activo, pp.created_at, pp.updated_at`

// GetAll devuelve todos los sets de parámetros configurados, ordenados por nombre de producto.
func (r *ParametrosProductoPostgresRepository) GetAll(ctx context.Context) ([]parametrosproducto.ParametroProducto, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+selectParametrosProductoColumns+`
		FROM parametros_producto pp
		JOIN productos pr ON pp.producto_id = pr.id
		ORDER BY pr.nombre ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query parametros_producto: %w", err)
	}
	defer rows.Close()

	items := []parametrosproducto.ParametroProducto{}
	for rows.Next() {
		var p parametrosproducto.ParametroProducto
		if err := rows.Scan(
			&p.ID, &p.ProductoID, &p.ProductoNombre,
			&p.PesoReferenciaKg, &p.ToleranciaPesoPct,
			&p.DimensionBaseCm, &p.ToleranciaDimensionCm,
			&p.TempMin, &p.TempMax,
			&p.VelocidadCintaMin, &p.VelocidadCintaMax,
			&p.TempSetpoint, &p.VelocidadCintaSetpoint,
			&p.CostoUnitario,
			&p.Activo, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan parametro_producto: %w", err)
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return items, nil
}

// GetByProductoID busca el set de parámetros vigente para un producto específico.
// Devuelve parametros_producto.ErrNotFound si no existe ningún registro.
func (r *ParametrosProductoPostgresRepository) GetByProductoID(ctx context.Context, productoID string) (*parametrosproducto.ParametroProducto, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+selectParametrosProductoColumns+`
		FROM parametros_producto pp
		JOIN productos pr ON pp.producto_id = pr.id
		WHERE pp.producto_id = $1
	`, productoID)

	var p parametrosproducto.ParametroProducto
	err := row.Scan(
		&p.ID, &p.ProductoID, &p.ProductoNombre,
		&p.PesoReferenciaKg, &p.ToleranciaPesoPct,
		&p.DimensionBaseCm, &p.ToleranciaDimensionCm,
		&p.TempMin, &p.TempMax,
		&p.VelocidadCintaMin, &p.VelocidadCintaMax,
		&p.TempSetpoint, &p.VelocidadCintaSetpoint,
		&p.CostoUnitario,
		&p.Activo, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, parametrosproducto.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query parametro_producto by producto_id: %w", err)
	}

	return &p, nil
}

// Create inserta un nuevo set de parámetros para un producto.
// Devuelve parametros_producto.ErrProductoNoExiste si producto_id no existe en el catálogo,
// o parametros_producto.ErrYaExiste si el producto ya tiene un set de parámetros cargado.
func (r *ParametrosProductoPostgresRepository) Create(ctx context.Context, p *parametrosproducto.ParametroProducto) error {
	const query = `
		WITH inserted AS (
			INSERT INTO parametros_producto (
				producto_id, peso_referencia_kg, tolerancia_peso_pct,
				dimension_base_cm, tolerancia_dimension_cm,
				temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max,
				costo_unitario, activo
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
			)
			RETURNING *
		)
		SELECT inserted.id, productos.nombre, inserted.created_at, inserted.updated_at
		FROM inserted
		JOIN productos ON productos.id = inserted.producto_id`

	err := r.db.QueryRow(ctx, query,
		p.ProductoID,
		p.PesoReferenciaKg,
		p.ToleranciaPesoPct,
		p.DimensionBaseCm,
		p.ToleranciaDimensionCm,
		p.TempMin,
		p.TempMax,
		p.VelocidadCintaMin,
		p.VelocidadCintaMax,
		p.CostoUnitario,
		p.Activo,
	).Scan(&p.ID, &p.ProductoNombre, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case pgErrCodeUniqueViolation:
				return parametrosproducto.ErrYaExiste
			case pgErrCodeForeignKeyViolation:
				return parametrosproducto.ErrProductoNoExiste
			}
		}
		return fmt.Errorf("failed to insert parametro_producto: %w", err)
	}
	return nil
}

// Update modifica el set de parámetros existente para el producto_id indicado.
// Devuelve parametros_producto.ErrNotFound si no existe un registro para ese producto.
func (r *ParametrosProductoPostgresRepository) Update(ctx context.Context, p *parametrosproducto.ParametroProducto) error {
	const query = `
		UPDATE parametros_producto SET
			peso_referencia_kg = $2,
			tolerancia_peso_pct = $3,
			dimension_base_cm = $4,
			tolerancia_dimension_cm = $5,
			temp_min = $6,
			temp_max = $7,
			velocidad_cinta_min = $8,
			velocidad_cinta_max = $9,
			costo_unitario = COALESCE($10, parametros_producto.costo_unitario),
			updated_at = now()
		FROM productos
		WHERE parametros_producto.producto_id = $1
		  AND productos.id = parametros_producto.producto_id
		RETURNING parametros_producto.id, productos.nombre, parametros_producto.activo,
		          parametros_producto.created_at, parametros_producto.updated_at`

	err := r.db.QueryRow(ctx, query,
		p.ProductoID,
		p.PesoReferenciaKg,
		p.ToleranciaPesoPct,
		p.DimensionBaseCm,
		p.ToleranciaDimensionCm,
		p.TempMin,
		p.TempMax,
		p.VelocidadCintaMin,
		p.VelocidadCintaMax,
		p.CostoUnitario,
	).Scan(&p.ID, &p.ProductoNombre, &p.Activo, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return parametrosproducto.ErrNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return fmt.Errorf("parametros_producto: violación de constraint %s: %w", pgErr.Code, err)
		}
		return fmt.Errorf("failed to update parametro_producto: %w", err)
	}
	return nil
}
