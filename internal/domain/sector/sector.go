// Package sector define el modelo de dominio de los sectores de producción y
// su contrato de persistencia. Un sector agrupa a lo sumo un dispositivo
// ENTRADA_HORNO y uno SALIDA_HORNO que operan la misma línea.
package sector

import (
	"context"
	"errors"
)

// ErrSinSector se retorna cuando el dispositivo no existe o no está asignado a
// ningún sector. Comparar con errors.Is() en el service y el handler.
var ErrSinSector = errors.New("sector: el dispositivo no pertenece a un sector")

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
}
