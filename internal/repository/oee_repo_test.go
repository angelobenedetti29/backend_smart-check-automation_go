package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	oeeDomain "github.com/angelobenedetti29/smart-check-automation/internal/domain/oee"
	oeeService "github.com/angelobenedetti29/smart-check-automation/internal/service/oee"
)

// TestOEERepo_DatosIntegracion cubre el snapshot completo del OEE: plan y
// paradas que solapan el período, lotes CERRADOS dentro del período, energía,
// capacidades y nombres de sectores, y el resumen resultante.
func TestOEERepo_DatosIntegracion(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)
	art := time.FixedZone("ART", -3*60*60)

	const (
		sectorID   = "sector-oee"
		productoID = "aaaaaaaa-0000-0000-0000-0000000000ee"
	)

	_, err := pool.Exec(ctx, `
		INSERT INTO productos (
			id, nombre, activo, peso_referencia_kg, tolerancia_peso_pct,
			dimension_base_cm, tolerancia_dimension_cm,
			temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max,
			capacidad_nominal_kg_h
		) VALUES ($1, 'Tostada OEE', true, 0.25, 5, 12, 0.5, 180, 220, 0.1, 0.3, 100)`,
		productoID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sectores (id, nombre) VALUES ($1, 'Sector OEE')`, sectorID)
	require.NoError(t, err)

	// 8 h dentro del período + 2 h que empiezan el día anterior.
	_, err = pool.Exec(ctx, `
		INSERT INTO plan_produccion (sector_id, producto_id, fecha, turno, inicio_at, fin_at, kg_plan)
		VALUES
			($1, $2, '2026-10-01', 'mañana', $3, $4, 500),
			($1, $2, '2026-09-30', 'noche', $5, $6, 200)`,
		sectorID, productoID,
		time.Date(2026, 10, 1, 6, 0, 0, 0, art),
		time.Date(2026, 10, 1, 14, 0, 0, 0, art),
		time.Date(2026, 9, 30, 22, 0, 0, 0, art),
		time.Date(2026, 10, 1, 2, 0, 0, 0, art))
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO eventos_parada (sector_id, inicio_at, fin_at, causa_codigo, tipo)
		VALUES
			($1, $2, $3, 'FALLA', 'NO_PLANIFICADA'),
			($1, $4, $5, 'MANTENIMIENTO', 'PLANIFICADA')`,
		sectorID,
		time.Date(2026, 10, 1, 10, 0, 0, 0, art),
		time.Date(2026, 10, 1, 11, 0, 0, 0, art),
		time.Date(2026, 10, 1, 12, 0, 0, 0, art),
		time.Date(2026, 10, 1, 13, 0, 0, 0, art))
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO lotes_productivos (
			producto_id, sector_id, estado, turno, inicio_at, fin_at,
			total_unidades, correctos, crudas, quemados,
			correctos_kg, crudos_kg, quemados_kg
		) VALUES ($1, $2, 'CERRADO', 'mañana', $3, $4, 100, 90, 5, 5, 90, 5, 5)`,
		productoID, sectorID,
		time.Date(2026, 10, 1, 9, 0, 0, 0, art),
		time.Date(2026, 10, 1, 10, 0, 0, 0, art))
	require.NoError(t, err)

	// Lote ABIERTO: no debe entrar en el OEE.
	_, err = pool.Exec(ctx, `
		INSERT INTO lotes_productivos (
			producto_id, sector_id, estado, turno, inicio_at,
			total_unidades, correctos, quemados, correctos_kg, quemados_kg
		) VALUES ($1, $2, 'ABIERTO', 'tarde', $3, 0, 0, 0, 0, 0)`,
		productoID, sectorID, time.Date(2026, 10, 1, 15, 0, 0, 0, art))
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO lecturas_energia (sector_id, periodo_inicio, periodo_fin, kwh, gas_m3)
		VALUES
			($1, $2, $3, 300, 10),
			($1, $4, $5, 100, NULL),
			($1, $6, $7, 200, 4)`,
		sectorID,
		time.Date(2026, 10, 1, 0, 0, 0, 0, art),
		time.Date(2026, 10, 1, 8, 0, 0, 0, art),
		time.Date(2026, 10, 1, 20, 0, 0, 0, art),
		time.Date(2026, 10, 1, 23, 0, 0, 0, art),
		time.Date(2026, 9, 30, 20, 0, 0, 0, art),
		time.Date(2026, 10, 1, 4, 0, 0, 0, art))
	require.NoError(t, err)

	repo := NewOEERepository(pool)
	periodo := oeeDomain.Periodo{
		Desde: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
		Hasta: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
	}

	datos, err := repo.Datos(ctx, oeeDomain.Query{Periodo: periodo})
	require.NoError(t, err)
	require.Len(t, datos.Plan, 2)
	require.Len(t, datos.Paradas, 2)
	require.Len(t, datos.Energia, 3)
	// ORDER BY periodo_inicio: la lectura parcial del día anterior va primero.
	require.True(t, time.Date(2026, 9, 30, 20, 0, 0, 0, art).Equal(datos.Energia[0].PeriodoInicio))
	require.True(t, time.Date(2026, 10, 1, 4, 0, 0, 0, art).Equal(datos.Energia[0].PeriodoFin))
	require.True(t, time.Date(2026, 10, 1, 0, 0, 0, 0, art).Equal(datos.Energia[1].PeriodoInicio))
	require.True(t, time.Date(2026, 10, 1, 8, 0, 0, 0, art).Equal(datos.Energia[1].PeriodoFin))
	require.True(t, time.Date(2026, 10, 1, 20, 0, 0, 0, art).Equal(datos.Energia[2].PeriodoInicio))
	require.True(t, time.Date(2026, 10, 1, 23, 0, 0, 0, art).Equal(datos.Energia[2].PeriodoFin))
	require.Len(t, datos.Lotes, 1)
	require.Equal(t, 100.0, *datos.Capacidades[productoID])
	require.Equal(t, "Sector OEE", datos.Sectores[sectorID])
	require.InDelta(t, 100.0, datos.Lotes[0].KgTotal, 1e-9)
	require.InDelta(t, 90.0, datos.Lotes[0].KgOK, 1e-9)

	// Mismo snapshot a través del servicio: verifica el resumen completo.
	resumen, err := oeeService.NewService(repo).OEE(ctx, oeeService.Query{
		Desde: "2026-10-01", Hasta: "2026-10-01",
	})
	require.NoError(t, err)

	g := resumen.Global
	require.True(t, g.Calculable)
	require.InDelta(t, 90.0, *g.AvailabilityPct, 1e-9)
	require.InDelta(t, 10.0, *g.PerformancePct, 1e-9)
	require.InDelta(t, 90.0, *g.QualityPct, 1e-9)
	require.InDelta(t, 8.1, *g.OEEPct, 1e-9)
	require.InDelta(t, 600.0, g.PlannedTimeMin, 1e-9)
	require.InDelta(t, 60.0, g.PlannedDowntimeMin, 1e-9)
	require.InDelta(t, 60.0, g.UnplannedDowntimeMin, 1e-9)
	require.InDelta(t, 540.0, g.OperatingTimeMin, 1e-9)
	require.InDelta(t, 600.0, g.KgPlan, 1e-9)
	require.InDelta(t, 100.0, g.KgProducidos, 1e-9)
	require.InDelta(t, 1000.0, g.CapacidadTeoricaKg, 1e-9)
	require.NotNil(t, g.Energy)
	// 300 + 100 completas + 200 * (4h/8h) de la lectura parcial.
	require.InDelta(t, 500.0, g.Energy.Kwh, 1e-9)
	require.InDelta(t, 12.0, *g.Energy.GasM3, 1e-9)
	require.InDelta(t, 5.556, *g.Energy.KwhPorKgOK, 1e-9)
	require.InDelta(t, 0.133, *g.Energy.GasM3PorKgOK, 1e-9)

	require.Len(t, resumen.PorSector, 1)
	require.Equal(t, sectorID, resumen.PorSector[0].SectorID)
	require.Equal(t, "Sector OEE", resumen.PorSector[0].SectorNombre)
	require.InDelta(t, 8.1, *resumen.PorSector[0].OEEPct, 1e-9)
}

// TestOEERepo_FiltroSector verifica el filtro opcional y que un sector sin
// datos se excluya cuando no se filtra.
func TestOEERepo_FiltroSector(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)
	art := time.FixedZone("ART", -3*60*60)

	const (
		sectorA    = "sector-oee-a"
		sectorB    = "sector-oee-b"
		productoID = "aaaaaaaa-0000-0000-0000-0000000000ef"
	)

	_, err := pool.Exec(ctx, `
		INSERT INTO productos (id, nombre, activo, peso_referencia_kg, tolerancia_peso_pct,
			dimension_base_cm, tolerancia_dimension_cm, temp_min, temp_max,
			velocidad_cinta_min, velocidad_cinta_max, capacidad_nominal_kg_h)
		VALUES ($1, 'Tostada OEE 2', true, 0.25, 5, 12, 0.5, 180, 220, 0.1, 0.3, 50)`,
		productoID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sectores (id, nombre) VALUES ($1, 'A'), ($2, 'B')`, sectorA, sectorB)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO plan_produccion (sector_id, producto_id, fecha, turno, inicio_at, fin_at, kg_plan)
		VALUES ($1, $2, '2026-10-01', 'mañana', $3, $4, 100)`,
		sectorA, productoID,
		time.Date(2026, 10, 1, 6, 0, 0, 0, art),
		time.Date(2026, 10, 1, 8, 0, 0, 0, art))
	require.NoError(t, err)

	repo := NewOEERepository(pool)
	periodo := oeeDomain.Periodo{
		Desde: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
		Hasta: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
	}

	filtro := sectorB
	datos, err := repo.Datos(ctx, oeeDomain.Query{
		Periodo: periodo,
		Filtros: oeeDomain.Filtros{SectorID: &filtro},
	})
	require.NoError(t, err)
	require.Empty(t, datos.Plan)
	require.Equal(t, "B", datos.Sectores[sectorB])
	require.Equal(t, "A", datos.Sectores[sectorA])

	sinFiltro, err := repo.Datos(ctx, oeeDomain.Query{Periodo: periodo})
	require.NoError(t, err)
	require.Len(t, sinFiltro.Plan, 1)
}
