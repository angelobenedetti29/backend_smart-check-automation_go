package lote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// turnos válidos según reglas de negocio de Fermar S.A.
var turnosValidos = map[string]bool{
	"mañana": true,
	"tarde":  true,
	"noche":  true,
}

// ValidationError agrupa todos los errores de validación de un LoteRequest.
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

// Repository defines the storage contract for productive batches.
// Implementations live in the infrastructure layer (internal/repository).
type Repository interface {
	Create(ctx context.Context, lote *Lote) error
}


// LoteRequest represents the incoming JSON payload from the Raspberry Pi
// for registering a productive batch in the industrial oven line.
type LoteRequest struct {
	ID              string    `json:"id"`
	ProductoID      string    `json:"productoId"`
	ProductoNombre  string    `json:"productoNombre"`
	Turno           string    `json:"turno"`
	InicioAt        time.Time `json:"inicioAt"`
	FinAt           time.Time `json:"finAt"`
	TotalUnidades   int       `json:"totalUnidades"`
	Correctos       int       `json:"correctos"`
	Quemados        int       `json:"quemados"`
	CorrectosKg     float64   `json:"correctosKg"`
	QuemadosKg      float64   `json:"quemadosKg"`
	TempHorno1      float64   `json:"tempHorno1"`
	TempCombHorno1  *float64  `json:"tempCombHorno1"`
	TempHorno2      float64   `json:"tempHorno2"`
	TempCombHorno2  *float64  `json:"tempCombHorno2"`
	VelocidadHorno  float64   `json:"velocidadHorno"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// Lote represents the domain entity for a productive batch as stored
// in the PostgreSQL database.
type Lote struct {
	ID              string    `json:"id"`
	ProductoID      string    `json:"producto_id"`
	Turno           string    `json:"turno"`
	InicioAt        time.Time `json:"inicio_at"`
	FinAt           time.Time `json:"fin_at"`
	TotalUnidades   int       `json:"total_unidades"`
	Correctos       int       `json:"correctos"`
	Quemados        int       `json:"quemados"`
	CorrectosKg     float64   `json:"correctos_kg"`
	QuemadosKg      float64   `json:"quemados_kg"`
	TempHorno1      float64   `json:"temp_horno_1"`
	TempCombHorno1  *float64  `json:"temp_comb_horno_1"`
	TempHorno2      float64   `json:"temp_horno_2"`
	TempCombHorno2  *float64  `json:"temp_comb_horno_2"`
	VelocidadHorno  float64   `json:"velocidad_horno"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// MapLoteRequestToLote converts a LoteRequest (API DTO from Raspberry Pi)
// into a Lote (domain entity). The ProductoNombre field is discarded
// since it belongs to the productos catalog, not the batch entity.
func MapLoteRequestToLote(req LoteRequest) Lote {
	return Lote{
		ID:             req.ID,
		ProductoID:     req.ProductoID,
		Turno:          req.Turno,
		InicioAt:       req.InicioAt,
		FinAt:          req.FinAt,
		TotalUnidades:  req.TotalUnidades,
		Correctos:      req.Correctos,
		Quemados:       req.Quemados,
		CorrectosKg:    req.CorrectosKg,
		QuemadosKg:     req.QuemadosKg,
		TempHorno1:     req.TempHorno1,
		TempCombHorno1: req.TempCombHorno1,
		TempHorno2:     req.TempHorno2,
		TempCombHorno2: req.TempCombHorno2,
		VelocidadHorno: req.VelocidadHorno,
		CreatedAt:      req.CreatedAt,
		UpdatedAt:      req.UpdatedAt,
	}
}

// Validate verifica todas las reglas de negocio del LoteRequest.
// Retorna un *ValidationError con el listado completo de campos inválidos,
// o nil si el request es válido.
func (req LoteRequest) Validate() error {
	var errs []string

	// Campo obligatorio: producto_id
	if strings.TrimSpace(req.ProductoID) == "" {
		errs = append(errs, "productoId: es requerido")
	}

	// Turno debe ser uno de los valores permitidos por el negocio
	if !turnosValidos[req.Turno] {
		errs = append(errs, fmt.Sprintf("turno: valor '%s' inválido, debe ser 'mañana', 'tarde' o 'noche'", req.Turno))
	}

	// Consistencia temporal: fin_at debe ser igual o posterior a inicio_at
	if !req.FinAt.IsZero() && !req.InicioAt.IsZero() && req.FinAt.Before(req.InicioAt) {
		errs = append(errs, "finAt: debe ser igual o posterior a inicioAt")
	}

	// Unidades no negativas
	if req.TotalUnidades < 0 {
		errs = append(errs, "totalUnidades: no puede ser negativo")
	}
	if req.Correctos < 0 {
		errs = append(errs, "correctos: no puede ser negativo")
	}
	if req.Quemados < 0 {
		errs = append(errs, "quemados: no puede ser negativo")
	}

	// Consistencia de unidades: correctos + quemados == total_unidades
	if req.Correctos+req.Quemados != req.TotalUnidades {
		errs = append(errs, fmt.Sprintf(
			"correctos(%d) + quemados(%d) = %d, debe ser igual a totalUnidades(%d)",
			req.Correctos, req.Quemados, req.Correctos+req.Quemados, req.TotalUnidades,
		))
	}

	// Pesos no negativos
	if req.CorrectosKg < 0 {
		errs = append(errs, "correctosKg: no puede ser negativo")
	}
	if req.QuemadosKg < 0 {
		errs = append(errs, "quemadosKg: no puede ser negativo")
	}

	// Velocidad del horno no negativa
	if req.VelocidadHorno < 0 {
		errs = append(errs, "velocidadHorno: no puede ser negativo")
	}

	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}
	return nil
}
