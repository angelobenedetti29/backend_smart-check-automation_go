package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reportes"
)

// TestReportesRepo_TrazabilidadIntegracion cubre el snapshot completo de un
// lote cerrado: cabecera, KPIs/serie, confianza por clase, decisiones,
// consignas y alertas.
func TestReportesRepo_TrazabilidadIntegracion(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)

	art := time.FixedZone("ART", -3*60*60)
	_, err := pool.Exec(ctx, `
		INSERT INTO productos (
			id, nombre, activo, peso_referencia_kg, tolerancia_peso_pct,
			dimension_base_cm, tolerancia_dimension_cm,
			temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max
		) VALUES ($1, 'Tostada Trazabilidad', true, 0.25, 5, 12, 0.5, 180, 220, 0.1, 0.3)`,
		testProductoID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sectores (id, nombre) VALUES ($1, 'Sector Trazabilidad')`, testSectorID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO dispositivos (id, nombre, tipo, sector_id) VALUES ($1, 'pi-entrada-traz', 'ENTRADA_HORNO', $2)`,
		testEntradaDevID, testSectorID)
	require.NoError(t, err)

	inicio := time.Date(2026, 10, 1, 10, 0, 0, 0, art)
	fin := inicio.Add(2 * time.Hour)

	var loteID string
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
		testProductoID, testSectorID, inicio, fin).Scan(&loteID)
	require.NoError(t, err)

	// 3 métricas: dos en el mismo bucket de 60s y una en otro.
	metricas := []struct {
		recibida time.Time
		temp     float64
		objTemp  *float64
		vel      float64
	}{
		{inicio.Add(10 * time.Second), 180, nil, 0.20},
		{inicio.Add(40 * time.Second), 182, floatPtr(180), 0.22},
		{inicio.Add(2*time.Minute + 5*time.Second), 184, floatPtr(180), 0.24},
	}
	for _, m := range metricas {
		_, err = pool.Exec(ctx, `
			INSERT INTO metricas_horno (
				lote_id, dispositivo_id, sector_id, temp_horno_1, velocidad_cinta,
				temp_objetivo, vel_objetivo, received_at
			) VALUES ($1,$2,$3,$4,$5,$6,0.200,$7)`,
			loteID, testEntradaDevID, testSectorID, m.temp, m.vel, m.objTemp, m.recibida)
		require.NoError(t, err)
	}

	// Eventos: confianza por clase (ok 0.9/0.8/0.7, crudo 0.6) y una fila sin
	// estado que no debe aparecer.
	eventos := []struct {
		estado    *string
		confianza *float64
	}{
		{strPtr("ok"), floatPtr(0.9)},
		{strPtr("ok"), floatPtr(0.8)},
		{strPtr("ok"), floatPtr(0.7)},
		{strPtr("crudo"), floatPtr(0.6)},
		{nil, nil},
	}
	for i, e := range eventos {
		var eventoID string
		eventoID = "cccccccc-0000-0000-0000-00000000000" + string(rune('0'+i))
		_, err = pool.Exec(ctx, `
			INSERT INTO eventos_lote (lote_id, evento_id, producto_id, estado, confianza, created_at)
			VALUES ($1,$2,$3,$4,$5,$6)`,
			loteID, eventoID, testProductoID, e.estado, e.confianza, inicio.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
	}

	// 2 decisiones: la segunda es más reciente y debe salir primero.
	for i := 0; i < 2; i++ {
		_, err = pool.Exec(ctx, `
			INSERT INTO decisiones_control (
				lote_id, ventana_inicio, ventana_fin, muestras,
				temp_actual, vel_actual,
				pct_crudos_obs, pct_ok_obs, pct_quemados_obs, conf_promedio,
				temp_propuesto, vel_propuesto, temp_aplicado, vel_aplicado,
				z_observado, z_predicho, aplicado_por, created_at
			) VALUES ($1,$2,$3,30,180,0.2,2,95,3,0.9,181,0.21,$4,$5,0.2,0.3,'MPC',$6)`,
			loteID, inicio.Add(time.Duration(i)*time.Minute), inicio.Add(time.Duration(i+1)*time.Minute),
			181.0+float64(i), 0.21+float64(i)*0.01,
			inicio.Add(time.Duration(i)*time.Minute))
		require.NoError(t, err)
	}

	// 2 consignas, 2 alertas: orden ascendente por fecha.
	for i := 0; i < 2; i++ {
		_, err = pool.Exec(ctx, `
			INSERT INTO historial_consignas (
				horno_id, lote_id, producto_id, temperatura_objetivo,
				velocidad_cinta_objetivo, origen, usuario, exitosa,
				decision_id, estado,
				temperatura_previa, velocidad_cinta_previa, creada_en
			) VALUES ('horno-traz',$1,$2,$3,$4,'MANUAL','operario',true,
				gen_random_uuid(),'APLICADA',179,0.19,$5)`,
			loteID, testProductoID, 180.0+float64(i), 0.2+float64(i)*0.01,
			inicio.Add(time.Duration(i)*time.Minute))
		require.NoError(t, err)

		_, err = pool.Exec(ctx, `
			INSERT INTO alertas (sector_id, lote_id, tipo, severidad, estado, mensaje, created_at, acked_at)
			VALUES ($1,$2,'TEMPERATURA_HORNO','CRITICAL','ACK','alta temp',$3,$4)`,
			testSectorID, loteID, inicio.Add(time.Duration(i)*time.Minute), inicio.Add(time.Duration(i+1)*time.Minute))
		require.NoError(t, err)
	}

	repo := NewReportesPostgresRepository(pool)
	traz, err := repo.Trazabilidad(ctx, loteID)
	require.NoError(t, err)
	require.NotNil(t, traz)

	// Cabecera.
	require.Equal(t, loteID, traz.Lote.ID)
	require.Equal(t, "CERRADO", traz.Lote.Estado)
	require.Equal(t, "AUTOMATICO", traz.Lote.Modo)
	require.Equal(t, "Sector Trazabilidad", traz.Lote.SectorNombre)
	require.Equal(t, "Tostada Trazabilidad", traz.Lote.ProductoNombre)
	require.Equal(t, 100, traz.Lote.Conteos.Total)
	require.Equal(t, 90, *traz.Lote.Conteos.OK)
	require.Equal(t, 6, *traz.Lote.Conteos.Crudo)
	require.Equal(t, 4, *traz.Lote.Conteos.Quemado)
	require.InDelta(t, 25.0, traz.Lote.Kg.Total, 0.0001)
	require.InDelta(t, 2.5, traz.Lote.Kg.Merma, 0.0001)
	require.InDelta(t, 90.0, *traz.Lote.FPYPct, 0.0001)
	require.InDelta(t, 6.0, *traz.Lote.CrudoPct, 0.0001)
	require.InDelta(t, 4.0, *traz.Lote.QuemadoPct, 0.0001)
	require.InDelta(t, 120.0, *traz.Lote.DuracionMin, 0.0001)
	require.NotNil(t, traz.Lote.Temp1Objetivo)
	require.InDelta(t, 180.0, *traz.Lote.Temp1Objetivo, 0.0001)
	require.Equal(t, 2.0, traz.Lote.ToleranciaTempC)

	// Proceso.
	require.NotNil(t, traz.Proceso.TempMAEC)
	require.InDelta(t, 2.0, *traz.Proceso.TempMAEC, 0.0001)
	require.NotNil(t, traz.Proceso.TempEnToleranciaPct)
	require.InDelta(t, 66.6667, *traz.Proceso.TempEnToleranciaPct, 0.01)
	require.NotNil(t, traz.Proceso.VelMAE)
	require.InDelta(t, 0.02, *traz.Proceso.VelMAE, 0.0001)
	require.NotNil(t, traz.Proceso.VelEnToleranciaPct)
	require.InDelta(t, 100.0, *traz.Proceso.VelEnToleranciaPct, 0.01)
	require.Len(t, traz.Proceso.Serie, 2)
	require.True(t, traz.Proceso.Serie[0].TS.Before(traz.Proceso.Serie[1].TS))
	require.Equal(t, 2, traz.Proceso.Serie[0].Muestras)
	require.Equal(t, 1, traz.Proceso.Serie[1].Muestras)
	require.InDelta(t, 181.0, *traz.Proceso.Serie[0].TempReal, 0.0001)

	// Confianza por clase.
	require.Len(t, traz.ConfianzaClases, 2)
	require.Equal(t, "crudo", traz.ConfianzaClases[0].Clase)
	require.Equal(t, 1, traz.ConfianzaClases[0].Muestras)
	require.Nil(t, traz.ConfianzaClases[0].ConfianzaStddev)
	require.Equal(t, "ok", traz.ConfianzaClases[1].Clase)
	require.Equal(t, 3, traz.ConfianzaClases[1].Muestras)
	require.InDelta(t, 0.8, traz.ConfianzaClases[1].ConfianzaMedia, 0.0001)
	require.NotNil(t, traz.ConfianzaClases[1].ConfianzaStddev)
	require.InDelta(t, 0.1, *traz.ConfianzaClases[1].ConfianzaStddev, 0.0001)

	// Decisiones, consignas y alertas.
	require.Len(t, traz.Decisiones, 2)
	require.Equal(t, "MPC", *traz.Decisiones[0].AplicadoPor)
	require.True(t, traz.Decisiones[0].CreadaEn.After(traz.Decisiones[1].CreadaEn))
	require.NotNil(t, traz.Decisiones[0].TempAplicado)
	require.InDelta(t, 182.0, *traz.Decisiones[0].TempAplicado, 0.0001)

	require.Len(t, traz.Consignas, 2)
	require.Equal(t, "MANUAL", traz.Consignas[0].Origen)
	require.NotNil(t, traz.Consignas[0].DecisionID)
	require.Equal(t, "APLICADA", traz.Consignas[0].Estado)
	require.True(t, traz.Consignas[0].CreadaEn.Before(traz.Consignas[1].CreadaEn))

	require.Len(t, traz.Alertas, 2)
	require.Equal(t, "TEMPERATURA_HORNO", traz.Alertas[0].Tipo)
	require.True(t, traz.Alertas[0].CreadaEn.Before(traz.Alertas[1].CreadaEn))
	require.NotNil(t, traz.Alertas[0].AckedAt)

	// Lote inexistente.
	_, err = repo.Trazabilidad(ctx, "a1b2c3d4-5678-90ab-cdef-1234567890ab")
	require.ErrorIs(t, err, reportes.ErrLoteNoEncontrado)
}

func strPtr(v string) *string { return &v }
