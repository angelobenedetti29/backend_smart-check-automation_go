package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	producto "github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
)

// ProductoPostgresRepository implementa producto.Repository usando pgxpool.
type ProductoPostgresRepository struct {
	db *pgxpool.Pool
}

// NewProductoPostgresRepository crea un repositorio real del catálogo de productos.
func NewProductoPostgresRepository(db *pgxpool.Pool) *ProductoPostgresRepository {
	return &ProductoPostgresRepository{db: db}
}

const selectProductoColumns = `
	id, nombre,
	peso_referencia_kg, tolerancia_peso_pct,
	dimension_base_cm, tolerancia_dimension_cm,
	temp_min, temp_max,
	velocidad_cinta_min, velocidad_cinta_max,
	temp_setpoint, velocidad_cinta_setpoint,
	activo, created_at, updated_at`

// scanProducto escanea las columnas de selectProductoColumns en un Producto.
func scanProducto(row pgx.Row, p *producto.Producto) error {
	return row.Scan(
		&p.ID, &p.Nombre,
		&p.PesoReferenciaKg, &p.ToleranciaPesoPct,
		&p.DimensionBaseCm, &p.ToleranciaDimensionCm,
		&p.TempMin, &p.TempMax,
		&p.VelocidadCintaMin, &p.VelocidadCintaMax,
		&p.TempSetpoint, &p.VelocidadCintaSetpoint,
		&p.Activo, &p.CreatedAt, &p.UpdatedAt,
	)
}

// List devuelve todo el catálogo ordenado por nombre, incluyendo los productos
// inactivos (activo=false): el filtrado por vigencia lo decide el consumidor.
func (r *ProductoPostgresRepository) List(ctx context.Context) ([]producto.Producto, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+selectProductoColumns+`
		FROM productos
		ORDER BY nombre`)
	if err != nil {
		return nil, fmt.Errorf("failed to query productos: %w", err)
	}
	defer rows.Close()

	out := []producto.Producto{}
	for rows.Next() {
		var p producto.Producto
		if err := scanProducto(rows, &p); err != nil {
			return nil, fmt.Errorf("failed to scan producto: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}
	return out, nil
}

// GetByID busca un producto por su id. Devuelve producto.ErrDesconocido tanto
// si el id no existe como si no es un UUID válido.
func (r *ProductoPostgresRepository) GetByID(ctx context.Context, id string) (*producto.Producto, error) {
	var p producto.Producto
	err := scanProducto(r.db.QueryRow(ctx, `SELECT `+selectProductoColumns+` FROM productos WHERE id=$1`, id), &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, producto.ErrDesconocido
	}
	if isInvalidTextRepresentation(err) {
		return nil, producto.ErrDesconocido
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query producto by id: %w", err)
	}
	return &p, nil
}

// ExistsByNombre indica si ya hay un producto con ese nombre (comparación
// case-insensitive), excluyendo el id indicado. exceptoID se compara como texto
// para tolerar ids inválidos sin error de cast.
func (r *ProductoPostgresRepository) ExistsByNombre(ctx context.Context, nombre string, exceptoID string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM productos
			WHERE LOWER(nombre) = LOWER($1)
			  AND ($2 = '' OR id::text <> $2)
		)`, nombre, exceptoID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check producto name uniqueness: %w", err)
	}
	return exists, nil
}

// Create inserta un producto nuevo en el catálogo. Devuelve producto.ErrDuplicado
// si el nombre colisiona con otro producto.
func (r *ProductoPostgresRepository) Create(ctx context.Context, p *producto.Producto) (*producto.Producto, error) {
	const query = `
		INSERT INTO productos (
			nombre, peso_referencia_kg, tolerancia_peso_pct,
			dimension_base_cm, tolerancia_dimension_cm,
			temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max,
			temp_setpoint, velocidad_cinta_setpoint
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, temp_setpoint, velocidad_cinta_setpoint, created_at, updated_at, activo`

	err := r.db.QueryRow(ctx, query,
		p.Nombre,
		p.PesoReferenciaKg,
		p.ToleranciaPesoPct,
		p.DimensionBaseCm,
		p.ToleranciaDimensionCm,
		p.TempMin,
		p.TempMax,
		p.VelocidadCintaMin,
		p.VelocidadCintaMax,
		p.TempSetpoint,
		p.VelocidadCintaSetpoint,
	).Scan(&p.ID, &p.TempSetpoint, &p.VelocidadCintaSetpoint, &p.CreatedAt, &p.UpdatedAt, &p.Activo)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, producto.ErrDuplicado
		}
		return nil, fmt.Errorf("failed to insert producto: %w", err)
	}
	return p, nil
}

// Update modifica un producto existente. Devuelve producto.ErrDuplicado si el
// nombre colisiona con otro producto, y producto.ErrDesconocido si el id no existe.
func (r *ProductoPostgresRepository) Update(ctx context.Context, p *producto.Producto) (*producto.Producto, error) {
	const query = `
		UPDATE productos SET
			nombre = $2,
			peso_referencia_kg = $3,
			tolerancia_peso_pct = $4,
			dimension_base_cm = $5,
			tolerancia_dimension_cm = $6,
			temp_min = $7,
			temp_max = $8,
			velocidad_cinta_min = $9,
			velocidad_cinta_max = $10,
			temp_setpoint = $11,
			velocidad_cinta_setpoint = $12,
			updated_at = now()
		WHERE id = $1
		RETURNING id, nombre,
			peso_referencia_kg, tolerancia_peso_pct,
			dimension_base_cm, tolerancia_dimension_cm,
			temp_min, temp_max,
			velocidad_cinta_min, velocidad_cinta_max,
			temp_setpoint, velocidad_cinta_setpoint,
			activo, created_at, updated_at`

	err := scanProducto(r.db.QueryRow(ctx, query,
		p.ID,
		p.Nombre,
		p.PesoReferenciaKg,
		p.ToleranciaPesoPct,
		p.DimensionBaseCm,
		p.ToleranciaDimensionCm,
		p.TempMin,
		p.TempMax,
		p.VelocidadCintaMin,
		p.VelocidadCintaMax,
		p.TempSetpoint,
		p.VelocidadCintaSetpoint,
	), p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, producto.ErrDesconocido
	}
	if isUniqueViolation(err) {
		return nil, producto.ErrDuplicado
	}
	if isInvalidTextRepresentation(err) {
		return nil, producto.ErrDesconocido
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update producto: %w", err)
	}
	return p, nil
}

// Deactivate aplica la baja lógica de un producto (activo=false). Devuelve
// producto.ErrDesconocido si el id no existe o no es un UUID válido.
func (r *ProductoPostgresRepository) Deactivate(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `UPDATE productos SET activo=false, updated_at=now() WHERE id=$1`, id)
	if err != nil {
		if isInvalidTextRepresentation(err) {
			return producto.ErrDesconocido
		}
		return fmt.Errorf("failed to deactivate producto: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return producto.ErrDesconocido
	}
	return nil
}

// isUniqueViolation indica si el error es una violación de restricción UNIQUE de PostgreSQL.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgErrCodeUniqueViolation
}
