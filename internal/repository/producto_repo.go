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

// List devuelve todo el catálogo ordenado por nombre, incluyendo los productos
// inactivos (activo=false): el filtrado por vigencia lo decide el consumidor.
func (r *ProductoPostgresRepository) List(ctx context.Context) ([]producto.Producto, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, nombre, activo
		FROM productos
		ORDER BY nombre`)
	if err != nil {
		return nil, fmt.Errorf("failed to query productos: %w", err)
	}
	defer rows.Close()

	out := []producto.Producto{}
	for rows.Next() {
		var p producto.Producto
		if err := rows.Scan(&p.ID, &p.Nombre, &p.Activo); err != nil {
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
	err := r.db.QueryRow(ctx, `SELECT id, nombre, activo FROM productos WHERE id=$1`, id).
		Scan(&p.ID, &p.Nombre, &p.Activo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, producto.ErrDesconocido
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeInvalidTextRepresentation {
		return nil, producto.ErrDesconocido
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query producto by id: %w", err)
	}
	return &p, nil
}
