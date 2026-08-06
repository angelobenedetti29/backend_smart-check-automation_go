package horno

import "time"

// Horno represents an industrial oven entity in the factory line.
type Horno struct {
	ID             string    `json:"id"`
	Nombre         string    `json:"nombre"`
	Temperatura    float64   `json:"temperatura"`
	VelocidadCinta float64   `json:"velocidad_cinta"`       // Velocidad activa de la cinta transportadora (m/s)
	ProductoID     *string   `json:"producto_id,omitempty"` // Producto activo actualmente en el horno (última consigna aplicada)
	LoteID         *string   `json:"lote_id,omitempty"`     // Lote de correlación de la última consigna aplicada
	Estado         string    `json:"estado"`                // "ACTIVO", "INACTIVO", "MANTENIMIENTO"
	UltimoCheck    time.Time `json:"ultimo_check"`          // Timestamp of the last check
}

// Repository defines the contract for persisting and retrieving Horno entities.
type Repository interface {
	GetByID(id string) (*Horno, error)
	Update(horno *Horno) error
}

// Service defines the business logic operations for industrial ovens.
type Service interface {
	CheckStatus(id string) (*Horno, error)
	UpdateTemperature(id string, temp float64) (*Horno, error)
}
