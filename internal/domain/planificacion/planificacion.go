// Package planificacion define los modelos de importación del plan de
// producción, las paradas de línea y las lecturas de energía (fase 1 de OEE),
// junto con su contrato de persistencia.
package planificacion

import (
	"context"
	"errors"
	"time"
)

// ErrFormatoCSV se retorna cuando el archivo no tiene la cabecera esperada o
// no puede parsearse como CSV. El handler lo traduce a HTTP 400.
var ErrFormatoCSV = errors.New("planificacion: formato de CSV inválido")

// Errores de validación del listado y del borrado. El handler los traduce a
// HTTP 400 (ErrNoEncontrado a 404).
var (
	ErrFechaInvalida   = errors.New("fecha inválida: se espera el formato YYYY-MM-DD")
	ErrDesdeMayorHasta = errors.New("la fecha desde no puede ser posterior a la fecha hasta")
	ErrRangoExcedido   = errors.New("el rango máximo es de 366 días")
	ErrLimiteInvalido  = errors.New("limite inválido: se espera un entero")
	ErrIDInvalido      = errors.New("id inválido: se espera un UUID")
	ErrNoEncontrado    = errors.New("registro no encontrado")
)

// PlanRow es una fila validada de plan de producción.
type PlanRow struct {
	SectorID   string
	ProductoID string
	Fecha      time.Time
	Turno      string
	InicioAt   time.Time
	FinAt      time.Time
	KgPlan     float64
}

// ParadaRow es una fila validada de evento de parada. LoteID vacío representa
// un evento no asociado a un lote (NULL en la base).
type ParadaRow struct {
	SectorID    string
	LoteID      string
	InicioAt    time.Time
	FinAt       time.Time
	CausaCodigo string
	Tipo        string
	Notas       string
}

// EnergiaRow es una fila validada de lectura de energía. GasM3 nil representa
// una lectura sin medición de gas (NULL en la base).
type EnergiaRow struct {
	SectorID      string
	LoteID        string
	PeriodoInicio time.Time
	PeriodoFin    time.Time
	Kwh           float64
	GasM3         *float64
}

// PlanItem es una fila del listado editable del plan de producción, ya con los
// nombres de sector y producto resueltos.
type PlanItem struct {
	ID             string    `json:"id"`
	SectorID       string    `json:"sector_id"`
	SectorNombre   string    `json:"sector_nombre"`
	ProductoID     string    `json:"producto_id"`
	ProductoNombre string    `json:"producto_nombre"`
	Fecha          string    `json:"fecha"`
	Turno          string    `json:"turno"`
	InicioAt       time.Time `json:"inicio_at"`
	FinAt          time.Time `json:"fin_at"`
	KgPlan         float64   `json:"kg_plan"`
}

// ParadaItem es una fila del listado de paradas.
type ParadaItem struct {
	ID           string    `json:"id"`
	SectorID     string    `json:"sector_id"`
	SectorNombre string    `json:"sector_nombre"`
	LoteID       *string   `json:"lote_id"`
	InicioAt     time.Time `json:"inicio_at"`
	FinAt        time.Time `json:"fin_at"`
	CausaCodigo  string    `json:"causa_codigo"`
	Tipo         string    `json:"tipo"`
	Notas        *string   `json:"notas"`
}

// EnergiaItem es una fila del listado de lecturas de energía.
type EnergiaItem struct {
	ID            string    `json:"id"`
	SectorID      string    `json:"sector_id"`
	SectorNombre  string    `json:"sector_nombre"`
	LoteID        *string   `json:"lote_id"`
	PeriodoInicio time.Time `json:"periodo_inicio"`
	PeriodoFin    time.Time `json:"periodo_fin"`
	Kwh           float64   `json:"kwh"`
	GasM3         *float64  `json:"gas_m3"`
}

// FiltroListado son los filtros ya validados de un listado. Desde es inclusivo
// y Hasta exclusivo (medianoche ART del día siguiente).
type FiltroListado struct {
	SectorID string
	Desde    time.Time
	Hasta    time.Time
	Limite   int
}

// Listado es la página devuelta por los GET de planificación. Total es el
// count con los mismos filtros, sin límite.
type Listado[T any] struct {
	Total    int  `json:"total"`
	Truncada bool `json:"truncada"`
	Items    []T  `json:"items"`
}

// ErrorImportacion detalla una fila rechazada durante la importación.
type ErrorImportacion struct {
	Fila    int    `json:"fila"`
	Mensaje string `json:"mensaje"`
}

// ImportResult es el resumen devuelto al panel tras procesar un CSV.
type ImportResult struct {
	Procesadas   int                `json:"procesadas"`
	Insertadas   int                `json:"insertadas"`
	Actualizadas int                `json:"actualizadas"`
	Errores      []ErrorImportacion `json:"errores"`
}

// Repository define el contrato de persistencia de la planificación. La
// implementación real vive en internal/repository/.
type Repository interface {
	// SectoresExistentes devuelve el subconjunto de ids que existen en sectores.
	SectoresExistentes(ctx context.Context, ids []string) (map[string]bool, error)
	// ProductosExistentes devuelve el subconjunto de ids que existen en productos.
	ProductosExistentes(ctx context.Context, ids []string) (map[string]bool, error)
	// LotesExistentes devuelve el subconjunto de ids que existen en lotes_productivos.
	LotesExistentes(ctx context.Context, ids []string) (map[string]bool, error)
	// UpsertPlan inserta o actualiza filas por (sector_id, inicio_at, producto_id)
	// y devuelve cuántas filas se insertaron y cuántas se actualizaron.
	UpsertPlan(ctx context.Context, rows []PlanRow) (insertadas, actualizadas int, err error)
	// UpsertParadas inserta o actualiza filas por (sector_id, inicio_at, causa_codigo).
	UpsertParadas(ctx context.Context, rows []ParadaRow) (insertadas, actualizadas int, err error)
	// UpsertEnergia inserta o actualiza filas por (sector_id, periodo_inicio, periodo_fin).
	UpsertEnergia(ctx context.Context, rows []EnergiaRow) (insertadas, actualizadas int, err error)

	// ListPlan lista el plan de un sector en una ventana (desde/hasta inclusive);
	// sectorID vacío no filtra por sector.
	ListPlan(ctx context.Context, sectorID string, desde, hasta time.Time) ([]PlanRow, error)
	// ListParadas lista las paradas de un sector en una ventana; sectorID vacío
	// no filtra por sector.
	ListParadas(ctx context.Context, sectorID string, desde, hasta time.Time) ([]ParadaRow, error)
	// ListEnergia lista las lecturas de un sector en una ventana; sectorID vacío
	// no filtra por sector.
	ListEnergia(ctx context.Context, sectorID string, desde, hasta time.Time) ([]EnergiaRow, error)
	// PlanSolapados lista el plan del sector que solapa [desde, hasta) con
	// semántica half-open: inicio_at < hasta AND fin_at > desde.
	PlanSolapados(ctx context.Context, sectorID string, desde, hasta time.Time) ([]PlanRow, error)

	// ListPlanItems lista el plan para el panel, con nombres de sector y
	// producto, ordenado por inicio_at descendente y truncado a f.Limite.
	ListPlanItems(ctx context.Context, f FiltroListado) (Listado[PlanItem], error)
	// ListParadaItems lista las paradas del mismo modo.
	ListParadaItems(ctx context.Context, f FiltroListado) (Listado[ParadaItem], error)
	// ListEnergiaItems lista las lecturas del mismo modo.
	ListEnergiaItems(ctx context.Context, f FiltroListado) (Listado[EnergiaItem], error)

	// DeletePlan elimina una fila del plan por id; false si no existía.
	DeletePlan(ctx context.Context, id string) (bool, error)
	// DeleteParada elimina una parada por id; false si no existía.
	DeleteParada(ctx context.Context, id string) (bool, error)
	// DeleteEnergia elimina una lectura por id; false si no existía.
	DeleteEnergia(ctx context.Context, id string) (bool, error)
}
