package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reportes"
)

// TestReportesRepo_ResumenIntegracion cubre los bloques principales del
// resumen sobre datos reales: producción cerrada/abierta, calidad, proceso,
// alertas y automatización.
func TestReportesRepo_ResumenIntegracion(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)

	art := time.FixedZone("ART", -3*60*60)
	_, err := pool.Exec(ctx, `
		INSERT INTO productos (
			id, nombre, activo, peso_referencia_kg, tolerancia_peso_pct,
			dimension_base_cm, tolerancia_dimension_cm,
			temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max
		) VALUES ($1, 'Tostada Reportes', true, 0.25, 5, 12, 0.5, 180, 220, 0.1, 0.3)`, testProductoID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sectores (id, nombre) VALUES ($1, 'Sector Reportes')`, testSectorID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO dispositivos (id, nombre, tipo, sector_id) VALUES
			($1, 'pi-entrada-reportes', 'ENTRADA_HORNO', $3),
			($2, 'pi-salida-reportes', 'SALIDA_HORNO', $3)`,
		testEntradaDevID, testSalidaDevID, testSectorID)
	require.NoError(t, err)

	// Lote 1: AUTOMATICO, 60 min, 100u (90/6/4), confianza 0.8.
	var lote1 string
	err = pool.QueryRow(ctx, `
		INSERT INTO lotes_productivos (
			producto_id, sector_id, estado, turno, inicio_at, fin_at,
			total_unidades, correctos, crudas, quemados,
			correctos_kg, crudos_kg, quemados_kg,
			modo, tolerancia_temp_c, umbral_aceptacion_crudo, umbral_aceptacion_quemado,
			temp1_objetivo, vel_objetivo, confianza_promedio
		) VALUES ($1,$2,'CERRADO','mañana',$3,$4,100,90,6,4,22.5,1.5,1.0,
		          'AUTOMATICO',2.0,5.0,5.0,180.0,0.200,0.8)
		RETURNING id`,
		testProductoID, testSectorID,
		time.Date(2026, 10, 1, 10, 0, 0, 0, art),
		time.Date(2026, 10, 1, 11, 0, 0, 0, art),
	).Scan(&lote1)
	require.NoError(t, err)

	// Lote 2: MANUAL, 120 min, 200u (190/4/6), confianza 0.9, conforme.
	var lote2 string
	err = pool.QueryRow(ctx, `
		INSERT INTO lotes_productivos (
			producto_id, sector_id, estado, turno, inicio_at, fin_at,
			total_unidades, correctos, crudas, quemados,
			correctos_kg, crudos_kg, quemados_kg,
			modo, tolerancia_temp_c, umbral_aceptacion_crudo, umbral_aceptacion_quemado,
			temp1_objetivo, vel_objetivo, confianza_promedio
		) VALUES ($1,$2,'CERRADO','tarde',$3,$4,200,190,4,6,47.5,1.0,1.5,
		          'MANUAL',2.0,5.0,5.0,180.0,0.200,0.9)
		RETURNING id`,
		testProductoID, testSectorID,
		time.Date(2026, 10, 1, 13, 0, 0, 0, art),
		time.Date(2026, 10, 1, 15, 0, 0, 0, art),
	).Scan(&lote2)
	require.NoError(t, err)

	// Lote ABIERTO: no suma unidades/kg, sólo lotes_abiertos.
	_, err = pool.Exec(ctx, `
		INSERT INTO lotes_productivos (producto_id, sector_id, estado, inicio_at, abierto_por)
		VALUES ($1, $2, 'ABIERTO', $3, $4)`,
		testProductoID, testSectorID, time.Date(2026, 10, 1, 12, 0, 0, 0, art), testEntradaDevID)
	require.NoError(t, err)

	// Proceso: diffs de temp 0/2/4 (tolerancia 2 → 2/3) y vel 0/0.02/0.04
	// (delta default del producto 0.05 → 3/3).
	for i, temp := range []float64{180, 182, 184} {
		_, err = pool.Exec(ctx, `
			INSERT INTO metricas_horno (
				lote_id, dispositivo_id, sector_id, temp_horno_1, velocidad_cinta, received_at
			) VALUES ($1,$2,$3,$4,$5,$6)`,
			lote1, testEntradaDevID, testSectorID, temp, 0.20+float64(i)*0.02,
			time.Date(2026, 10, 1, 10+i*5, 0, 0, 0, art))
		require.NoError(t, err)
	}

	// Alertas: una ACK (MTTA 10 min) y una ACTIVA.
	_, err = pool.Exec(ctx, `
		INSERT INTO alertas (sector_id, lote_id, tipo, severidad, estado, mensaje, created_at, acked_at)
		VALUES ($1,$2,'TEMPERATURA_HORNO','CRITICAL','ACK','alta temp',$3,$4)`,
		testSectorID, lote1,
		time.Date(2026, 10, 1, 10, 0, 0, 0, art),
		time.Date(2026, 10, 1, 10, 10, 0, 0, art))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO alertas (sector_id, lote_id, tipo, severidad, estado, mensaje, created_at)
		VALUES ($1,$2,'CONSIGNA_ENLACE','WARNING','ACTIVA','sin enlace',$3)`,
		testSectorID, lote1, time.Date(2026, 10, 1, 11, 0, 0, 0, art))
	require.NoError(t, err)

	// Decisiones: 2 MPC + 1 NADIE, con z para el MAE.
	decisiones := []struct {
		aplicado string
		zObs     *float64
		zPred    *float64
	}{
		{"MPC", floatPtr(0.3), floatPtr(0.2)},
		{"MPC", floatPtr(0.5), floatPtr(0.4)},
		{"NADIE", floatPtr(0.2), floatPtr(0.2)},
	}
	for _, d := range decisiones {
		_, err = pool.Exec(ctx, `
			INSERT INTO decisiones_control (
				lote_id, ventana_inicio, ventana_fin, muestras,
				temp_actual, vel_actual,
				pct_crudos_obs, pct_ok_obs, pct_quemados_obs, conf_promedio,
				temp_propuesto, vel_propuesto,
				aplicado_por, z_observado, z_predicho, created_at
			) VALUES ($1,$2,$3,10,180,0.2,2,95,3,0.9,181,0.21,$4,$5,$6,$7)`,
			lote1,
			time.Date(2026, 10, 1, 10, 0, 0, 0, art),
			time.Date(2026, 10, 1, 10, 5, 0, 0, art),
			d.aplicado, d.zObs, d.zPred,
			time.Date(2026, 10, 1, 10, 5, 0, 0, art))
		require.NoError(t, err)
	}

	repo := NewReportesPostgresRepository(pool)
	res, err := repo.Resumen(ctx, reportes.Periodo{
		Desde: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
		Hasta: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
	}, reportes.Filtros{})
	require.NoError(t, err)
	require.NotNil(t, res)

	// Producción.
	require.Equal(t, 2, res.Produccion.LotesCerrados)
	require.Equal(t, 1, res.Produccion.LotesAbiertos)
	require.Equal(t, 3, res.Produccion.LotesTotal)
	require.Equal(t, 300, res.Produccion.Unidades)
	require.Equal(t, 280, res.Produccion.UnidadesOK)
	require.Equal(t, 10, res.Produccion.UnidadesCrudas)
	require.Equal(t, 10, res.Produccion.UnidadesQuemadas)
	require.InDelta(t, 75.0, res.Produccion.KgTotalEstimados, 0.0001)
	require.InDelta(t, 70.0, res.Produccion.KgOKEstimados, 0.0001)
	require.InDelta(t, 5.0, res.Produccion.KgMermaEstimados, 0.0001)
	require.NotNil(t, res.Produccion.DuracionMediaMin)
	require.InDelta(t, 90, *res.Produccion.DuracionMediaMin, 0.0001)
	require.Len(t, res.Produccion.PorTurno, 2)

	// Calidad.
	require.NotNil(t, res.Calidad.FPYPct)
	require.InDelta(t, 93.3333, *res.Calidad.FPYPct, 0.01)
	require.NotNil(t, res.Calidad.ConfianzaPromedio)
	require.InDelta(t, 0.8667, *res.Calidad.ConfianzaPromedio, 0.01)
	require.NotNil(t, res.Calidad.LotesConformesPct)
	require.InDelta(t, 50.0, *res.Calidad.LotesConformesPct, 0.01)
	require.Len(t, res.Calidad.SerieDiaria, 1)
	require.Equal(t, "2026-10-01", res.Calidad.SerieDiaria[0].Fecha)

	// Proceso.
	require.NotNil(t, res.Proceso.TempMAEC)
	require.InDelta(t, 2.0, *res.Proceso.TempMAEC, 0.0001)
	require.NotNil(t, res.Proceso.TempEnToleranciaPct)
	require.InDelta(t, 66.6667, *res.Proceso.TempEnToleranciaPct, 0.01)
	require.NotNil(t, res.Proceso.VelMAE)
	require.InDelta(t, 0.02, *res.Proceso.VelMAE, 0.0001)
	require.NotNil(t, res.Proceso.VelEnToleranciaPct)
	require.InDelta(t, 100.0, *res.Proceso.VelEnToleranciaPct, 0.01)
	require.Len(t, res.Proceso.SerieDiaria, 1)

	// Alertas.
	require.Equal(t, 2, res.Alertas.Total)
	require.Equal(t, 1, res.Alertas.PorSeveridad["CRITICAL"])
	require.Equal(t, 1, res.Alertas.PorSeveridad["WARNING"])
	require.NotNil(t, res.Alertas.AckPct)
	require.InDelta(t, 50.0, *res.Alertas.AckPct, 0.01)
	require.NotNil(t, res.Alertas.MTTAMin)
	require.InDelta(t, 10.0, *res.Alertas.MTTAMin, 0.0001)
	require.Len(t, res.Alertas.SerieDiaria, 1)

	// Automatización.
	require.Equal(t, 3, res.Automatizacion.DecisionesTotal)
	require.NotNil(t, res.Automatizacion.MPCPct)
	require.InDelta(t, 66.6667, *res.Automatizacion.MPCPct, 0.01)
	require.NotNil(t, res.Automatizacion.SinAplicarPct)
	require.InDelta(t, 33.3333, *res.Automatizacion.SinAplicarPct, 0.01)
	require.NotNil(t, res.Automatizacion.ZMAE)
	require.InDelta(t, 0.0667, *res.Automatizacion.ZMAE, 0.001)
	require.NotNil(t, res.Automatizacion.ModoPct["AUTOMATICO"])
	require.InDelta(t, 33.3333, *res.Automatizacion.ModoPct["AUTOMATICO"], 0.01)
	require.NotNil(t, res.Automatizacion.ModoPct["MANUAL"])
	require.InDelta(t, 66.6667, *res.Automatizacion.ModoPct["MANUAL"], 0.01)

	// El segundo lote existe y no quedó fuera del rango por accidente.
	require.NotEmpty(t, lote2)

	// Filtros: turno tarde deja sólo el lote 2 y excluye alertas/decisiones del
	// lote 1. Ejercita los placeholders dinámicos de todos los bloques.
	sector, producto, turno := testSectorID, testProductoID, "tarde"
	filtrado, err := repo.Resumen(ctx, reportes.Periodo{
		Desde: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
		Hasta: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
	}, reportes.Filtros{SectorID: &sector, ProductoID: &producto, Turno: &turno})
	require.NoError(t, err)
	require.Equal(t, 1, filtrado.Produccion.LotesCerrados)
	require.Equal(t, 200, filtrado.Produccion.Unidades)
	require.Equal(t, 0, filtrado.Alertas.Total)
	require.Equal(t, 0, filtrado.Automatizacion.DecisionesTotal)
}

// TestReportesRepo_LotesIntegracion cubre el detalle paginado: orden, estados,
// kg/fpy/duración, filtros y cursor sin repetir filas.
func TestReportesRepo_LotesIntegracion(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)

	const (
		producto2 = "aaaaaaaa-0000-0000-0000-000000000002"
		sector2   = "sector-test-2"
	)
	art := time.FixedZone("ART", -3*60*60)

	_, err := pool.Exec(ctx, `
		INSERT INTO productos (id, nombre, activo, peso_referencia_kg, tolerancia_peso_pct,
			dimension_base_cm, tolerancia_dimension_cm, temp_min, temp_max,
			velocidad_cinta_min, velocidad_cinta_max) VALUES
			($1, 'Tostada Reportes', true, 0.25, 5, 12, 0.5, 180, 220, 0.1, 0.3),
			($2, 'Tostada Reportes 2', true, 0.25, 5, 12, 0.5, 180, 220, 0.1, 0.3)`,
		testProductoID, producto2)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO sectores (id, nombre) VALUES ($1, 'Sector Reportes'), ($2, 'Sector Reportes 2')`,
		testSectorID, sector2)
	require.NoError(t, err)

	inicio1 := time.Date(2026, 10, 1, 10, 0, 0, 0, art)
	inicio2 := time.Date(2026, 10, 2, 14, 0, 0, 0, art)
	inicio3 := time.Date(2026, 10, 3, 22, 0, 0, 0, art)

	var lote1, lote2, lote3 string
	err = pool.QueryRow(ctx, `
		INSERT INTO lotes_productivos (
			producto_id, sector_id, estado, turno, inicio_at, fin_at,
			total_unidades, correctos, crudas, quemados,
			correctos_kg, crudos_kg, quemados_kg, modo, confianza_promedio
		) VALUES ($1,$2,'CERRADO','mañana',$3,$4,100,90,6,4,22.5,1.5,1.0,'AUTOMATICO',0.8)
		RETURNING id`,
		testProductoID, testSectorID, inicio1, inicio1.Add(time.Hour)).Scan(&lote1)
	require.NoError(t, err)

	err = pool.QueryRow(ctx, `
		INSERT INTO lotes_productivos (
			producto_id, sector_id, estado, turno, inicio_at, fin_at,
			total_unidades, correctos, crudas, quemados,
			correctos_kg, crudos_kg, quemados_kg, modo, confianza_promedio
		) VALUES ($1,$2,'CERRADO','tarde',$3,$4,200,190,4,6,47.5,1.0,1.5,'MANUAL',0.9)
		RETURNING id`,
		testProductoID, testSectorID, inicio2, inicio2.Add(2*time.Hour)).Scan(&lote2)
	require.NoError(t, err)

	// ABIERTO: conteos/kg parciales (sólo OK) y sin fin_at.
	err = pool.QueryRow(ctx, `
		INSERT INTO lotes_productivos (
			producto_id, sector_id, estado, turno, inicio_at,
			correctos, correctos_kg, modo
		) VALUES ($1,$2,'ABIERTO','noche',$3,10,2.5,'SUPERVISION')
		RETURNING id`,
		producto2, sector2, inicio3).Scan(&lote3)
	require.NoError(t, err)

	repo := NewReportesPostgresRepository(pool)
	periodo := reportes.Periodo{
		Desde: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
		Hasta: time.Date(2026, 10, 3, 0, 0, 0, 0, art),
	}

	lotes, total, err := repo.Lotes(ctx, reportes.LotesParams{Periodo: periodo, Limite: 10})
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, lotes, 3)
	require.Equal(t, lote3, lotes[0].ID, "orden inicio_at DESC")
	require.Equal(t, lote2, lotes[1].ID)
	require.Equal(t, lote1, lotes[2].ID)

	// Lote cerrado: conteos, kg, fpy y duración.
	l1 := lotes[2]
	require.Equal(t, "CERRADO", l1.Estado)
	require.Equal(t, "Sector Reportes", l1.SectorNombre)
	require.Equal(t, "Tostada Reportes", l1.ProductoNombre)
	require.NotNil(t, l1.Turno)
	require.Equal(t, "mañana", *l1.Turno)
	require.NotNil(t, l1.CerradoEn)
	require.NotNil(t, l1.DuracionMin)
	require.InDelta(t, 60.0, *l1.DuracionMin, 0.0001)
	require.Equal(t, 90, *l1.Conteos.OK)
	require.Equal(t, 6, *l1.Conteos.Crudo)
	require.Equal(t, 4, *l1.Conteos.Quemado)
	require.Equal(t, 100, l1.Conteos.Total)
	require.InDelta(t, 22.5, *l1.Kg.OK, 0.0001)
	require.InDelta(t, 1.5, *l1.Kg.Crudo, 0.0001)
	require.InDelta(t, 1.0, *l1.Kg.Quemado, 0.0001)
	require.InDelta(t, 25.0, l1.Kg.Total, 0.0001)
	require.InDelta(t, 2.5, l1.Kg.Merma, 0.0001)
	require.NotNil(t, l1.FPYPct)
	require.InDelta(t, 90.0, *l1.FPYPct, 0.0001)
	require.InDelta(t, 6.0, *l1.CrudoPct, 0.0001)
	require.InDelta(t, 4.0, *l1.QuemadoPct, 0.0001)

	// Lote abierto: sin cierre/duración, buckets nulos preservados y total
	// derivado de la suma de buckets.
	abierto := lotes[0]
	require.Equal(t, "ABIERTO", abierto.Estado)
	require.Nil(t, abierto.CerradoEn)
	require.Nil(t, abierto.MotivoCierre)
	require.Nil(t, abierto.DuracionMin)
	require.Nil(t, abierto.Conteos.Crudo)
	require.Nil(t, abierto.Conteos.Quemado)
	require.Equal(t, 10, abierto.Conteos.Total)
	require.Nil(t, abierto.Kg.Crudo)
	require.Nil(t, abierto.Kg.Quemado)
	require.InDelta(t, 2.5, abierto.Kg.Total, 0.0001)
	require.InDelta(t, 0.0, abierto.Kg.Merma, 0.0001)
	require.InDelta(t, 100.0, *abierto.FPYPct, 0.0001)

	// Filtros por turno y sector.
	turno := "tarde"
	filtrados, totalFiltrado, err := repo.Lotes(ctx, reportes.LotesParams{
		Periodo: periodo, Filtros: reportes.Filtros{Turno: &turno}, Limite: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 1, totalFiltrado)
	require.Len(t, filtrados, 1)
	require.Equal(t, lote2, filtrados[0].ID)

	sectorFiltro := sector2
	porSector, totalSector, err := repo.Lotes(ctx, reportes.LotesParams{
		Periodo: periodo, Filtros: reportes.Filtros{SectorID: &sectorFiltro}, Limite: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 1, totalSector)
	require.Equal(t, lote3, porSector[0].ID)

	// Cursor: la primera página trae los 2 más nuevos; la segunda, con el
	// cursor del último, trae el restante sin repetir.
	primera, totalPrimera, err := repo.Lotes(ctx, reportes.LotesParams{Periodo: periodo, Limite: 2})
	require.NoError(t, err)
	require.Equal(t, 3, totalPrimera)
	require.Len(t, primera, 2)
	require.Equal(t, lote3, primera[0].ID)
	require.Equal(t, lote2, primera[1].ID)

	antesDe := primera[len(primera)-1].AbiertoEn
	segunda, totalSegunda, err := repo.Lotes(ctx, reportes.LotesParams{
		Periodo:   periodo,
		Limite:    2,
		AntesDe:   &antesDe,
		AntesDeID: primera[len(primera)-1].ID,
	})
	require.NoError(t, err)
	require.Equal(t, 3, totalSegunda)
	require.Len(t, segunda, 1)
	require.Equal(t, lote1, segunda[0].ID)
}

// TestReportesRepo_AlertasIntegracion cubre el export de alertas: orden,
// total sin límite, mtta/acked_por, filtros y truncación por límite.
func TestReportesRepo_AlertasIntegracion(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)

	const (
		producto2 = "aaaaaaaa-0000-0000-0000-000000000002"
		sector2   = "sector-test-2"
		usuarioID = "e0000000-0000-0000-0000-000000000001"
	)
	art := time.FixedZone("ART", -3*60*60)

	_, err := pool.Exec(ctx, `
		INSERT INTO productos (id, nombre, activo, peso_referencia_kg, tolerancia_peso_pct,
			dimension_base_cm, tolerancia_dimension_cm, temp_min, temp_max,
			velocidad_cinta_min, velocidad_cinta_max) VALUES
			($1, 'Tostada Reportes', true, 0.25, 5, 12, 0.5, 180, 220, 0.1, 0.3),
			($2, 'Tostada Reportes 2', true, 0.25, 5, 12, 0.5, 180, 220, 0.1, 0.3)`,
		testProductoID, producto2)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO sectores (id, nombre) VALUES ($1, 'Sector Reportes'), ($2, 'Sector Reportes 2')`,
		testSectorID, sector2)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO usuarios (id, email, nombre, rol) VALUES ($1, 'reportes@fermar.com.ar', 'Reportes', 'Supervisor')`,
		usuarioID)
	require.NoError(t, err)

	var lote1, lote2 string
	err = pool.QueryRow(ctx, `
		INSERT INTO lotes_productivos (producto_id, sector_id, estado, turno, inicio_at)
		VALUES ($1,$2,'ABIERTO','mañana',$3) RETURNING id`,
		testProductoID, testSectorID, time.Date(2026, 10, 1, 8, 0, 0, 0, art)).Scan(&lote1)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `
		INSERT INTO lotes_productivos (producto_id, sector_id, estado, turno, inicio_at)
		VALUES ($1,$2,'ABIERTO','tarde',$3) RETURNING id`,
		producto2, sector2, time.Date(2026, 10, 1, 13, 0, 0, 0, art)).Scan(&lote2)
	require.NoError(t, err)

	// Alerta 1 (más vieja, ACK con MTTA 10 min) y alerta 2 (más nueva, ACTIVA).
	_, err = pool.Exec(ctx, `
		INSERT INTO alertas (sector_id, lote_id, tipo, severidad, estado, mensaje, created_at, acked_by, acked_at)
		VALUES ($1,$2,'TEMPERATURA_HORNO','CRITICAL','ACK','alta temp',$3,$4,$5)`,
		testSectorID, lote1,
		time.Date(2026, 10, 1, 10, 0, 0, 0, art), usuarioID,
		time.Date(2026, 10, 1, 10, 10, 0, 0, art))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO alertas (sector_id, lote_id, tipo, severidad, estado, mensaje, created_at)
		VALUES ($1,$2,'CONSIGNA_ENLACE','WARNING','ACTIVA','sin enlace',$3)`,
		sector2, lote2, time.Date(2026, 10, 1, 12, 0, 0, 0, art))
	require.NoError(t, err)

	repo := NewReportesPostgresRepository(pool)
	periodo := reportes.Periodo{
		Desde: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
		Hasta: time.Date(2026, 10, 1, 0, 0, 0, 0, art),
	}

	alertas, total, err := repo.Alertas(ctx, reportes.AlertasParams{Periodo: periodo, Limite: 10})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, alertas, 2)
	require.Equal(t, lote2, *alertas[0].LoteID, "orden created_at DESC")
	require.Equal(t, "ACTIVA", alertas[0].Estado)
	require.Equal(t, "Sector Reportes 2", alertas[0].SectorNombre)
	require.Equal(t, "Tostada Reportes 2", alertas[0].ProductoNombre)
	require.Equal(t, "tarde", *alertas[0].Turno)
	require.Nil(t, alertas[0].AckedAt)
	require.Nil(t, alertas[0].AckedPor)
	require.Nil(t, alertas[0].MTTAMin)

	require.Equal(t, lote1, *alertas[1].LoteID)
	require.Equal(t, "ACK", alertas[1].Estado)
	require.Equal(t, "Sector Reportes", alertas[1].SectorNombre)
	require.Equal(t, "Tostada Reportes", alertas[1].ProductoNombre)
	require.Equal(t, "mañana", *alertas[1].Turno)
	require.NotNil(t, alertas[1].AckedAt)
	require.Equal(t, "reportes@fermar.com.ar", *alertas[1].AckedPor)
	require.NotNil(t, alertas[1].MTTAMin)
	require.InDelta(t, 10.0, *alertas[1].MTTAMin, 0.0001)
	require.Equal(t, time.Date(2026, 10, 1, 10, 0, 0, 0, art).UTC(), alertas[1].CreadaEn.UTC())
	require.Equal(t, "-03:00", alertas[1].CreadaEn.Format("-07:00"))

	// Filtros: sector por a.sector_id, producto/turno vía lote y estado.
	sectorFiltro := testSectorID
	porSector, totalSector, err := repo.Alertas(ctx, reportes.AlertasParams{
		Periodo: periodo, Filtros: reportes.Filtros{SectorID: &sectorFiltro}, Limite: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 1, totalSector)
	require.Equal(t, lote1, *porSector[0].LoteID)

	productoFiltro := producto2
	porProducto, totalProducto, err := repo.Alertas(ctx, reportes.AlertasParams{
		Periodo: periodo, Filtros: reportes.Filtros{ProductoID: &productoFiltro}, Limite: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 1, totalProducto)
	require.Equal(t, lote2, *porProducto[0].LoteID)

	turnoFiltro := "mañana"
	porTurno, totalTurno, err := repo.Alertas(ctx, reportes.AlertasParams{
		Periodo: periodo, Filtros: reportes.Filtros{Turno: &turnoFiltro}, Limite: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 1, totalTurno)
	require.Equal(t, lote1, *porTurno[0].LoteID)

	estadoFiltro := "ACK"
	porEstado, totalEstado, err := repo.Alertas(ctx, reportes.AlertasParams{
		Periodo: periodo, Estado: &estadoFiltro, Limite: 10,
	})
	require.NoError(t, err)
	require.Equal(t, 1, totalEstado)
	require.Equal(t, lote1, *porEstado[0].LoteID)

	// Limite=1: una fila pero total sin límite → truncada en el servicio.
	truncada, totalTruncada, err := repo.Alertas(ctx, reportes.AlertasParams{Periodo: periodo, Limite: 1})
	require.NoError(t, err)
	require.Equal(t, 2, totalTruncada)
	require.Len(t, truncada, 1)
	require.Greater(t, totalTruncada, len(truncada))
}
