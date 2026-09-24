package dispositivo

import "time"

// AuthStatus es el estado de admisión del dispositivo, independiente de su
// salud por heartbeat.
type AuthStatus string

// Estados de admisión posibles de la credencial de un dispositivo.
const (
	AuthUnenrolled AuthStatus = "unenrolled"
	AuthActive     AuthStatus = "active"
	AuthDisabled   AuthStatus = "disabled"
	AuthRevoked    AuthStatus = "revoked"
)

// DeviceRead es el modelo de lectura del catálogo que expone, además del estado
// de salud, si el dispositivo tiene un secret registrado. El sector del
// dispositivo viaja en el campo embebido SectorID de EstadoDispositivo.
type DeviceRead struct {
	EstadoDispositivo
	Tipo          *string    `json:"type,omitempty"`
	AuthStatus    AuthStatus `json:"authStatus"`
	HasSecret     bool       `json:"hasSecret"`
	AuthUpdatedAt *time.Time `json:"authUpdatedAt,omitempty"`
}
