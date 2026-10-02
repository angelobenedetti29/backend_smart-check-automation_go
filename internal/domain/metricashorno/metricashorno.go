// Package metricashorno define el modelo de dominio de la telemetría cruda
// del horno reportada por la Pi ENTRADA: temperaturas leídas (T1, T2,
// combustión, ambiente, humedad) y velocidad de cinta activa. Cada fila es un
// heartbeat periódico y se persiste append-only sobre la tabla metricas_horno.
//
// La telemetría es la fuente de la ventana de evaluación del MPC (SCA-400):
// el control automático lee los últimos N segundos para decidir.
package metricashorno

import (
	"context"
	"errors"
	"time"
)

// ErrLoteNoAbierto se retorna cuando el lote no existe o no está en estado
// ABIERTO: la telemetría sólo se acepta sobre lotes abiertos.
var ErrLoteNoAbierto = errors.New("metricashorno: el lote no está abierto")

// MetricaHorno es la entidad persistida. Los punteros representan lecturas
// que el sensor puede no entregar (caída momentánea, sensor opcional).
type MetricaHorno struct {
	ID                  string     `json:"id"`
	LoteID              string     `json:"lote_id"`
	DispositivoID       string     `json:"dispositivo_id"`
	SectorID            string     `json:"sector_id"`
	TempHorno1          *float64   `json:"temp_horno_1,omitempty"`
	TempHorno2          *float64   `json:"temp_horno_2,omitempty"`
	TempComb1           *float64   `json:"temp_comb_1,omitempty"`
	TempComb2           *float64   `json:"temp_comb_2,omitempty"`
	HumedadHorno        *float64   `json:"humedad_horno,omitempty"`
	TempAmbiente        *float64   `json:"temp_ambiente,omitempty"`
	HumedadAmbiente     *float64   `json:"humedad_ambiente,omitempty"`
	VelocidadCinta      *float64   `json:"velocidad_cinta,omitempty"`
	TempObjetivo        *float64   `json:"temp_objetivo,omitempty"`
	VelObjetivo         *float64   `json:"vel_objetivo,omitempty"`
	ConfianzaDeteccion  *float64   `json:"confianza_deteccion,omitempty"`
	ReceivedAt          time.Time  `json:"received_at"`
}

// MetricaHornoRequest es el body de POST /api/v1/lotes/{id}/metricas-horno.
// El servidor completa lote_id (de la URL), dispositivo_id (del principal
// autenticado) y sector_id (del dispositivo). El resto es lo que la Pi
// reporta en este heartbeat.
type MetricaHornoRequest struct {
	TempHorno1         *float64   `json:"temp_horno_1,omitempty"`
	TempHorno2         *float64   `json:"temp_horno_2,omitempty"`
	TempComb1          *float64   `json:"temp_comb_1,omitempty"`
	TempComb2          *float64   `json:"temp_comb_2,omitempty"`
	HumedadHorno       *float64   `json:"humedad_horno,omitempty"`
	TempAmbiente       *float64   `json:"temp_ambiente,omitempty"`
	HumedadAmbiente    *float64   `json:"humedad_ambiente,omitempty"`
	VelocidadCinta     *float64   `json:"velocidad_cinta,omitempty"`
	TempObjetivo       *float64   `json:"temp_objetivo,omitempty"`
	VelObjetivo        *float64   `json:"vel_objetivo,omitempty"`
	ConfianzaDeteccion *float64   `json:"confianza_deteccion,omitempty"`
	ReceivedAt         *time.Time `json:"received_at,omitempty"` // opcional; default: server clock
}

// Validate aplica reglas mínimas de forma. La coherencia de los rangos se
// valida a nivel del service contra los parámetros del producto.
func (r MetricaHornoRequest) Validate() error {
	// Sólo verificamos que la confianza, si está, esté en [0,1].
	if r.ConfianzaDeteccion != nil && (*r.ConfianzaDeteccion < 0 || *r.ConfianzaDeteccion > 1) {
		return errors.New("metricashorno: confianza_deteccion debe estar entre 0 y 1")
	}
	return nil
}

// Repository define el contrato de persistencia de la telemetría.
type Repository interface {
	// Save persiste una métrica. Devuelve el ID generado y el received_at del server.
	Save(ctx context.Context, m *MetricaHorno) error

	// GetUltimasParaLote devuelve las últimas N métricas de un lote, ordenadas
	// de la más reciente a la más antigua. Es la consulta principal del MPC.
	GetUltimasParaLote(ctx context.Context, loteID string, limite int) ([]MetricaHorno, error)
}