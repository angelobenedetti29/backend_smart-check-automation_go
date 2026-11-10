package reportes

// ImpactoEconomico valoriza únicamente las mermas detectadas (crudas +
// quemadas), al costo ACTUAL de cada producto. No es contabilidad histórica.
// PerdidaARS es null si hay merma sin costo o lotes sin conteos de defectos;
// PerdidaValorizadaARS conserva el subtotal que sí se puede calcular.
type ImpactoEconomico struct {
	Moneda               string          `json:"moneda"`
	ReferenciaCosto      string          `json:"referencia_costo"`
	PerdidaARS           *float64        `json:"perdida_ars"`
	PerdidaValorizadaARS float64         `json:"perdida_valorizada_ars"`
	UnidadesMerma        int64           `json:"unidades_merma"`
	UnidadesSinCosto     int64           `json:"unidades_sin_costo"`
	LotesSinConteos      int             `json:"lotes_sin_conteos"`
	PorProducto          []MermaProducto `json:"por_producto"`
}

type MermaProducto struct {
	ProductoID       string   `json:"producto_id"`
	Nombre           string   `json:"nombre"`
	CostoUnitarioARS *float64 `json:"costo_unitario_ars"`
	UnidadesMerma    int64    `json:"unidades_merma"`
	LotesSinConteos  int      `json:"lotes_sin_conteos"`
	PerdidaARS       *float64 `json:"perdida_ars"`
}

type ReporteEconomico struct {
	Periodo          PeriodoResumen   `json:"periodo"`
	Filtros          FiltrosResumen   `json:"filtros"`
	ImpactoEconomico ImpactoEconomico `json:"impacto_economico"`
}
