// Package producto define el modelo de dominio del catálogo de productos y su
// contrato de persistencia. El catálogo es propiedad del backend: la Raspberry
// lo consulta para vincular sus detecciones y el panel para poblarlos selectores.
// Desde la unificación, cada producto lleva embebidos sus parámetros de control
// (antes en la tabla parametros_producto).
package producto

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrDesconocido se retorna cuando el producto_id referenciado no existe en el
// catálogo. Comparar con errors.Is() en el service y el handler.
var ErrDesconocido = errors.New("producto: el producto referenciado no existe")

// ErrDuplicado se retorna cuando ya existe otro producto con el mismo nombre
// (colisión de la restricción UNIQUE, comparación case-insensitive).
var ErrDuplicado = errors.New("producto: ya existe un producto con ese nombre")

// ValidationError agrupa todos los errores de validación de un ProductoRequest.
type ValidationError struct {
	Fields []string
}

// Error devuelve el listado de errores de validación concatenado.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("errores de validación: %s", strings.Join(e.Fields, "; "))
}

// Producto es una entrada del catálogo maestro. Activo=false indica un producto
// retirado: no se ofrece para elegir, pero sigue resolviendo lotes históricos.
// Los campos numéricos son los parámetros de control ideales de horneado.
type Producto struct {
	ID                    string    `json:"id"`
	Nombre                string    `json:"nombre"`
	PesoReferenciaKg      float64   `json:"pesoReferenciaKg"`
	ToleranciaPesoPct     float64   `json:"toleranciaPesoPct"`
	DimensionBaseCm       float64   `json:"dimensionBaseCm"`
	ToleranciaDimensionCm float64   `json:"toleranciaDimensionCm"`
	TempMin               float64   `json:"tempMin"`
	TempMax               float64   `json:"tempMax"`
	VelocidadCintaMin     float64   `json:"velocidadCintaMin"`
	VelocidadCintaMax     float64   `json:"velocidadCintaMax"`
	Activo                bool      `json:"activo"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

// ProductoRequest representa el payload JSON entrante para el alta (POST) y la
// modificación (PUT) de un producto del catálogo. El id lo asigna el servidor,
// por lo que no forma parte del body.
type ProductoRequest struct {
	Nombre                string  `json:"nombre"`
	PesoReferenciaKg      float64 `json:"pesoReferenciaKg"`
	ToleranciaPesoPct     float64 `json:"toleranciaPesoPct"`
	DimensionBaseCm       float64 `json:"dimensionBaseCm"`
	ToleranciaDimensionCm float64 `json:"toleranciaDimensionCm"`
	TempMin               float64 `json:"tempMin"`
	TempMax               float64 `json:"tempMax"`
	VelocidadCintaMin     float64 `json:"velocidadCintaMin"`
	VelocidadCintaMax     float64 `json:"velocidadCintaMax"`
}

// Validate verifica todas las reglas de negocio del ProductoRequest, replicando
// en el servidor los CHECK constraints de la tabla productos. Retorna un
// *ValidationError con el listado completo de campos inválidos, o nil si es válido.
func (req ProductoRequest) Validate() error {
	var errs []string

	nombre := strings.TrimSpace(req.Nombre)
	if nombre == "" {
		errs = append(errs, "nombre: es requerido")
	} else if len([]rune(nombre)) > 100 {
		errs = append(errs, "nombre: no puede superar los 100 caracteres")
	}

	if req.PesoReferenciaKg <= 0 {
		errs = append(errs, "pesoReferenciaKg: debe ser un valor numérico mayor a 0")
	}
	if req.ToleranciaPesoPct < 0 {
		errs = append(errs, "toleranciaPesoPct: no puede ser negativo")
	}
	if req.DimensionBaseCm <= 0 {
		errs = append(errs, "dimensionBaseCm: debe ser un valor numérico mayor a 0")
	}
	if req.ToleranciaDimensionCm < 0 {
		errs = append(errs, "toleranciaDimensionCm: no puede ser negativo")
	}
	if req.TempMax <= req.TempMin {
		errs = append(errs, fmt.Sprintf("tempMax(%.2f): debe ser mayor a tempMin(%.2f)", req.TempMax, req.TempMin))
	}
	if req.VelocidadCintaMax <= req.VelocidadCintaMin {
		errs = append(errs, fmt.Sprintf("velocidadCintaMax(%.2f): debe ser mayor a velocidadCintaMin(%.2f)", req.VelocidadCintaMax, req.VelocidadCintaMin))
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

// Repository define el contrato de persistencia del catálogo de productos.
// La implementación real vive en internal/repository/.
type Repository interface {
	// List devuelve todo el catálogo ordenado por nombre, incluidos los
	// productos inactivos (el filtrado por vigencia lo decide el consumidor).
	List(ctx context.Context) ([]Producto, error)
	// GetByID busca un producto por id o devuelve ErrDesconocido si no existe.
	GetByID(ctx context.Context, id string) (*Producto, error)
	// Create inserta un producto nuevo y devuelve la fila persistida.
	Create(ctx context.Context, p *Producto) (*Producto, error)
	// Update modifica un producto existente. Devuelve ErrDesconocido si el id
	// no existe y ErrDuplicado si el nombre colisiona con otro producto.
	Update(ctx context.Context, p *Producto) (*Producto, error)
	// Deactivate aplica la baja lógica (activo=false). Devuelve ErrDesconocido
	// si el id no existe.
	Deactivate(ctx context.Context, id string) error
	// ExistsByNombre indica si hay otro producto con ese nombre (comparación
	// case-insensitive), excluyendo el id indicado (cadena vacía = ninguno).
	ExistsByNombre(ctx context.Context, nombre string, exceptoID string) (bool, error)
}
