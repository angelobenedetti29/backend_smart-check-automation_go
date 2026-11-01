package alerta

import (
	"context"
	"errors"
	"time"
)

// Estados del ciclo de vida de una alerta.
const (
	EstadoActiva   = "ACTIVA"
	EstadoAck      = "ACK"
	EstadoResuelta = "RESUELTA"
)

// Severidades admitidas.
const (
	SeveridadInfo     = "INFO"
	SeveridadWarning  = "WARNING"
	SeveridadCritical = "CRITICAL"
)

// Tipos de alerta generados por el sistema.
const (
	TipoTemperaturaHorno        = "TEMPERATURA_HORNO"
	TipoConsignaEnlace          = "CONSIGNA_ENLACE"
	TipoPrecalentamientoTimeout = "PRECALENTAMIENTO_TIMEOUT"
)

// Errores de dominio del repositorio de alertas.
var (
	ErrNotFound     = errors.New("alerta no encontrada")
	ErrYaReconocida = errors.New("la alerta ya fue reconocida")
)

// Usuario es la identidad mínima de quien reconoció una alerta.
type Usuario struct {
	Email  string `json:"email"`
	Nombre string `json:"nombre"`
}

// Alerta representa una alerta industrial generada durante la operación ante
// anomalías detectadas.
type Alerta struct {
	ID        string     `json:"id"`
	SectorID  *string    `json:"sector_id,omitempty"`
	LoteID    *string    `json:"lote_id,omitempty"`
	HornoID   string     `json:"horno_id,omitempty"`
	Tipo      string     `json:"tipo"`
	Severidad string     `json:"severidad"`
	Estado    string     `json:"estado"`
	Mensaje   string     `json:"mensaje"`
	CreadaEn  time.Time  `json:"created_at"`
	AckedPor  *Usuario   `json:"acked_por,omitempty"`
	AckedAt   *time.Time `json:"acked_at,omitempty"`
}

// Repository define el contrato de persistencia de los eventos de alerta.
//
// Save y GetByHornoID no reciben contexto a propósito: horno_service y
// horno_handler los invocan sin uno y refactorizarlos excedería el alcance.
// La implementación PostgreSQL los acota internamente con un timeout.
type Repository interface {
	Save(a *Alerta) error
	GetByHornoID(hornoID string) ([]Alerta, error)
	Listar(ctx context.Context, estado string) ([]Alerta, error)
	Ack(ctx context.Context, id string, usuario Usuario) (*Alerta, error)
}
