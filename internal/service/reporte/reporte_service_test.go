package reporte

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reporte"
)

// mockReporteRepo implementa reporte.Repository en memoria para tests unitarios.
type mockReporteRepo struct {
	kpiToReturn *reporte.KPIFinanciero
	errToReturn error
}

func (m *mockReporteRepo) GetKPIFinanciero(ctx context.Context, filtro reporte.FiltroKPI) (*reporte.KPIFinanciero, error) {
	if m.errToReturn != nil {
		return nil, m.errToReturn
	}
	return m.kpiToReturn, nil
}

func floatPtr(f float64) *float64 {
	return &f
}

func TestCalcularKPIFinanciero_HappyPath(t *testing.T) {
	costoTostada := 150.0
	costoLactal := 420.0

	expectedKPI := &reporte.KPIFinanciero{
		TotalImpactoEconomico:  150.0*10 + 420.0*5, // 1500 + 2100 = 3600.00
		Moneda:                 "ARS",
		TotalMermasUnidades:    15,
		TotalMermasConCosto:    15,
		TotalMermasSinCosto:    0,
		TotalLotes:             5,
		LotesSinCosto:          0,
		TotalProductos:         2,
		ProductosSinCostoCount: 0,
		TieneCostosFaltantes:   false,
		Advertencia:            "",
		DesgloseProductos: []reporte.DesgloseProducto{
			{
				ProductoID:            "prod-1",
				ProductoNombre:        "Tostada Integral",
				TotalMermasUnidades:   10,
				CostoUnitarioPromedio: &costoTostada,
				ImpactoEconomico:      1500.0,
				TieneCostoConfigurado: true,
				LotesTotales:          3,
				LotesSinCosto:         0,
			},
			{
				ProductoID:            "prod-2",
				ProductoNombre:        "Pan Lactal",
				TotalMermasUnidades:   5,
				CostoUnitarioPromedio: &costoLactal,
				ImpactoEconomico:      2100.0,
				TieneCostoConfigurado: true,
				LotesTotales:          2,
				LotesSinCosto:         0,
			},
		},
		CalculadoAt: time.Now().UTC(),
	}

	repo := &mockReporteRepo{kpiToReturn: expectedKPI}
	svc := NewService(repo)

	inicio := time.Now().Add(-24 * time.Hour)
	fin := time.Now()
	res, err := svc.CalcularKPIFinanciero(context.Background(), reporte.FiltroKPI{
		FechaInicio: &inicio,
		FechaFin:    &fin,
		Turno:       "mañana",
	})

	require.NoError(t, err)
	assert.Equal(t, 3600.0, res.TotalImpactoEconomico)
	assert.Equal(t, "ARS", res.Moneda)
	assert.False(t, res.TieneCostosFaltantes)
	assert.Empty(t, res.Advertencia)
	assert.Len(t, res.DesgloseProductos, 2)
}

func TestCalcularKPIFinanciero_PartialCostosMissing(t *testing.T) {
	expectedKPI := &reporte.KPIFinanciero{
		TotalImpactoEconomico:  1500.0, // Solo suma prod-1 con costo
		Moneda:                 "ARS",
		TotalMermasUnidades:    20,
		TotalMermasConCosto:    10,
		TotalMermasSinCosto:    10,
		TotalLotes:             4,
		LotesSinCosto:          2,
		TotalProductos:         2,
		ProductosSinCostoCount: 1,
		TieneCostosFaltantes:   true,
		Advertencia:            "Cálculo parcial: hay 1 producto con costo no configurado",
		DesgloseProductos: []reporte.DesgloseProducto{
			{
				ProductoID:            "prod-1",
				ProductoNombre:        "Tostada Integral",
				TotalMermasUnidades:   10,
				CostoUnitarioPromedio: floatPtr(150.0),
				ImpactoEconomico:      1500.0,
				TieneCostoConfigurado: true,
				LotesTotales:          2,
				LotesSinCosto:         0,
			},
			{
				ProductoID:            "prod-2",
				ProductoNombre:        "Pan Dulce (Sin Costo)",
				TotalMermasUnidades:   10,
				CostoUnitarioPromedio: nil,
				ImpactoEconomico:      0.0,
				TieneCostoConfigurado: false,
				LotesTotales:          2,
				LotesSinCosto:         2,
			},
		},
		CalculadoAt: time.Now().UTC(),
	}

	repo := &mockReporteRepo{kpiToReturn: expectedKPI}
	svc := NewService(repo)

	res, err := svc.CalcularKPIFinanciero(context.Background(), reporte.FiltroKPI{})

	require.NoError(t, err)
	assert.Equal(t, 1500.0, res.TotalImpactoEconomico)
	assert.True(t, res.TieneCostosFaltantes)
	assert.Equal(t, 1, res.ProductosSinCostoCount)
	assert.Contains(t, res.Advertencia, "Cálculo parcial: hay 1 producto con costo no configurado")
}

func TestCalcularKPIFinanciero_LargeVolumesPrecision(t *testing.T) {
	// 500,000 defect units * 324.75 ARS = 162,375,000.00 ARS
	expectedKPI := &reporte.KPIFinanciero{
		TotalImpactoEconomico:  162375000.00,
		Moneda:                 "ARS",
		TotalMermasUnidades:    500000,
		TotalMermasConCosto:    500000,
		TotalMermasSinCosto:    0,
		TotalLotes:             1000,
		LotesSinCosto:          0,
		TotalProductos:         1,
		ProductosSinCostoCount: 0,
		TieneCostosFaltantes:   false,
		CalculadoAt:            time.Now().UTC(),
	}

	repo := &mockReporteRepo{kpiToReturn: expectedKPI}
	svc := NewService(repo)

	res, err := svc.CalcularKPIFinanciero(context.Background(), reporte.FiltroKPI{})
	require.NoError(t, err)
	assert.Equal(t, 162375000.00, res.TotalImpactoEconomico)
}

func TestCalcularKPIFinanciero_InvalidDateRange(t *testing.T) {
	repo := &mockReporteRepo{}
	svc := NewService(repo)

	inicio := time.Now()
	fin := inicio.Add(-1 * time.Hour) // Fin anterior a inicio

	_, err := svc.CalcularKPIFinanciero(context.Background(), reporte.FiltroKPI{
		FechaInicio: &inicio,
		FechaFin:    &fin,
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrRangoFechasInvalido))
}

func TestCalcularKPIFinanciero_InvalidTurno(t *testing.T) {
	repo := &mockReporteRepo{}
	svc := NewService(repo)

	_, err := svc.CalcularKPIFinanciero(context.Background(), reporte.FiltroKPI{
		Turno: "madrugada", // Turno no reconocido
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTurnoInvalido))
}
