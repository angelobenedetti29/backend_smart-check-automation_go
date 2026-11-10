// Package oee define los tipos y el contrato de persistencia del cálculo de
// OEE (Availability, Performance, Quality).
//
// El período se expresa en días ART (America/Argentina/Buenos_Aires) e incluye
// ambas fechas. El repositorio traduce ese período a un rango de timestamptz
// semiabierto [desde 00:00, hasta+1día 00:00) y trae las filas que lo solapan.
package oee

import (
	"context"
	"errors"
	"time"
)

// Timezone es la zona horaria de negocio usada para agrupar los días.
const Timezone = "America/Argentina/Buenos_Aires"

// Errores de validación del período. El handler los traduce a 400.
var (
	ErrFechaInvalida   = errors.New("fecha inválida: se espera el formato YYYY-MM-DD")
	ErrDesdeMayorHasta = errors.New("la fecha desde no puede ser posterior a la fecha hasta")
	ErrRangoExcedido   = errors.New("el rango máximo es de 366 días")
)

// Periodo es el rango inclusivo de fechas ART, con ambas fechas a las 00:00.
type Periodo struct {
	Desde time.Time
	Hasta time.Time
}

// Filtros del cálculo. Sólo se admite sector: el plan y las paradas no se
// pueden desagregar por producto/turno de forma consistente.
type Filtros struct {
	SectorID *string
}

// Query es el período validado más los filtros aplicados.
type Query struct {
	Periodo Periodo
	Filtros Filtros
}

// PeriodoResumen es la representación JSON del período aplicado.
type PeriodoResumen struct {
	Desde    string `json:"desde"`
	Hasta    string `json:"hasta"`
	Timezone string `json:"timezone"`
}

// FiltrosResumen es la representación JSON de los filtros aplicados.
type FiltrosResumen struct {
	SectorID *string `json:"sector_id"`
}

// PlanOEE es una fila del plan de producción que solapa el período.
type PlanOEE struct {
	SectorID   string
	ProductoID string
	InicioAt   time.Time
	FinAt      time.Time
	KgPlan     float64
}

// ParadaOEE es un evento de parada que solapa el período.
type ParadaOEE struct {
	SectorID string
	InicioAt time.Time
	FinAt    time.Time
	Tipo     string
}

// LoteOEE es un lote CERRADO con inicio_at dentro del período.
type LoteOEE struct {
	SectorID   string
	ProductoID string
	KgTotal    float64
	KgOK       float64
}

// Datos es el conjunto crudo que consume el cálculo. Capacidades y Sectores
// están indexados por id; una capacidad nil significa que el producto no
// tiene capacidad nominal informada.
type Datos struct {
	Plan        []PlanOEE
	Paradas     []ParadaOEE
	Lotes       []LoteOEE
	Capacidades map[string]*float64
	Sectores    map[string]string
}

// ResumenSector es el bloque de indicadores de un sector o del global.
type ResumenSector struct {
	SectorID             string   `json:"sector_id"`
	SectorNombre         string   `json:"sector_nombre"`
	Calculable           bool     `json:"calculable"`
	Motivos              []string `json:"motivos"`
	AvailabilityPct      *float64 `json:"availability_pct"`
	PerformancePct       *float64 `json:"performance_pct"`
	QualityPct           *float64 `json:"quality_pct"`
	OEEPct               *float64 `json:"oee_pct"`
	PlannedTimeMin       float64  `json:"planned_time_min"`
	PlannedDowntimeMin   float64  `json:"planned_downtime_min"`
	UnplannedDowntimeMin float64  `json:"unplanned_downtime_min"`
	OperatingTimeMin     float64  `json:"operating_time_min"`
	KgPlan               float64  `json:"kg_plan"`
	KgProducidos         float64  `json:"kg_producidos"`
	KgOK                 float64  `json:"kg_ok"`
	CapacidadTeoricaKg   float64  `json:"capacidad_teorica_kg"`
}

// Resumen es el cuerpo de GET /api/v1/reportes/oee.
type Resumen struct {
	Periodo   PeriodoResumen  `json:"periodo"`
	Filtros   FiltrosResumen  `json:"filtros"`
	Global    ResumenSector   `json:"global"`
	PorSector []ResumenSector `json:"por_sector"`
}

// Repository define el contrato de persistencia del cálculo de OEE.
type Repository interface {
	// Datos trae, en un único snapshot, el plan y las paradas que solapan el
	// período, los lotes CERRADOS con inicio_at dentro del período, y las
	// capacidades/nombres de sectores.
	Datos(ctx context.Context, q Query) (*Datos, error)
}
