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
// cargados, necesarios para validar una consigna manual contra su rango seguro.
var ErrParametrosNoExiste = errors.New("consigna: no hay parámetros de control (o setpoints) cargados para el producto")

// ErrFueraDeRango indica que los valores solicitados en un despacho manual
// están fuera del rango seguro definido para el producto.
var ErrFueraDeRango = errors.New("consigna: valores fuera de rango seguro")

// ErrDispatchFallido indica que el controlador físico del horno rechazó o no
// pudo aplicar la consigna.
var ErrDispatchFallido = errors.New("consigna: el controlador físico rechazó la consigna")

// ErrHorno2NoHabilitado indica que la consigna pidió TempHorno2Objetivo pero
// el producto del lote no tiene el horno 2 habilitado.
var ErrHorno2NoHabilitado = errors.New("consigna: el producto no tiene el horno 2 habilitado")

// Origen identifica el mecanismo que disparó la consigna. Hoy conviven:
//   - MANUAL (SCA-320): envío desde el panel por un operario.
//   - AUTOMATICO (SCA-142 reactivado por SCA-400): despacho que dispara el
//     optimizador MPC del control automático cuando el modo del lote es
//     AUTOMATICO. Persistido y auditado igual que el manual.
type Origen string

const (
	OrigenManual     Origen = "MANUAL"
	OrigenAutomatico Origen = "AUTOMATICO"
)

// ValidationError agrupa todos los errores de validación de un ConsignaManualRequest.
type ValidationError struct {
	Fields []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("errores de validación: %s", strings.Join(e.Fields, "; "))
}

// Consigna representa un registro histórico de auditoría: una consigna
// térmica y de velocidad de cinta que fue (o se intentó) despachar al
// controlador físico del horno, tal como se persiste en PostgreSQL.
//
// Desde SCA-400 incluye TempHorno2Objetivo/Previa y Horno2Habilitado para
// soportar dos zonas térmicas cuando la línea tiene horno 2 en serie.
type Consigna struct {
	ID                     string    `json:"id"                        db:"id"`
	DispositivoID          *string   `json:"dispositivoId,omitempty"   db:"dispositivo_id"`
	HornoID                string    `json:"hornoId"                   db:"horno_id"`
	LoteID                 *string   `json:"loteId,omitempty"          db:"lote_id"`
	ProductoID             *string   `json:"productoId,omitempty"      db:"producto_id"`
	TemperaturaObjetivo    float64   `json:"temperaturaObjetivo"       db:"temperatura_objetivo"`
	VelocidadCintaObjetivo float64   `json:"velocidadCintaObjetivo"    db:"velocidad_cinta_objetivo"`
	Origen                 Origen    `json:"origen"                    db:"origen"`
	Usuario                *string   `json:"usuario,omitempty"         db:"usuario"` // operario que envió la consigna manual (opcional)
	Exitosa                bool      `json:"exitosa"                   db:"exitosa"`
	MotivoError            *string   `json:"motivoError,omitempty"     db:"motivo_error"`
	TemperaturaPrevia      *float64  `json:"temperaturaPrevia,omitempty" db:"temperatura_previa"`
	VelocidadCintaPrevia   *float64  `json:"velocidadCintaPrevia,omitempty" db:"velocidad_cinta_previa"`
	// SCA-400: horno 2 (T2). NULL cuando la línea tiene un solo horno.
	TempHorno2Objetivo     *float64  `json:"tempHorno2Objetivo,omitempty" db:"temp_horno2_objetivo"`
	TempHorno2Previa       *float64  `json:"tempHorno2Previa,omitempty"   db:"temp_horno2_previa"`
	Horno2Habilitado       bool      `json:"horno2Habilitado"             db:"horno2_habilitado"`
	CreadaEn               time.Time `json:"creadaEn"                  db:"creada_en"`
}

// ConsignaManualRequest es el payload JSON entrante del panel web para el
// envío manual de consigna térmica (SCA-320). ProductoID es obligatorio: el
// despacho manual siempre valida contra el rango real cargado en el producto
// unificado, sin caer a límites de seguridad hardcodeados.
//
// Desde SCA-400 admite TempHorno2 opcional para líneas con horno 2.
type ConsignaManualRequest struct {
	HornoID                string  `json:"hornoId"`
	ProductoID             string  `json:"productoId"`
	TemperaturaObjetivo    float64 `json:"temperaturaObjetivo"`
	VelocidadCintaObjetivo float64 `json:"velocidadCintaObjetivo"`
	TempHorno2Objetivo     *float64 `json:"tempHorno2Objetivo,omitempty"`
	Usuario                string  `json:"usuario,omitempty"` // nombre del operario que envía la consigna manual (opcional)
	// LoteID es opcional: permite correlacionar un ajuste manual con el lote
	// en curso (por ejemplo, el loteId de correlación devuelto por
	// POST /api/v1/lotes/inicio) para que aparezca en su historial de
	// auditoría. Si se omite, la consigna manual queda auditada sin lote asociado.
	LoteID *string `json:"loteId,omitempty"`
}

// Validate aplica las reglas de forma básicas del request (presencia, valores
// positivos). El rango seguro específico por producto se valida en el
// Service, porque depende de datos del producto unificado, no es una regla
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
}

// Service orquesta la resolución de la consigna, el despacho al controlador
// físico simulado, la actualización del estado activo del horno y el
// registro de auditoría.
//
// Desde SCA-400 expone un Dispatch con origen parametrizable para que el
// control automático (MPC) pueda reusar exactamente el mismo camino que el
// despacho manual (SCA-320), distinguiendo sólo el origen a efectos de
// auditoría. DispatchManual queda como wrapper con origen=MANUAL.
type Service interface {
	// DispatchManual valida el request contra el rango seguro del producto y,
	// si es válido, despacha la consigna solicitada por el operario (SCA-320).
	DispatchManual(ctx context.Context, req ConsignaManualRequest) (*Consigna, error)

	// Dispatch valida contra el rango seguro del producto y despacha la
	// consigna con el origen indicado (MANUAL o AUTOMATICO). El caller (panel
	// o control automático) elige el origen; el service no decide.
	Dispatch(ctx context.Context, req ConsignaManualRequest, origen Origen, usuario *string) (*Consigna, error)

	// GetHistorialByLote devuelve el historial de auditoría de consignas de un lote.
	GetHistorialByLote(ctx context.Context, loteID string) ([]Consigna, error)
}
