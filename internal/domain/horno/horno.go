package horno

import "time"

// EstadoControlManual indica que el enlace con el controlador físico del
// horno falló (o no pudo confirmarse) al intentar despachar una consigna.
// Mientras el horno esté en este estado, el control automático de consignas
// (SCA-142) queda bloqueado — solo el envío manual (SCA-320) puede operarlo,
// como vía de escape segura — hasta que un despacho se aplique con éxito y
// restaure el estado a "ACTIVO" (ver internal/service/consigna).
const EstadoControlManual = "CONTROL_MANUAL"

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
