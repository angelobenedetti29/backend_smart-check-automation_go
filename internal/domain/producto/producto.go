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
// Desde SCA-400 el producto admite dos zonas térmicas: temp_min/temp_max son
// siempre del horno 1, y temp2_min/temp2_max (nullable) son del horno 2
// cuando la línea tiene un segundo horno en serie (horno2_habilitado=true).
// delta_temp1_max_c, delta_temp2_max_c (este último opcional) y delta_vel_max
// son las variaciones máximas admitidas por ciclo del optimizador MPC.
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
	// TempSetpoint y VelocidadCintaSetpoint son opcionales (nullable): valor
	// puntual dentro del rango min/max, sólo usado si se reactiva el despacho
	// automático de consigna (SCA-142).
	TempSetpoint           *float64  `json:"tempSetpoint,omitempty"`
	VelocidadCintaSetpoint *float64  `json:"velocidadCintaSetpoint,omitempty"`
	// Horno 2 (opcional): si Horno2Habilitado=true, Temp2Min/Temp2Max/DeltaTemp2MaxC
	// deben estar definidos. Temp2Setpoint es opcional pero, si está, cae en el rango.
	Horno2Habilitado bool     `json:"horno2Habilitado"`
	Temp2Min         *float64 `json:"temp2Min,omitempty"`
	Temp2Max         *float64 `json:"temp2Max,omitempty"`
	Temp2Setpoint    *float64 `json:"temp2Setpoint,omitempty"`
	DeltaTemp1MaxC   float64  `json:"deltaTemp1MaxC"`
	DeltaTemp2MaxC   *float64 `json:"deltaTemp2MaxC,omitempty"`
	DeltaVelMax      float64  `json:"deltaVelMax"`
	Activo           bool     `json:"activo"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
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
	// Setpoints opcionales: si se envían, deben caer dentro del rango min/max.
	TempSetpoint           *float64 `json:"tempSetpoint,omitempty"`
	VelocidadCintaSetpoint *float64 `json:"velocidadCintaSetpoint,omitempty"`
	// Horno 2 (opcional). Default false en backend; los nil quedan como NULL en la BD.
	Horno2Habilitado *bool    `json:"horno2Habilitado,omitempty"`
	Temp2Min         *float64 `json:"temp2Min,omitempty"`
	Temp2Max         *float64 `json:"temp2Max,omitempty"`
	Temp2Setpoint    *float64 `json:"temp2Setpoint,omitempty"`
	DeltaTemp1MaxC   *float64 `json:"deltaTemp1MaxC,omitempty"` // default 10.0 si nil
	DeltaTemp2MaxC   *float64 `json:"deltaTemp2MaxC,omitempty"`
	DeltaVelMax      *float64 `json:"deltaVelMax,omitempty"` // default 0.05 si nil
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
	if req.TempSetpoint != nil && (*req.TempSetpoint < req.TempMin || *req.TempSetpoint > req.TempMax) {
		fields["tempSetpoint"] = "debe estar entre tempMin y tempMax"
	}
	if req.VelocidadCintaSetpoint != nil && (*req.VelocidadCintaSetpoint < req.VelocidadCintaMin || *req.VelocidadCintaSetpoint > req.VelocidadCintaMax) {
		fields["velocidadCintaSetpoint"] = "debe estar entre velocidadCintaMin y velocidadCintaMax"
	}

	// Horno 2: validar consistencia del trío min/max/flag. Los defaults del
	// flag se aplican en el service (Create/Update) antes de persistir.
	if req.Horno2Habilitado != nil && *req.Horno2Habilitado {
		if req.Temp2Min == nil || req.Temp2Max == nil {
			fields["horno2Habilitado"] = "habilitar el horno 2 requiere definir temp2Min y temp2Max"
		} else if *req.Temp2Max <= *req.Temp2Min {
			fields["temp2Max"] = fmt.Sprintf("debe ser mayor a temp2Min (%.2f)", *req.Temp2Min)
		}
		if req.DeltaTemp2MaxC == nil {
			fields["deltaTemp2MaxC"] = "es requerido cuando horno2Habilitado=true"
		}
	} else {
		// horno2Habilitado=false: temp2_min/max no deben estar definidos.
		if req.Temp2Min != nil || req.Temp2Max != nil {
			fields["horno2Habilitado"] = "temp2Min/temp2Max requieren horno2Habilitado=true"
		}
	}
	if req.Temp2Setpoint != nil {
		if req.Temp2Min == nil || req.Temp2Max == nil {
			fields["temp2Setpoint"] = "requiere temp2Min y temp2Max"
		} else if *req.Temp2Setpoint < *req.Temp2Min || *req.Temp2Setpoint > *req.Temp2Max {
			fields["temp2Setpoint"] = "debe estar entre temp2Min y temp2Max"
		}
	}
	if req.DeltaTemp1MaxC != nil && *req.DeltaTemp1MaxC <= 0 {
		fields["deltaTemp1MaxC"] = "debe ser mayor a 0"
	}
	if req.DeltaTemp2MaxC != nil && *req.DeltaTemp2MaxC <= 0 {
		fields["deltaTemp2MaxC"] = "debe ser mayor a 0"
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
