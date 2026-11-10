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
	"sort"
	"strings"
	"time"
)

// ErrDesconocido se retorna cuando el producto_id referenciado no existe en el
// catálogo. Comparar con errors.Is() en el service y el handler.
var ErrDesconocido = errors.New("producto: el producto referenciado no existe")

// ErrDuplicado se retorna cuando ya existe otro producto con el mismo nombre
// (colisión de la restricción UNIQUE, comparación case-insensitive).
var ErrDuplicado = errors.New("producto: ya existe un producto con ese nombre")

// ValidationError agrupa los errores de validación de un ProductoRequest,
// indexados por nombre de campo para que el frontend pueda mapearlos directo.
type ValidationError struct {
	Fields map[string]string
}

// Error devuelve los errores de validación en formato legible y determinista
// (campos ordenados alfabéticamente), apto para logging.
func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", k, e.Fields[k]))
	}
	return fmt.Sprintf("errores de validación: %s", strings.Join(parts, "; "))
}

// Producto es una entrada del catálogo maestro. Activo=false indica un producto
// retirado: no se ofrece para elegir, pero sigue resolviendo lotes históricos.
// Los campos numéricos son los parámetros de control ideales de horneado.
//
// temp_min/temp_max son del horno; delta_temp1_max_c y delta_vel_max son las
// variaciones máximas admitidas por ciclo del optimizador MPC.
type Producto struct {
	ID                    string  `json:"id"`
	Nombre                string  `json:"nombre"`
	PesoReferenciaKg      float64 `json:"pesoReferenciaKg"`
	ToleranciaPesoPct     float64 `json:"toleranciaPesoPct"`
	DimensionBaseCm       float64 `json:"dimensionBaseCm"`
	ToleranciaDimensionCm float64 `json:"toleranciaDimensionCm"`
	TempMin               float64 `json:"tempMin"`
	TempMax               float64 `json:"tempMax"`
	VelocidadCintaMin     float64 `json:"velocidadCintaMin"`
	VelocidadCintaMax     float64 `json:"velocidadCintaMax"`
	// CapacidadNominalKgH es la capacidad nominal de producción en kg/h.
	// Nullable: si no está cargada, el producto no participa de cálculos de
	// capacidad/OEE.
	CapacidadNominalKgH *float64 `json:"capacidadNominalKgH"`
	// TempSetpoint y VelocidadCintaSetpoint son opcionales (nullable): valor
	// puntual dentro del rango min/max, sólo usado si se reactiva el despacho
	// automático de consigna (SCA-142).
	TempSetpoint           *float64  `json:"tempSetpoint,omitempty"`
	VelocidadCintaSetpoint *float64  `json:"velocidadCintaSetpoint,omitempty"`
	DeltaTemp1MaxC         float64   `json:"deltaTemp1MaxC"`
	DeltaVelMax            float64   `json:"deltaVelMax"`
	Activo                 bool      `json:"activo"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
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
	// CapacidadNominalKgH es opcional (nullable) y, si viene, debe ser > 0.
	CapacidadNominalKgH *float64 `json:"capacidadNominalKgH"`
	// Setpoints opcionales: si se envían, deben caer dentro del rango min/max.
	TempSetpoint           *float64 `json:"tempSetpoint,omitempty"`
	VelocidadCintaSetpoint *float64 `json:"velocidadCintaSetpoint,omitempty"`
	DeltaTemp1MaxC         *float64 `json:"deltaTemp1MaxC,omitempty"` // default 10.0 si nil
	DeltaVelMax            *float64 `json:"deltaVelMax,omitempty"`    // default 0.05 si nil
}

// Validate verifica todas las reglas de negocio del ProductoRequest, replicando
// en el servidor los CHECK constraints de la tabla productos. Retorna un
// *ValidationError con el mapa campo→mensaje de todos los campos inválidos, o
// nil si es válido.
func (req ProductoRequest) Validate() error {
	fields := map[string]string{}

	nombre := strings.TrimSpace(req.Nombre)
	if nombre == "" {
		fields["nombre"] = "es requerido"
	} else if len([]rune(nombre)) > 100 {
		fields["nombre"] = "no puede superar los 100 caracteres"
	}

	if req.PesoReferenciaKg <= 0 {
		fields["pesoReferenciaKg"] = "debe ser un valor numérico mayor a 0"
	}
	if req.ToleranciaPesoPct < 0 {
		fields["toleranciaPesoPct"] = "no puede ser negativo"
	}
	if req.DimensionBaseCm <= 0 {
		fields["dimensionBaseCm"] = "debe ser un valor numérico mayor a 0"
	}
	if req.ToleranciaDimensionCm < 0 {
		fields["toleranciaDimensionCm"] = "no puede ser negativo"
	}
	if req.TempMax <= req.TempMin {
		fields["tempMax"] = fmt.Sprintf("debe ser mayor a tempMin (%.2f)", req.TempMin)
	}
	if req.VelocidadCintaMax <= req.VelocidadCintaMin {
		fields["velocidadCintaMax"] = fmt.Sprintf("debe ser mayor a velocidadCintaMin (%.2f)", req.VelocidadCintaMin)
	}
	if req.CapacidadNominalKgH != nil && *req.CapacidadNominalKgH <= 0 {
		fields["capacidadNominalKgH"] = "debe ser un valor numérico mayor a 0"
	}
	if req.TempSetpoint != nil && (*req.TempSetpoint < req.TempMin || *req.TempSetpoint > req.TempMax) {
		fields["tempSetpoint"] = "debe estar entre tempMin y tempMax"
	}
	if req.VelocidadCintaSetpoint != nil && (*req.VelocidadCintaSetpoint < req.VelocidadCintaMin || *req.VelocidadCintaSetpoint > req.VelocidadCintaMax) {
		fields["velocidadCintaSetpoint"] = "debe estar entre velocidadCintaMin y velocidadCintaMax"
	}

	if req.DeltaTemp1MaxC != nil && *req.DeltaTemp1MaxC <= 0 {
		fields["deltaTemp1MaxC"] = "debe ser mayor a 0"
	}
	if req.DeltaVelMax != nil && *req.DeltaVelMax <= 0 {
		fields["deltaVelMax"] = "debe ser mayor a 0"
	}

	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
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
