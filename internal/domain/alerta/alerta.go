package alerta

import "time"

// Alerta representa una alerta industrial generada durante la operación ante
// anomalías detectadas.
type Alerta struct {
	ID        string    `json:"id"`
	HornoID   string    `json:"horno_id"`
	Nivel     string    `json:"nivel"` // "INFO", "WARNING", "CRITICAL"
	Mensaje   string    `json:"mensaje"`
	CreadaEn  time.Time `json:"creada_en"`
}

// Repository define el contrato de persistencia de los eventos de alerta.
type Repository interface {
	Save(alerta *Alerta) error
	GetByHornoID(hornoID string) ([]Alerta, error)
}
