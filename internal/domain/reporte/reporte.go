package reporte

import (
	"context"
	"time"
)

// FiltroKPI representa los criterios de filtrado para el cálculo del KPI financiero.
type FiltroKPI struct {
	FechaInicio *time.Time `json:"fechaInicio,omitempty"`
	FechaFin    *time.Time `json:"fechaFin,omitempty"`
	ProductoID  string     `json:"productoId,omitempty"`
	Turno       string     `json:"turno,omitempty"`
}

// DesgloseProducto detalla el impacto económico y el estado de costos para una variedad de producto.
type DesgloseProducto struct {
	ProductoID              string   `json:"productoId"`
	ProductoNombre          string   `json:"productoNombre"`
	TotalMermasUnidades     int      `json:"totalMermasUnidades"`
	CostoUnitarioPromedio   *float64 `json:"costoUnitarioPromedio,omitempty"`
	CostoConfiguradoVigente *float64 `json:"costoConfiguradoVigente,omitempty"`
	ImpactoEconomico        float64  `json:"impactoEconomico"`
	TieneCostoConfigurado   bool     `json:"tieneCostoConfigurado"`
	LotesTotales            int      `json:"lotesTotales"`
	LotesSinCosto           int      `json:"lotesSinCosto"`
}

// KPIFinanciero consolida la pérdida económica acumulada por mermas y métricas asociadas.
type KPIFinanciero struct {
	TotalImpactoEconomico  float64            `json:"totalImpactoEconomico"`
	Moneda                 string             `json:"moneda"`
	TotalMermasUnidades    int                `json:"totalMermasUnidades"`
	TotalMermasConCosto    int                `json:"totalMermasConCosto"`
	TotalMermasSinCosto    int                `json:"totalMermasSinCosto"`
	TotalLotes             int                `json:"totalLotes"`
	LotesSinCosto          int                `json:"lotesSinCosto"`
	TotalProductos         int                `json:"totalProductos"`
	ProductosSinCostoCount int                `json:"productosSinCostoCount"`
	TieneCostosFaltantes   bool               `json:"tieneCostosFaltantes"`
	Advertencia            string             `json:"advertencia,omitempty"`
	DesgloseProductos      []DesgloseProducto `json:"desgloseProductos"`
	CalculadoAt            time.Time          `json:"calculadoAt"`
}

// Repository define el contrato de persistencia para consultas de reportes y KPIs.
type Repository interface {
	GetKPIFinanciero(ctx context.Context, filtro FiltroKPI) (*KPIFinanciero, error)
}

// Service define los casos de uso de negocio para reportes y KPIs.
type Service interface {
	CalcularKPIFinanciero(ctx context.Context, filtro FiltroKPI) (*KPIFinanciero, error)
}
