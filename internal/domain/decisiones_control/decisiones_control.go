// Package decisiones_control define el modelo de dominio del audit del MPC.
//
// Cada fila de la tabla decisiones_control es una decisión del optimizador:
// qué propuso el MPC, qué terminó aplicándose, y con qué observaciones.
// Permite responder "¿el optimizador mejoró las cosas?" en retrospectiva
// comparando z_predicho con z_observado.
package decisiones_control

import (
	"context"
	"errors"
	"time"
)

// Estados posibles del flag aplicado_por.
const (
	AplicadoPorMPC      = "MPC"      // modo AUTOMATICO, despachado por el control
	AplicadoPorOperario = "OPERARIO" // modo SUPERVISION, aprobado manualmente
	AplicadoPorNadie    = "NADIE"    // propuesta descartada o no aprobada
)

// PrediccionResumen es el subset de la salida del XGBoost que viaja al
// cliente (lo demás es interno). Permite al operario distinguir si la
// decisión del MPC vino del modelo real o del placeholder determinístico.
type PrediccionResumen struct {
	PctCrudos     float64 `json:"pctCrudos"`
	PctOk         float64 `json:"pctOk"`
	PctQuemados   float64 `json:"pctQuemados"`
	ModelVersion  string  `json:"modelVersion"`
	EsPlaceholder bool    `json:"esPlaceholder"`
}

// DecisionControl es el registro persistido.
type DecisionControl struct {
	ID              string     `json:"id"`
	LoteID          string     `json:"lote_id"`
	VentanaInicio   time.Time  `json:"ventana_inicio"`
	VentanaFin      time.Time  `json:"ventana_fin"`
	Muestras        int        `json:"muestras"`
	TempActual      float64    `json:"temp_actual"`
	VelActual       *float64   `json:"vel_actual,omitempty"`
	PctCrudosObs    float64    `json:"pct_crudos_obs"`
	PctOkObs        float64    `json:"pct_ok_obs"`
	PctQuemadosObs  float64    `json:"pct_quemados_obs"`
	ConfPromedio    float64    `json:"conf_promedio"`
	TempPropuesto   float64    `json:"temp_propuesto"`
	VelPropuesto    float64    `json:"vel_propuesto"`
	TempAplicado    *float64   `json:"temp_aplicado,omitempty"`
	VelAplicado     *float64   `json:"vel_aplicado,omitempty"`
	// Prediccion es la salida cruda del XGBoost (model_version, es_placeholder, ...).
	// En la API se serializa como PrediccionResumen (subset); el []byte raw
	// persiste en BD como JSONB pero no viaja en el response.
	Prediccion     []byte             `json:"-"`
	PrediccionView *PrediccionResumen `json:"prediccion,omitempty"`
	ZObservado     *float64           `json:"z_observado,omitempty"`
	ZPredicho      *float64           `json:"z_predicho,omitempty"`
	RazonSkipped   *string            `json:"razon_skipped,omitempty"`
	AplicadoPor    string             `json:"aplicado_por"`
	// DecisionOrigenID apunta a la decisión origen cuando esta fila fue
	// promovida (NADIE→OPERARIO/AUTO). Self-ref cuando se promueve la misma fila.
	DecisionOrigenID *string          `json:"decision_origen_id,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
}

// ErrNoHayDecision indica que se intentó cargar una decisión que no existe.
var ErrNoHayDecision = errors.New("decisiones_control: la decisión no existe")

// Repository define el contrato de persistencia del audit del MPC.
type Repository interface {
	// Save persiste una decisión y completa ID/CreatedAt.
	Save(ctx context.Context, d *DecisionControl) error

	// ListByLote devuelve las últimas N decisiones del lote, más recientes primero.
	ListByLote(ctx context.Context, loteID string, limite int) ([]DecisionControl, error)

	// GetUltimaPendiente devuelve la decisión de SUPERVISION más reciente que
	// todavía no fue aplicada (aplicado_por='NADIE' y sin razon_skipped), o
	// nil si el lote no tiene ninguna propuesta pendiente.
	GetUltimaPendiente(ctx context.Context, loteID string) (*DecisionControl, error)

	// MarcarAplicada actualiza la fila de una decisión para reflejar que la
	// propuesta fue aplicada por un operario (o MPC) e incluye los valores
	// efectivamente despachados. Idempotente: aplicada_por='OPERARIO' o 'MPC'
	// es estado terminal; la query no distingue.
	MarcarAplicada(ctx context.Context, decisionID string, tempAplicada, velAplicada float64, aplicadoPor string) error
}