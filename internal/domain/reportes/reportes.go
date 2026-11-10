// Package reportes define los tipos y el contrato de persistencia del resumen
// agregado de producción, calidad, proceso, alertas y automatización.
//
// El período se expresa en días ART (America/Argentina/Buenos_Aires) e incluye
// ambas fechas. El repositorio traduce ese período a un rango de timestamptz
// semiabierto [desde 00:00, hasta+1día 00:00) para no depender de la zona del
// servidor.
package reportes

import (
	"context"
	"errors"
	"time"
)

// Timezone es la zona horaria de negocio usada para agrupar los días.
const Timezone = "America/Argentina/Buenos_Aires"

// Turnos válidos del dominio de lotes.
var TurnosValidos = map[string]bool{
	"mañana": true,
	"tarde":  true,
	"noche":  true,
}

// EstadosAlertaValidos son los estados aceptados por el filtro del export.
var EstadosAlertaValidos = map[string]bool{
	"ACTIVA":   true,
	"ACK":      true,
	"RESUELTA": true,
}

// Errores de validación del período/filtros. El handler los traduce a 400.
var (
	ErrFechaInvalida      = errors.New("fecha inválida: se espera el formato YYYY-MM-DD")
	ErrDesdeMayorHasta    = errors.New("la fecha desde no puede ser posterior a la fecha hasta")
	ErrRangoExcedido      = errors.New("el rango máximo es de 366 días")
	ErrTurnoInvalido      = errors.New("turno inválido: se espera mañana, tarde o noche")
	ErrProductoIDInvalido = errors.New("producto_id inválido: se espera un UUID")
	ErrLimiteInvalido     = errors.New("limite inválido: se espera un entero")
	ErrCursorInvalido     = errors.New("antes_de inválido: cursor malformado")
	// ErrEstadoInvalido valida el filtro estado de GET /api/v1/reportes/alertas.
	ErrEstadoInvalido = errors.New("estado inválido: se espera ACTIVA, ACK o RESUELTA")
	// Errores de GET /api/v1/reportes/trazabilidad. El handler los traduce a
	// 400 (id inválido) y 404 (lote inexistente).
	ErrLoteIDInvalido   = errors.New("lote_id inválido: se espera un UUID")
	ErrLoteNoEncontrado = errors.New("lote no encontrado")
)

// Filtros opcionales del resumen. Turno es uno de mañana/tarde/noche.
type Filtros struct {
	SectorID   *string
	ProductoID *string
	Turno      *string
}

// Periodo es el rango inclusivo de fechas ART, con ambas fechas a las 00:00.
type Periodo struct {
	Desde time.Time
	Hasta time.Time
}

// PeriodoResumen es la representación JSON del período aplicado.
type PeriodoResumen struct {
	Desde    string `json:"desde"`
	Hasta    string `json:"hasta"`
	Timezone string `json:"timezone"`
}

// FiltrosResumen es la representación JSON de los filtros aplicados.
type FiltrosResumen struct {
	SectorID   *string `json:"sector_id"`
	ProductoID *string `json:"producto_id"`
	Turno      *string `json:"turno"`
}

// GrupoProduccion es un corte de producción por turno, sector o producto.
type GrupoProduccion struct {
	Clave       string   `json:"clave"`
	Nombre      string   `json:"nombre"`
	Lotes       int      `json:"lotes"`
	Unidades    int      `json:"unidades"`
	KgEstimados float64  `json:"kg_estimados"`
	FPYPct      *float64 `json:"fpy_pct"`
}

// Produccion agrupa los agregados productivos del período.
type Produccion struct {
	LotesTotal       int               `json:"lotes_total"`
	LotesCerrados    int               `json:"lotes_cerrados"`
	LotesAbiertos    int               `json:"lotes_abiertos"`
	Unidades         int               `json:"unidades"`
	UnidadesOK       int               `json:"unidades_ok"`
	UnidadesCrudas   int               `json:"unidades_crudas"`
	UnidadesQuemadas int               `json:"unidades_quemadas"`
	KgTotalEstimados float64           `json:"kg_total_estimados"`
	KgOKEstimados    float64           `json:"kg_ok_estimados"`
	KgMermaEstimados float64           `json:"kg_merma_estimados"`
	DuracionMediaMin *float64          `json:"duracion_media_min"`
	DuracionP95Min   *float64          `json:"duracion_p95_min"`
	UnidadesPorHora  *float64          `json:"unidades_por_hora"`
	KgPorHora        *float64          `json:"kg_por_hora"`
	PorTurno         []GrupoProduccion `json:"por_turno"`
	PorSector        []GrupoProduccion `json:"por_sector"`
	PorProducto      []GrupoProduccion `json:"por_producto"`
}

// CalidadDia es un punto de la serie diaria de calidad.
type CalidadDia struct {
	Fecha       string   `json:"fecha"`
	Lotes       int      `json:"lotes"`
	Unidades    int      `json:"unidades"`
	KgEstimados float64  `json:"kg_estimados"`
	FPYPct      *float64 `json:"fpy_pct"`
	CrudoPct    *float64 `json:"crudo_pct"`
	QuemadoPct  *float64 `json:"quemado_pct"`
}

// Calidad agrupa los indicadores de calidad del período.
type Calidad struct {
	FPYPct            *float64     `json:"fpy_pct"`
	CrudoPct          *float64     `json:"crudo_pct"`
	QuemadoPct        *float64     `json:"quemado_pct"`
	ConfianzaPromedio *float64     `json:"confianza_promedio"`
	LotesConformesPct *float64     `json:"lotes_conformes_pct"`
	SerieDiaria       []CalidadDia `json:"serie_diaria"`
}

// ProcesoDia es un punto de la serie diaria de proceso.
type ProcesoDia struct {
	Fecha               string   `json:"fecha"`
	TempRealProm        *float64 `json:"temp_real_prom"`
	TempObjetivoProm    *float64 `json:"temp_objetivo_prom"`
	TempEnToleranciaPct *float64 `json:"temp_en_tolerancia_pct"`
}

// Proceso agrupa los indicadores de seguimiento del horno.
type Proceso struct {
	TempMAEC            *float64     `json:"temp_mae_c"`
	TempEnToleranciaPct *float64     `json:"temp_en_tolerancia_pct"`
	VelMAE              *float64     `json:"vel_mae"`
	VelEnToleranciaPct  *float64     `json:"vel_en_tolerancia_pct"`
	SerieDiaria         []ProcesoDia `json:"serie_diaria"`
}

// Conteo es un par clave/total usado por los cortes de alertas y skip.
type Conteo struct {
	Clave string `json:"clave"`
	Total int    `json:"total"`
}

// AlertasDia es un punto de la serie diaria de alertas.
type AlertasDia struct {
	Fecha    string `json:"fecha"`
	Total    int    `json:"total"`
	Criticas int    `json:"criticas"`
}

// Alertas agrupa los indicadores de alertas del período.
type Alertas struct {
	Total        int            `json:"total"`
	PorSeveridad map[string]int `json:"por_severidad"`
	PorTipo      []Conteo       `json:"por_tipo"`
	AckPct       *float64       `json:"ack_pct"`
	MTTAMin      *float64       `json:"mtta_min"`
	SerieDiaria  []AlertasDia   `json:"serie_diaria"`
}

// Automatizacion agrupa los indicadores del MPC y del modo de operación.
type Automatizacion struct {
	DecisionesTotal int                 `json:"decisiones_total"`
	MPCPct          *float64            `json:"mpc_pct"`
	OperarioPct     *float64            `json:"operario_pct"`
	SinAplicarPct   *float64            `json:"sin_aplicar_pct"`
	ZMAE            *float64            `json:"z_mae"`
	TopSkip         []Conteo            `json:"top_skip"`
	ModoPct         map[string]*float64 `json:"modo_pct"`
}

// Resumen es el cuerpo del endpoint GET /api/v1/reportes/resumen.
type Resumen struct {
	ImpactoEconomico ImpactoEconomico `json:"impacto_economico"`
	Periodo          PeriodoResumen   `json:"periodo"`
	Filtros          FiltrosResumen   `json:"filtros"`
	Produccion       Produccion       `json:"produccion"`
	Calidad          Calidad          `json:"calidad"`
	Proceso          Proceso          `json:"proceso"`
	Alertas          Alertas          `json:"alertas"`
	Automatizacion   Automatizacion   `json:"automatizacion"`
}

// LoteConteos conserva NULL por bucket (el modelo no produjo ese estado) y el
// total autoritativo del lote.
type LoteConteos struct {
	OK      *int `json:"ok"`
	Crudo   *int `json:"crudo"`
	Quemado *int `json:"quemado"`
	Total   int  `json:"total"`
}

// LoteKg son los kg estimados persistidos por bucket. Total y Merma suman con
// COALESCE 0 sobre los buckets nulos.
type LoteKg struct {
	OK      *float64 `json:"ok"`
	Crudo   *float64 `json:"crudo"`
	Quemado *float64 `json:"quemado"`
	Total   float64  `json:"total"`
	Merma   float64  `json:"merma"`
}

// LoteReporte es una fila del detalle paginado de lotes del período, fuente de
// los CSV y del anexo del PDF. Las métricas derivadas van en null cuando no
// hay denominador.
type LoteReporte struct {
	CostoFabricacionUnitarioARS *float64    `json:"costo_fabricacion_unitario_ars"`
	PerdidaMermaARS             *float64    `json:"perdida_merma_ars"`
	ID                          string      `json:"id"`
	SectorID                    string      `json:"sector_id"`
	SectorNombre                string      `json:"sector_nombre"`
	ProductoID                  string      `json:"producto_id"`
	ProductoNombre              string      `json:"producto_nombre"`
	Turno                       *string     `json:"turno"`
	Estado                      string      `json:"estado"`
	AbiertoEn                   time.Time   `json:"abierto_en"`
	CerradoEn                   *time.Time  `json:"cerrado_en"`
	MotivoCierre                *string     `json:"motivo_cierre"`
	Modo                        string      `json:"modo"`
	Conteos                     LoteConteos `json:"conteos"`
	Kg                          LoteKg      `json:"kg"`
	ConfianzaPromedio           *float64    `json:"confianza_promedio"`
	DuracionMin                 *float64    `json:"duracion_min"`
	FPYPct                      *float64    `json:"fpy_pct"`
	CrudoPct                    *float64    `json:"crudo_pct"`
	QuemadoPct                  *float64    `json:"quemado_pct"`
}

// LotesParams pagina el detalle de lotes del período. AntesDe/AntesDeID forman
// el cursor opaco (inicio_at, id) para avanzar hacia atrás.
type LotesParams struct {
	Periodo   Periodo
	Filtros   Filtros
	Limite    int
	AntesDe   *time.Time
	AntesDeID string
}

// PaginaLotes es el resultado del detalle paginado: filas, total sin cursor y
// el cursor de la página siguiente (vacío en la última).
type PaginaLotes struct {
	Lotes           []LoteReporte `json:"lotes"`
	Total           int           `json:"total"`
	Limite          int           `json:"limite"`
	SiguienteCursor string        `json:"siguiente_cursor"`
}

// LoteTrazabilidad es la cabecera del lote para el PDF de trazabilidad:
// LoteReporte más los parámetros de control vigentes del lote.
type LoteTrazabilidad struct {
	LoteReporte
	Temp1Objetivo           *float64 `json:"temp1_objetivo"`
	VelObjetivo             *float64 `json:"vel_objetivo"`
	ToleranciaTempC         float64  `json:"tolerancia_temp_c"`
	UmbralAceptacionCrudo   float64  `json:"umbral_aceptacion_crudo"`
	UmbralAceptacionQuemado float64  `json:"umbral_aceptacion_quemado"`
	DeltaTemp1MaxC          *float64 `json:"delta_temp1_max_c"`
	DeltaVelMax             *float64 `json:"delta_vel_max"`
}

// PuntoProceso es un bucket temporal de la telemetría del lote.
type PuntoProceso struct {
	TS           time.Time `json:"ts"`
	TempReal     *float64  `json:"temp_real"`
	TempObjetivo *float64  `json:"temp_objetivo"`
	VelReal      *float64  `json:"vel_real"`
	VelObjetivo  *float64  `json:"vel_objetivo"`
	Muestras     int       `json:"muestras"`
}

// ProcesoTrazabilidad agrupa KPIs y serie temporal de las métricas del lote.
type ProcesoTrazabilidad struct {
	TempMAEC            *float64       `json:"temp_mae_c"`
	TempEnToleranciaPct *float64       `json:"temp_en_tolerancia_pct"`
	VelMAE              *float64       `json:"vel_mae"`
	VelEnToleranciaPct  *float64       `json:"vel_en_tolerancia_pct"`
	Serie               []PuntoProceso `json:"serie"`
}

// ConfianzaClaseTrazabilidad es la confianza del modelo por clase del lote.
type ConfianzaClaseTrazabilidad struct {
	Clase           string   `json:"clase"`
	ConfianzaMedia  float64  `json:"confianza_media"`
	ConfianzaStddev *float64 `json:"confianza_stddev"`
	Muestras        int      `json:"muestras"`
}

// DecisionTrazabilidad es una decisión del MPC del lote.
type DecisionTrazabilidad struct {
	ID             string     `json:"id"`
	CreadaEn       time.Time  `json:"creada_en"`
	VentanaInicio  time.Time  `json:"ventana_inicio"`
	VentanaFin     time.Time  `json:"ventana_fin"`
	Muestras       int        `json:"muestras"`
	TempActual     float64    `json:"temp_actual"`
	VelActual      *float64   `json:"vel_actual"`
	PctOkObs       float64    `json:"pct_ok_obs"`
	PctCrudosObs   float64    `json:"pct_crudos_obs"`
	PctQuemadosObs float64    `json:"pct_quemados_obs"`
	ConfPromedio   float64    `json:"conf_promedio"`
	TempPropuesto  float64    `json:"temp_propuesto"`
	VelPropuesto   float64    `json:"vel_propuesto"`
	TempAplicado   *float64   `json:"temp_aplicado"`
	VelAplicado    *float64   `json:"vel_aplicado"`
	ZObservado     *float64   `json:"z_observado"`
	ZPredicho      *float64   `json:"z_predicho"`
	RazonSkipped   *string    `json:"razon_skipped"`
	AplicadoPor    *string    `json:"aplicado_por"`
	AprobadaPor    *string    `json:"aprobada_por"`
	AprobadaAt     *time.Time `json:"aprobada_at"`
}

// ConsignaTrazabilidad es una consigna despachada (o intentada) del lote.
type ConsignaTrazabilidad struct {
	ID                     string    `json:"id"`
	CreadaEn               time.Time `json:"creada_en"`
	TemperaturaObjetivo    float64   `json:"temperatura_objetivo"`
	VelocidadCintaObjetivo float64   `json:"velocidad_cinta_objetivo"`
	Origen                 string    `json:"origen"`
	Usuario                *string   `json:"usuario"`
	DecisionID             *string   `json:"decision_id,omitempty"`
	Estado                 string    `json:"estado"`
	Exitosa                bool      `json:"exitosa"`
	MotivoError            *string   `json:"motivo_error"`
	TemperaturaPrevia      *float64  `json:"temperatura_previa"`
	VelocidadCintaPrevia   *float64  `json:"velocidad_cinta_previa"`
}

// AlertaTrazabilidad es una alerta asociada al lote.
type AlertaTrazabilidad struct {
	ID        string     `json:"id"`
	CreadaEn  time.Time  `json:"creada_en"`
	Tipo      string     `json:"tipo"`
	Severidad string     `json:"severidad"`
	Estado    string     `json:"estado"`
	Mensaje   string     `json:"mensaje"`
	AckedAt   *time.Time `json:"acked_at"`
}

// Trazabilidad es el cuerpo de GET /api/v1/reportes/trazabilidad.
type Trazabilidad struct {
	Lote            LoteTrazabilidad             `json:"lote"`
	Proceso         ProcesoTrazabilidad          `json:"proceso"`
	ConfianzaClases []ConfianzaClaseTrazabilidad `json:"confianza_clases"`
	Decisiones      []DecisionTrazabilidad       `json:"decisiones"`
	Consignas       []ConsignaTrazabilidad       `json:"consignas"`
	Alertas         []AlertaTrazabilidad         `json:"alertas"`
	GeneradoEn      time.Time                    `json:"generado_en"`
}

// AlertaReporte es una fila del export de alertas del período.
type AlertaReporte struct {
	ID             string     `json:"id"`
	CreadaEn       time.Time  `json:"creada_en"`
	SectorID       *string    `json:"sector_id"`
	SectorNombre   string     `json:"sector_nombre"`
	LoteID         *string    `json:"lote_id"`
	ProductoID     *string    `json:"producto_id"`
	ProductoNombre string     `json:"producto_nombre"`
	Turno          *string    `json:"turno"`
	Tipo           string     `json:"tipo"`
	Severidad      string     `json:"severidad"`
	Estado         string     `json:"estado"`
	Mensaje        string     `json:"mensaje"`
	AckedAt        *time.Time `json:"acked_at"`
	AckedPor       *string    `json:"acked_por"`
	MTTAMin        *float64   `json:"mtta_min"`
}

// AlertasParams filtra el export de alertas del período.
type AlertasParams struct {
	Periodo Periodo
	Filtros Filtros
	Estado  *string
	Limite  int
}

// PaginaAlertas es el cuerpo de GET /api/v1/reportes/alertas. Truncada indica
// que el total supera las filas devueltas por el límite.
type PaginaAlertas struct {
	Total    int             `json:"total"`
	Truncada bool            `json:"truncada"`
	Alertas  []AlertaReporte `json:"alertas"`
}

// Repository define el contrato de persistencia del resumen agregado, del
// detalle paginado de lotes, de la trazabilidad de un lote y del export de
// alertas.
type Repository interface {
	ImpactoEconomico(ctx context.Context, periodo Periodo, filtros Filtros) (*ImpactoEconomico, error)
	Resumen(ctx context.Context, periodo Periodo, filtros Filtros) (*Resumen, error)
	Lotes(ctx context.Context, params LotesParams) ([]LoteReporte, int, error)
	Trazabilidad(ctx context.Context, loteID string) (*Trazabilidad, error)
	Alertas(ctx context.Context, params AlertasParams) ([]AlertaReporte, int, error)
}
