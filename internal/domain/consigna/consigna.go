package consigna

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrHornoNoExiste indica que el horno referenciado no existe.
var ErrHornoNoExiste = errors.New("consigna: el horno referenciado no existe")

// ErrParametrosNoExiste indica que el producto no tiene parámetros de control
// cargados, o que le faltan los setpoints puntuales (temp_setpoint/velocidad_cinta_setpoint)
// necesarios para un despacho automático.
var ErrParametrosNoExiste = errors.New("consigna: no hay parámetros de control (o setpoints) cargados para el producto")

// ErrFueraDeRango indica que los valores solicitados en un despacho manual
// están fuera del rango seguro definido para el producto.
var ErrFueraDeRango = errors.New("consigna: valores fuera de rango seguro")

// ErrDispatchFallido indica que el controlador físico del horno rechazó o no
// pudo aplicar la consigna.
var ErrDispatchFallido = errors.New("consigna: el controlador físico rechazó la consigna")

// Origen indica si la consigna fue disparada automáticamente por el sistema
// (tras la detección de IA al iniciar un lote) o manualmente por un operario
// desde el panel de control.
type Origen string

const (
	OrigenAutomatico Origen = "AUTOMATICO"
	OrigenManual     Origen = "MANUAL"
)

// ValidationError agrupa todos los errores de validación de un ConsignaManualRequest.
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

// Consigna representa un registro histórico de auditoría: una consigna
// térmica y de velocidad de cinta que fue (o se intentó) despachar al
// controlador físico del horno, tal como se persiste en PostgreSQL.
type Consigna struct {
	ID                     string    `json:"id"                        db:"id"`
	HornoID                string    `json:"hornoId"                   db:"horno_id"`
	LoteID                 *string   `json:"loteId,omitempty"          db:"lote_id"`
	ProductoID             *string   `json:"productoId,omitempty"      db:"producto_id"`
	TemperaturaObjetivo    float64   `json:"temperaturaObjetivo"       db:"temperatura_objetivo"`
	VelocidadCintaObjetivo float64   `json:"velocidadCintaObjetivo"    db:"velocidad_cinta_objetivo"`
	Origen                 Origen    `json:"origen"                    db:"origen"`
	Usuario                *string   `json:"usuario,omitempty"         db:"usuario"` // TODO(OAuth): reemplazar por identidad real cuando exista login de usuarios
	Exitosa                bool      `json:"exitosa"                   db:"exitosa"`
	MotivoError            *string   `json:"motivoError,omitempty"     db:"motivo_error"`
	TemperaturaPrevia      *float64  `json:"temperaturaPrevia,omitempty" db:"temperatura_previa"`
	VelocidadCintaPrevia   *float64  `json:"velocidadCintaPrevia,omitempty" db:"velocidad_cinta_previa"`
	CreadaEn               time.Time `json:"creadaEn"                  db:"creada_en"`
}

// ConsignaManualRequest es el payload JSON entrante del panel web para el
// envío manual de consigna térmica (SCA-320). ProductoID es obligatorio: el
// despacho manual siempre valida contra el rango real cargado en
// parametros_producto, sin caer a límites de seguridad hardcodeados.
type ConsignaManualRequest struct {
	HornoID                string  `json:"hornoId"`
	ProductoID             string  `json:"productoId"`
	TemperaturaObjetivo    float64 `json:"temperaturaObjetivo"`
	VelocidadCintaObjetivo float64 `json:"velocidadCintaObjetivo"`
	Usuario                string  `json:"usuario,omitempty"` // TODO(OAuth): tomar de la sesión/JWT una vez exista auth de usuarios
	// LoteID es opcional: permite correlacionar un ajuste manual con el lote
	// en curso (por ejemplo, el loteId de correlación devuelto por
	// POST /api/v1/lotes/inicio) para que aparezca en su historial de
	// auditoría. Si se omite, la consigna manual queda auditada sin lote asociado.
	LoteID *string `json:"loteId,omitempty"`
}

// Validate aplica las reglas de forma básicas del request (presencia, valores
// positivos). El rango seguro específico por producto se valida en el
// Service, porque depende de datos de parametros_producto, no es una regla
// de forma pura de dominio.
func (r ConsignaManualRequest) Validate() error {
	var errs []string

	if strings.TrimSpace(r.HornoID) == "" {
		errs = append(errs, "hornoId: es requerido")
	}
	if strings.TrimSpace(r.ProductoID) == "" {
		errs = append(errs, "productoId: es requerido")
	}
	if r.TemperaturaObjetivo <= 0 {
		errs = append(errs, "temperaturaObjetivo: debe ser un valor numérico mayor a 0")
	}
	if r.VelocidadCintaObjetivo <= 0 {
		errs = append(errs, "velocidadCintaObjetivo: debe ser un valor numérico mayor a 0")
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}

// Repository define el contrato de persistencia del historial de auditoría
// de consignas despachadas al horno.
type Repository interface {
	Save(ctx context.Context, c *Consigna) error
	GetByLoteID(ctx context.Context, loteID string) ([]Consigna, error)
	GetByHornoID(ctx context.Context, hornoID string, limit int) ([]Consigna, error)
}

// Service orquesta la resolución de la consigna, el despacho al controlador
// físico simulado, la actualización del estado activo del horno y el
// registro de auditoría. Es el mecanismo compartido por el envío automático
// (SCA-142) y el envío manual (SCA-320).
type Service interface {
	// DispatchAutomatico resuelve el setpoint puntual cargado en
	// parametros_producto para productoID y lo despacha automáticamente.
	// loteID es un identificador de correlación (puede no corresponder aún a
	// una fila persistida en lotes_productivos).
	DispatchAutomatico(ctx context.Context, hornoID, loteID, productoID string) (*Consigna, error)

	// DispatchManual valida el request contra el rango seguro del producto y,
	// si es válido, despacha la consigna solicitada por el operario.
	DispatchManual(ctx context.Context, req ConsignaManualRequest) (*Consigna, error)

	// GetHistorialByLote devuelve el historial de auditoría de consignas de un lote.
	GetHistorialByLote(ctx context.Context, loteID string) ([]Consigna, error)
}
