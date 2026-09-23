// Package producto define el modelo de dominio del catálogo de productos y su
// contrato de persistencia. El catálogo es propiedad del backend: la Raspberry
// lo consulta para vincular sus detecciones y el panel para poblarlos selectores.
package producto

import (
	"context"
	"errors"
)

// ErrDesconocido se retorna cuando el producto_id referenciado no existe en el
// catálogo. Comparar con errors.Is() en el service y el handler.
var ErrDesconocido = errors.New("producto: el producto referenciado no existe")

// Producto es una entrada del catálogo maestro. Activo=false indica un producto
// retirado: no se ofrece para elegir, pero sigue resolviendo lotes históricos.
type Producto struct {
	ID     string `json:"id"`
	Nombre string `json:"nombre"`
	Activo bool   `json:"activo"`
}

// Repository define el contrato de persistencia del catálogo de productos.
// La implementación real vive en internal/repository/.
type Repository interface {
	// List devuelve todo el catálogo ordenado por nombre, incluidos los
	// productos inactivos (el filtrado por vigencia lo decide el consumidor).
	List(ctx context.Context) ([]Producto, error)
	// GetByID busca un producto por id o devuelve ErrDesconocido si no existe.
	GetByID(ctx context.Context, id string) (*Producto, error)
}
