package consigna

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrLoteRequerido indica que una consigna manual no trae lote.
var ErrLoteRequerido = errors.New("consigna: el lote es requerido")

// ErrLoteNoExiste indica que el lote referenciado no existe.
var ErrLoteNoExiste = errors.New("consigna: el lote referenciado no existe")

// ErrLoteNoAbierto indica que el lote no está ABIERTO.
var ErrLoteNoAbierto = errors.New("consigna: el lote no está abierto")

// ErrLoteNoManual indica que el lote no está en modo MANUAL.
var ErrLoteNoManual = errors.New("consigna: el lote no está en modo MANUAL")

// ErrEncoladoFallido indica que no se pudo dejar la consigna en la cola que
// consume la Pi ENTRADA.
var ErrEncoladoFallido = errors.New("consigna: no se pudo encolar la consigna para la Pi")

// ErrParametrosNoExiste indica que el producto no tiene parámetros de control
// cargados, necesarios para validar una consigna manual contra su rango seguro.
var ErrParametrosNoExiste = errors.New("consigna: no hay parámetros de control (o setpoints) cargados para el producto")

// ErrFueraDeRango indica que los valores solicitados en un despacho manual
// están fuera del rango seguro definido para el producto.
var ErrFueraDeRango = errors.New("consigna: valores fuera de rango seguro")

// ErrSectorRequerido indica que el lote no tiene sector asignado.
var ErrSectorRequerido = errors.New("consigna: el sector/horno es requerido")

// ErrSectorMismatch indica que el sector enviado no coincide con el sector del
// lote referenciado.
var ErrSectorMismatch = errors.New("consigna: el sector no coincide con el del lote")

// Origen identifica el mecanismo que disparó la consigna. Sólo MANUAL se
// escribe hoy: la consigna manual se encola para la Pi. AUTOMATICO se conserva
// únicamente para leer filas históricas; ya no se genera.
type Origen string

const (
	OrigenManual     Origen = "MANUAL"
	OrigenAutomatico Origen = "AUTOMATICO"
)

// Estados del ciclo de vida de una consigna encolada para la Pi. La única
// transición a APLICADA la produce el ack de la Pi.
const (
	EstadoConsignaPendiente = "PENDIENTE"
	EstadoConsignaAplicada  = "APLICADA"
	EstadoConsignaRechazada = "RECHAZADA"
)

// ValidationError agrupa todos los errores de validación de un ConsignaManualRequest.
type ValidationError struct {
	Fields []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("errores de validación: %s", strings.Join(e.Fields, "; "))
}

// Consigna representa un registro histórico de auditoría: una consigna
// térmica y de velocidad de cinta que fue aceptada y encolada para la Pi
// ENTRADA, tal como se persiste en PostgreSQL.
type Consigna struct {
	ID                     string  `json:"id"                        db:"id"`
	DispositivoID          *string `json:"dispositivoId,omitempty"   db:"dispositivo_id"`
	HornoID                string  `json:"hornoId"                   db:"horno_id"`
	LoteID                 *string `json:"loteId,omitempty"          db:"lote_id"`
	ProductoID             *string `json:"productoId,omitempty"      db:"producto_id"`
	TemperaturaObjetivo    float64 `json:"temperaturaObjetivo"       db:"temperatura_objetivo"`
	VelocidadCintaObjetivo float64 `json:"velocidadCintaObjetivo"    db:"velocidad_cinta_objetivo"`
	Origen                 Origen  `json:"origen"                    db:"origen"`
	Usuario                *string `json:"usuario,omitempty"         db:"usuario"` // operario que envió la consigna manual (opcional)
	// Exitosa se conserva por compatibilidad con el contrato histórico
	// (true = aceptada y encolada). La fuente fina del ciclo de vida es Estado.
	Exitosa bool `json:"exitosa"                   db:"exitosa"`
	// DecisionID es la decisión de control encolada para la Pi; el ack la marca
	// APLICADA. Null cuando no se pudo encolar.
	DecisionID           *string   `json:"decisionId,omitempty"      db:"decision_id"`
	Estado               string    `json:"estado"                    db:"estado"`
	MotivoError          *string   `json:"motivoError,omitempty"     db:"motivo_error"`
	TemperaturaPrevia    *float64  `json:"temperaturaPrevia,omitempty" db:"temperatura_previa"`
	VelocidadCintaPrevia *float64  `json:"velocidadCintaPrevia,omitempty" db:"velocidad_cinta_previa"`
	CreadaEn             time.Time `json:"creadaEn"                  db:"creada_en"`
}

// ConsignaManualRequest es el payload JSON entrante del panel web para el
// envío manual de consigna térmica (SCA-320). LoteID es obligatorio: la
// consigna se encola para el lote abierto en modo MANUAL y su sector.
// ProductoID es obligatorio: se valida contra el rango real del producto.
type ConsignaManualRequest struct {
	// SectorID es opcional: si viene, debe coincidir con el sector del lote.
	SectorID string `json:"sectorId,omitempty"`
	// HornoID queda como alias legacy de SectorID.
	HornoID                string  `json:"hornoId,omitempty"`
	ProductoID             string  `json:"productoId"`
	TemperaturaObjetivo    float64 `json:"temperaturaObjetivo"`
	VelocidadCintaObjetivo float64 `json:"velocidadCintaObjetivo"`
	Usuario                string  `json:"usuario,omitempty"` // nombre del operario que envía la consigna manual (opcional)
	// LoteID identifica el lote abierto en modo MANUAL al que se asocia la
	// consigna. Requerido.
	LoteID *string `json:"loteId,omitempty"`
}

// OvenID resuelve el id de sector/horno enviado: prefiere sectorId y cae al
// alias legacy hornoId. Devuelve el valor trimeado.
func (r ConsignaManualRequest) OvenID() string {
	if id := strings.TrimSpace(r.SectorID); id != "" {
		return id
	}
	return strings.TrimSpace(r.HornoID)
}

// Validate aplica las reglas de forma básicas del request (presencia, valores
// positivos). El rango seguro específico por producto y la coincidencia de
// sector con el lote se validan en el Service, porque dependen de datos
// persistidos.
func (r ConsignaManualRequest) Validate() error {
	var errs []string

	if r.LoteID == nil || strings.TrimSpace(*r.LoteID) == "" {
		errs = append(errs, "loteId: es requerido")
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
// de consignas encoladas para la Pi.
type Repository interface {
	Save(ctx context.Context, c *Consigna) error
	GetByLoteID(ctx context.Context, loteID string) ([]Consigna, error)
	// MarcarAplicadaPorDecision cierra como APLICADA la consigna encolada con
	// ese decision_id. Idempotente: 0 filas no es error. El ack de la Pi es la
	// única transición a APLICADA.
	MarcarAplicadaPorDecision(ctx context.Context, decisionID string) error
}

// Service encola una consigna manual para que la Pi ENTRADA la levante por
// lote y audita el intento en historial_consignas.
type Service interface {
	// DispatchManual valida el request contra el rango seguro del producto y,
	// si es válido, encola la consigna para la Pi (SCA-320).
	DispatchManual(ctx context.Context, req ConsignaManualRequest) (*Consigna, error)

	// GetHistorialByLote devuelve el historial de auditoría de consignas de un lote.
	GetHistorialByLote(ctx context.Context, loteID string) ([]Consigna, error)
}
