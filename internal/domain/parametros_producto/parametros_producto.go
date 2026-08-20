package parametros_producto

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound indica que no existe un set de parámetros para el producto solicitado.
var ErrNotFound = errors.New("parametros_producto: registro no encontrado")

// ErrProductoNoExiste indica que el producto_id referenciado no existe en el catálogo de productos.
var ErrProductoNoExiste = errors.New("parametros_producto: el producto referenciado no existe")

// ErrYaExiste indica que ya existe un set de parámetros para el producto (violación de UNIQUE).
var ErrYaExiste = errors.New("parametros_producto: ya existe un set de parámetros para este producto")

// ValidationError agrupa todos los errores de validación de un ParametroProductoRequest.
type ValidationError struct {
	Fields []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("errores de validación: %s", strings.Join(e.Fields, "; "))
}

// IsValidationError informa si un error es de tipo ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// ParametroProducto representa los umbrales de control ideales de horneado
// para una variedad de producto, tal como se persisten en PostgreSQL.
type ParametroProducto struct {
	ID                    string  `json:"id"                    db:"id"`
	ProductoID            string  `json:"productoId"            db:"producto_id"`
	ProductoNombre        string  `json:"productoNombre"        db:"producto_nombre"`
	PesoReferenciaKg      float64 `json:"pesoReferenciaKg"      db:"peso_referencia_kg"`
	ToleranciaPesoPct     float64 `json:"toleranciaPesoPct"     db:"tolerancia_peso_pct"`
	DimensionBaseCm       float64 `json:"dimensionBaseCm"       db:"dimension_base_cm"`
	ToleranciaDimensionCm float64 `json:"toleranciaDimensionCm" db:"tolerancia_dimension_cm"`
	TempMin               float64 `json:"tempMin"               db:"temp_min"`
	TempMax               float64 `json:"tempMax"               db:"temp_max"`
	VelocidadCintaMin     float64 `json:"velocidadCintaMin"     db:"velocidad_cinta_min"`
	VelocidadCintaMax     float64 `json:"velocidadCintaMax"     db:"velocidad_cinta_max"`
	// TempSetpoint y VelocidadCintaSetpoint son el valor puntual (dentro del
	// rango min/max) que se despacha al horno en un envío automático de
	// consigna (ver internal/domain/consigna). Nullable: si no están
	// cargados, el producto no admite despacho automático todavía.
	TempSetpoint           *float64  `json:"tempSetpoint,omitempty"           db:"temp_setpoint"`
	VelocidadCintaSetpoint *float64  `json:"velocidadCintaSetpoint,omitempty" db:"velocidad_cinta_setpoint"`
	CostoUnitario          *float64  `json:"costoUnitario,omitempty"          db:"costo_unitario"`
	Activo                 bool      `json:"activo"                db:"activo"`
	CreatedAt              time.Time `json:"createdAt"             db:"created_at"`
	UpdatedAt              time.Time `json:"updatedAt"             db:"updated_at"`
}

// ParametroProductoRequest representa el payload JSON entrante para el alta (POST)
// y la modificación (PUT) de parámetros por producto, provenientes del panel de
// configuración del Supervisor.
type ParametroProductoRequest struct {
	ProductoID            string   `json:"productoId"`
	PesoReferenciaKg      float64  `json:"pesoReferenciaKg"`
	ToleranciaPesoPct     float64  `json:"toleranciaPesoPct"`
	DimensionBaseCm       float64  `json:"dimensionBaseCm"`
	ToleranciaDimensionCm float64  `json:"toleranciaDimensionCm"`
	TempMin               float64  `json:"tempMin"`
	TempMax               float64  `json:"tempMax"`
	VelocidadCintaMin     float64  `json:"velocidadCintaMin"`
	VelocidadCintaMax     float64  `json:"velocidadCintaMax"`
	CostoUnitario          *float64 `json:"costoUnitario,omitempty"`
}

// MapRequestToParametroProducto convierte un ParametroProductoRequest (DTO de API)
// en un ParametroProducto (entidad de dominio), preservando activo=true por defecto.
func MapRequestToParametroProducto(req ParametroProductoRequest) ParametroProducto {
	return ParametroProducto{
		ProductoID:            req.ProductoID,
		PesoReferenciaKg:      req.PesoReferenciaKg,
		ToleranciaPesoPct:     req.ToleranciaPesoPct,
		DimensionBaseCm:       req.DimensionBaseCm,
		ToleranciaDimensionCm: req.ToleranciaDimensionCm,
		TempMin:               req.TempMin,
		TempMax:               req.TempMax,
		VelocidadCintaMin:     req.VelocidadCintaMin,
		VelocidadCintaMax:     req.VelocidadCintaMax,
		CostoUnitario:          req.CostoUnitario,
		Activo:                true,
	}
}

// Validate verifica todas las reglas de negocio del ParametroProductoRequest,
// replicando en el servidor los CHECK constraints de la tabla parametros_producto
// para rechazar valores numéricos inválidos o vacíos antes de llegar a la base de datos.
// Retorna un *ValidationError con el listado completo de campos inválidos, o nil si es válido.
func (req ParametroProductoRequest) Validate() error {
	var errs []string

	// Campo obligatorio: producto_id
	if strings.TrimSpace(req.ProductoID) == "" {
		errs = append(errs, "productoId: es requerido")
	}

	// Peso de referencia debe ser estrictamente positivo
	if req.PesoReferenciaKg <= 0 {
		errs = append(errs, "pesoReferenciaKg: debe ser un valor numérico mayor a 0")
	}

	// Tolerancia de peso no puede ser negativa
	if req.ToleranciaPesoPct < 0 {
		errs = append(errs, "toleranciaPesoPct: no puede ser negativo")
	}

	// Dimensión base debe ser estrictamente positiva
	if req.DimensionBaseCm <= 0 {
		errs = append(errs, "dimensionBaseCm: debe ser un valor numérico mayor a 0")
	}

	// Tolerancia de dimensión no puede ser negativa
	if req.ToleranciaDimensionCm < 0 {
		errs = append(errs, "toleranciaDimensionCm: no puede ser negativo")
	}

	// Consistencia del rango de temperatura: el máximo debe superar al mínimo
	if req.TempMax <= req.TempMin {
		errs = append(errs, fmt.Sprintf("tempMax(%.2f): debe ser mayor a tempMin(%.2f)", req.TempMax, req.TempMin))
	}

	// Consistencia del rango de velocidad de cinta: el máximo debe superar al mínimo
	if req.VelocidadCintaMax <= req.VelocidadCintaMin {
		errs = append(errs, fmt.Sprintf("velocidadCintaMax(%.2f): debe ser mayor a velocidadCintaMin(%.2f)", req.VelocidadCintaMax, req.VelocidadCintaMin))
	}

	// Costo unitario no puede ser negativo si se provee
	if req.CostoUnitario != nil && *req.CostoUnitario < 0 {
		errs = append(errs, fmt.Sprintf("costoUnitario(%.2f): no puede ser negativo", *req.CostoUnitario))
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

// Repository define el contrato de persistencia para ParametroProducto.
type Repository interface {
	GetAll(ctx context.Context) ([]ParametroProducto, error)
	GetByProductoID(ctx context.Context, productoID string) (*ParametroProducto, error)
	Create(ctx context.Context, p *ParametroProducto) error
	Update(ctx context.Context, p *ParametroProducto) error
}

// Service define las operaciones de negocio para parámetros por producto.
type Service interface {
	GetAll(ctx context.Context) ([]ParametroProducto, error)
	Create(ctx context.Context, req ParametroProductoRequest) (*ParametroProducto, error)
	Update(ctx context.Context, req ParametroProductoRequest) (*ParametroProducto, error)
}
