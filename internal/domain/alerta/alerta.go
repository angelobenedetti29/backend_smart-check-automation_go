package alerta

import "time"

// Alerta represents an industrial alert generated during operation check anomalies.
type Alerta struct {
	ID        string    `json:"id"`
	HornoID   string    `json:"horno_id"`
	Nivel     string    `json:"nivel"` // "INFO", "WARNING", "CRITICAL"
	Mensaje   string    `json:"mensaje"`
	CreadaEn  time.Time `json:"creada_en"`
}

// Repository defines the storage interface for alert events.
type Repository interface {
	Save(alerta *Alerta) error
	GetByHornoID(hornoID string) ([]Alerta, error)
}
