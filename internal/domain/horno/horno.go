package horno

import "time"

// EstadoControlManual indica que el enlace con el controlador físico del
// horno falló (o no pudo confirmarse) al intentar despachar una consigna.
// Mientras el horno esté en este estado, el control automático de consignas
// (SCA-142) queda bloqueado — solo el envío manual (SCA-320) puede operarlo,
// como vía de escape segura — hasta que un despacho se aplique con éxito y
// restaure el estado a "ACTIVO" (ver internal/service/consigna).
const EstadoControlManual = "CONTROL_MANUAL"

// Horno es el registro en memoria del estado de COMANDO/AUDITORÍA del sector:
// la última consigna despachada y el flag CONTROL_MANUAL. NO es la fuente del
// estado actual del horno: GET /api/v1/horno se proyecta desde metricas_horno
// (telemetría real de la Pi) + el lote abierto.
type Horno struct {
	ID             string    `json:"id"`
	Nombre         string    `json:"nombre"`
	Temperatura    float64   `json:"temperatura"`
	VelocidadCinta float64   `json:"velocidad_cinta"`       // Velocidad activa de la cinta transportadora (m/s)
	ProductoID     *string   `json:"producto_id,omitempty"` // Producto activo actualmente en el horno (última consigna aplicada)
	LoteID         *string   `json:"lote_id,omitempty"`     // Lote de correlación de la última consigna aplicada
	Estado         string    `json:"estado"`                // "ACTIVO", "ATENCION", "MANTENIMIENTO", "CONTROL_MANUAL"
	UltimoCheck    time.Time `json:"ultimo_check"`          // Timestamp of the last check
}

// Repository defines the contract for persisting and retrieving Horno entities.
type Repository interface {
	GetByID(id string) (*Horno, error)
	// Ensure devuelve el horno en memoria de un sector, creándolo si todavía no
	// existe (get-or-create idempotente). Es un registro de comando/auditoría:
	// lo usan el dispatch de consignas y el endpoint legacy
	// POST /api/v1/horno/temperatura. El estado real se lee de metricas_horno.
	Ensure(id string) (*Horno, error)
	Update(horno *Horno) error
}

// Service defines the business logic operations for industrial ovens.
type Service interface {
	// CheckStatus conserva el acceso al horno en memoria para compatibilidad;
	// no es la fuente del estado real del horno.
	CheckStatus(id string) (*Horno, error)
	UpdateTemperature(id string, temp float64) (*Horno, error)
}
