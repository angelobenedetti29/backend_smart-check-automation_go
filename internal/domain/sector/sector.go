// Package sector define el modelo de dominio de los sectores de producción y
// su contrato de persistencia. Un sector agrupa a lo sumo un dispositivo
// ENTRADA_HORNO y uno SALIDA_HORNO que operan la misma línea.
package sector

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// MaxNombreLength es el largo máximo del nombre de un sector, coherente con el
// VARCHAR(100) de la tabla sectores (database/schema.sql).
const MaxNombreLength = 100

// ErrSinSector se retorna cuando el dispositivo no existe o no está asignado a
// ningún sector. Comparar con errors.Is() en el service y el handler.
var ErrSinSector = errors.New("sector: el dispositivo no pertenece a un sector")

// ErrSectorNotFound se retorna al buscar, modificar o eliminar un sector que no
// existe. Comparar con errors.Is() en el service y el handler.
var ErrSectorNotFound = errors.New("sector: no encontrado")

// ErrSectorConLotes se retorna al intentar eliminar un sector que todavía tiene
// lotes productivos asociados. El borrado se bloquea para no romper el historial.
var ErrSectorConLotes = errors.New("sector: no se puede eliminar porque tiene lotes asociados")

// ErrSectorIDExists señala que el id generado ya está ocupado por otro sector.
// El repositorio lo devuelve al detectar la violación de la clave primaria; el
// service reintenta con un sufijo.
var ErrSectorIDExists = errors.New("sector: el id ya existe")

// Sector es la entidad de agrupación de dispositivos de una misma línea.
type Sector struct {
	ID     string `json:"id"`
	Nombre string `json:"nombre"`
}

// Companero describe al otro dispositivo del sector, tal como lo expone la
// respuesta de GET /api/v1/dispositivos/sector.
type Companero struct {
	DeviceID string `json:"device_id"`
	Hostname string `json:"hostname"`
	Tipo     string `json:"type"`
}

// DeviceInfo es la identidad de un dispositivo junto con su sector (nil si no
// tiene sector asignado), usada para resolver el alcance de las rutas device.
type DeviceInfo struct {
	DeviceID string  `json:"device_id"`
	Hostname string  `json:"hostname"`
	Tipo     string  `json:"type"`
	SectorID *string `json:"sector_id"`
}

// CreateSectorRequest representa el payload JSON entrante para el alta de un
// sector desde el panel (POST /api/v1/sectores).
type CreateSectorRequest struct {
	Nombre string `json:"nombre"`
}

// Validate verifica que el nombre esté presente y no supere el máximo.
func (req CreateSectorRequest) Validate() error {
	return validateNombre(req.Nombre)
}

// UpdateSectorRequest representa el payload JSON entrante para la modificación
// de un sector existente (PUT /api/v1/sectores/{id}).
type UpdateSectorRequest struct {
	Nombre string `json:"nombre"`
}

// Validate verifica que el nombre esté presente y no supere el máximo.
func (req UpdateSectorRequest) Validate() error {
	return validateNombre(req.Nombre)
}

// ValidationError agrupa los errores de validación de un request entrante.
type ValidationError struct {
	Fields []string
}

// Error implementa la interfaz error.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("errores de validación: %s", strings.Join(e.Fields, "; "))
}

// IsValidationError informa si un error es de tipo ValidationError.
func IsValidationError(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

// validateNombre aplica las reglas del nombre de sector: requerido, con trim y
// de hasta MaxNombreLength runes.
func validateNombre(nombre string) error {
	trimmed := strings.TrimSpace(nombre)
	if trimmed == "" {
		return &ValidationError{Fields: []string{"nombre: es requerido"}}
	}
	if len([]rune(trimmed)) > MaxNombreLength {
		return &ValidationError{Fields: []string{fmt.Sprintf("nombre: no puede superar los %d caracteres", MaxNombreLength)}}
	}
	return nil
}

// Repository define el contrato de persistencia de sectores y su relación con
// dispositivos. La implementación real vive en internal/repository/.
type Repository interface {
	// GetDeviceInfo resuelve id, nombre, tipo y sector del dispositivo, o
	// ErrSinSector si el dispositivo no existe.
	GetDeviceInfo(ctx context.Context, deviceID string) (*DeviceInfo, error)
	// GetByID busca un sector por id o devuelve ErrSinSector si no existe.
	GetByID(ctx context.Context, sectorID string) (*Sector, error)
	// ListCompaneros devuelve los demás dispositivos del sector, excluyendo al
	// dispositivo indicado.
	ListCompaneros(ctx context.Context, sectorID, excludeDeviceID string) ([]Companero, error)
	// List devuelve todos los sectores ordenados por nombre.
	List(ctx context.Context) ([]Sector, error)
	// Create inserta un sector nuevo. Devuelve ErrSectorIDExists si el id ya
	// está ocupado.
	Create(ctx context.Context, s *Sector) error
	// Update modifica el nombre de un sector. Devuelve ErrSectorNotFound si no
	// existe.
	Update(ctx context.Context, s *Sector) error
	// Delete elimina un sector sin lotes asociados. Devuelve ErrSectorNotFound
	// si no existe y ErrSectorConLotes si todavía tiene lotes.
	Delete(ctx context.Context, id string) error
}
