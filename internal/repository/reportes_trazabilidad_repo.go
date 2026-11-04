package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reportes"
)

// zonaARTRepo replica el fallback de la zona de negocio para serializar la
// serie con el mismo offset que el resto de la API.
var zonaARTRepo = func() *time.Location {
	if loc, err := time.LoadLocation(reportes.Timezone); err == nil {
		return loc
	}
	return time.FixedZone("ART", -3*60*60)
}()

// Trazabilidad arma el snapshot completo del lote en una única transacción
// read-only REPEATABLE READ: cabecera, proceso, confianza, decisiones,
// consignas y alertas.
func (r *ReportesPostgresRepository) Trazabilidad(ctx context.Context, loteID string) (*reportes.Trazabilidad, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin tx trazabilidad: %w", err)
	}
	defer tx.Rollback(ctx)

	lote, err := loteTrazabilidad(ctx, tx, loteID)
	if err != nil {
		return nil, err
	}

	out := &reportes.Trazabilidad{
		Lote:            lote,
		Proceso:         reportes.ProcesoTrazabilidad{Serie: []reportes.PuntoProceso{}},
		ConfianzaClases: []reportes.ConfianzaClaseTrazabilidad{},
		Decisiones:      []reportes.DecisionTrazabilidad{},
		Consignas:       []reportes.ConsignaTrazabilidad{},
		Alertas:         []reportes.AlertaTrazabilidad{},
	}
	if err := trazabilidadProceso(ctx, tx, loteID, &out.Proceso); err != nil {
		return nil, err
	}
	if err := trazabilidadConfianza(ctx, tx, loteID, &out.ConfianzaClases); err != nil {
		return nil, err
	}
	if err := trazabilidadDecisiones(ctx, tx, loteID, &out.Decisiones); err != nil {
		return nil, err
	}
	if err := trazabilidadConsignas(ctx, tx, loteID, &out.Consignas); err != nil {
		return nil, err
	}
	if err := trazabilidadAlertas(ctx, tx, loteID, &out.Alertas); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx trazabilidad: %w", err)
	}
	return out, nil
}

// loteTrazabilidad trae la cabecera con LoteReporte más los parámetros de
// control del lote. Un id inexistente devuelve ErrLoteNoEncontrado.
func loteTrazabilidad(ctx context.Context, tx pgx.Tx, loteID string) (reportes.LoteTrazabilidad, error) {
	var l reportes.LoteTrazabilidad
	query := fmt.Sprintf(`
		SELECT %s,
		       lp.temp1_objetivo, lp.vel_objetivo, lp.tolerancia_temp_c,
		       lp.umbral_aceptacion_crudo, lp.umbral_aceptacion_quemado,
		       lp.delta_temp1_max_c, lp.delta_vel_max
		FROM lotes_productivos lp
		LEFT JOIN sectores s ON s.id = lp.sector_id
		JOIN productos pr ON pr.id = lp.producto_id
		WHERE lp.id = $1`, fmt.Sprintf(loteReporteSelect, sqlUnidades))

	err := tx.QueryRow(ctx, query, loteID).Scan(
		&l.ID, &l.SectorID, &l.SectorNombre,
		&l.ProductoID, &l.ProductoNombre, &l.Turno, &l.Estado,
		&l.AbiertoEn, &l.CerradoEn, &l.MotivoCierre, &l.Modo,
		&l.Conteos.OK, &l.Conteos.Crudo, &l.Conteos.Quemado, &l.Conteos.Total,
		&l.Kg.OK, &l.Kg.Crudo, &l.Kg.Quemado,
		&l.ConfianzaPromedio,
		&l.Temp1Objetivo, &l.VelObjetivo, &l.ToleranciaTempC,
		&l.UmbralAceptacionCrudo, &l.UmbralAceptacionQuemado,
		&l.DeltaTemp1MaxC, &l.DeltaVelMax,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, reportes.ErrLoteNoEncontrado
	}
	if err != nil {
		return l, fmt.Errorf("query trazabilidad lote: %w", err)
	}
	derivarLoteReporte(&l.LoteReporte)
	return l, nil
}

// trazabilidadProceso calcula los KPIs de seguimiento y la serie temporal
// agregada en buckets de ~120 puntos sobre metricas_horno del lote.
func trazabilidadProceso(ctx context.Context, tx pgx.Tx, loteID string, out *reportes.ProcesoTrazabilidad) error {
	objTemp := "COALESCE(m.temp_objetivo, lp.temp1_objetivo)"
	diffTemp := fmt.Sprintf("(m.temp_horno_1 - %s)", objTemp)
	objVel := "COALESCE(m.vel_objetivo, lp.vel_objetivo)"
	diffVel := fmt.Sprintf("(m.velocidad_cinta - %s)", objVel)
	paresTemp := fmt.Sprintf("m.temp_horno_1 IS NOT NULL AND %s IS NOT NULL", objTemp)
	paresVel := fmt.Sprintf("m.velocidad_cinta IS NOT NULL AND %s IS NOT NULL", objVel)

	query := fmt.Sprintf(`
		SELECT (AVG(ABS(%s)) FILTER (WHERE %s))::float8,
		       (100.0 * count(*) FILTER (WHERE %s AND ABS(%s) <= lp.tolerancia_temp_c)
		            / NULLIF(count(*) FILTER (WHERE %s),0))::float8,
		       (AVG(ABS(%s)) FILTER (WHERE %s))::float8,
		       (100.0 * count(*) FILTER (WHERE %s AND ABS(%s) <= COALESCE(lp.delta_vel_max, pr.delta_vel_max))
		            / NULLIF(count(*) FILTER (WHERE %s),0))::float8
		FROM metricas_horno m
		JOIN lotes_productivos lp ON lp.id = m.lote_id
		JOIN productos pr ON pr.id = lp.producto_id
		WHERE m.lote_id = $1`,
		diffTemp, paresTemp, paresTemp, diffTemp, paresTemp,
		diffVel, paresVel, paresVel, diffVel, paresVel)

	if err := tx.QueryRow(ctx, query, loteID).Scan(
		&out.TempMAEC, &out.TempEnToleranciaPct, &out.VelMAE, &out.VelEnToleranciaPct,
	); err != nil {
		return fmt.Errorf("query trazabilidad proceso: %w", err)
	}

	// bucket = max(60s, ceil(duración/120)); en lotes abiertos now() cierra el
	// rango para no dejar la serie sin eje.
	serieQuery := fmt.Sprintf(`
		WITH params AS (
			SELECT GREATEST(60, CEIL(EXTRACT(EPOCH FROM (COALESCE(fin_at, now()) - inicio_at))/120.0))::bigint AS bucket
			FROM lotes_productivos WHERE id = $1
		)
		SELECT to_timestamp(floor(extract(epoch from m.received_at)/p.bucket)*p.bucket),
		       AVG(m.temp_horno_1)::float8,
		       AVG(%s)::float8,
		       AVG(m.velocidad_cinta)::float8,
		       AVG(%s)::float8,
		       count(*)::int
		FROM metricas_horno m
		JOIN lotes_productivos lp ON lp.id = m.lote_id
		CROSS JOIN params p
		WHERE m.lote_id = $1
		GROUP BY 1 ORDER BY 1`, objTemp, objVel)

	rows, err := tx.Query(ctx, serieQuery, loteID)
	if err != nil {
		return fmt.Errorf("query trazabilidad serie: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p reportes.PuntoProceso
		if err := rows.Scan(&p.TS, &p.TempReal, &p.TempObjetivo, &p.VelReal, &p.VelObjetivo, &p.Muestras); err != nil {
			return fmt.Errorf("scan trazabilidad serie: %w", err)
		}
		p.TS = p.TS.In(zonaARTRepo)
		out.Serie = append(out.Serie, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows trazabilidad serie: %w", err)
	}
	return nil
}

func trazabilidadConfianza(ctx context.Context, tx pgx.Tx, loteID string, out *[]reportes.ConfianzaClaseTrazabilidad) error {
	rows, err := tx.Query(ctx, `
		SELECT estado, AVG(confianza)::float8, STDDEV_SAMP(confianza)::float8, count(*)::int
		FROM eventos_lote
		WHERE lote_id = $1 AND estado IS NOT NULL AND confianza IS NOT NULL
		GROUP BY estado ORDER BY estado`, loteID)
	if err != nil {
		return fmt.Errorf("query trazabilidad confianza: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var c reportes.ConfianzaClaseTrazabilidad
		if err := rows.Scan(&c.Clase, &c.ConfianzaMedia, &c.ConfianzaStddev, &c.Muestras); err != nil {
			return fmt.Errorf("scan trazabilidad confianza: %w", err)
		}
		*out = append(*out, c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows trazabilidad confianza: %w", err)
	}
	return nil
}

func trazabilidadDecisiones(ctx context.Context, tx pgx.Tx, loteID string, out *[]reportes.DecisionTrazabilidad) error {
	rows, err := tx.Query(ctx, `
		SELECT id::text, created_at, ventana_inicio, ventana_fin, muestras,
		       temp_actual, vel_actual,
		       pct_ok_obs, pct_crudos_obs, pct_quemados_obs, conf_promedio,
		       temp_propuesto, vel_propuesto, temp_aplicado, vel_aplicado,
		       z_observado, z_predicho, razon_skipped, aplicado_por,
		       aprobada_por, aprobada_at
		FROM decisiones_control
		WHERE lote_id = $1
		ORDER BY created_at DESC
		LIMIT 100`, loteID)
	if err != nil {
		return fmt.Errorf("query trazabilidad decisiones: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d reportes.DecisionTrazabilidad
		if err := rows.Scan(
			&d.ID, &d.CreadaEn, &d.VentanaInicio, &d.VentanaFin, &d.Muestras,
			&d.TempActual, &d.VelActual,
			&d.PctOkObs, &d.PctCrudosObs, &d.PctQuemadosObs, &d.ConfPromedio,
			&d.TempPropuesto, &d.VelPropuesto, &d.TempAplicado, &d.VelAplicado,
			&d.ZObservado, &d.ZPredicho, &d.RazonSkipped, &d.AplicadoPor,
			&d.AprobadaPor, &d.AprobadaAt,
		); err != nil {
			return fmt.Errorf("scan trazabilidad decisiones: %w", err)
		}
		*out = append(*out, d)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows trazabilidad decisiones: %w", err)
	}
	return nil
}

func trazabilidadConsignas(ctx context.Context, tx pgx.Tx, loteID string, out *[]reportes.ConsignaTrazabilidad) error {
	rows, err := tx.Query(ctx, `
		SELECT id::text, creada_en, temperatura_objetivo, velocidad_cinta_objetivo,
		       origen, usuario, exitosa, motivo_error,
		       temperatura_previa, velocidad_cinta_previa
		FROM historial_consignas
		WHERE lote_id = $1
		ORDER BY creada_en ASC`, loteID)
	if err != nil {
		return fmt.Errorf("query trazabilidad consignas: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var c reportes.ConsignaTrazabilidad
		if err := rows.Scan(
			&c.ID, &c.CreadaEn, &c.TemperaturaObjetivo, &c.VelocidadCintaObjetivo,
			&c.Origen, &c.Usuario, &c.Exitosa, &c.MotivoError,
			&c.TemperaturaPrevia, &c.VelocidadCintaPrevia,
		); err != nil {
			return fmt.Errorf("scan trazabilidad consignas: %w", err)
		}
		*out = append(*out, c)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows trazabilidad consignas: %w", err)
	}
	return nil
}

func trazabilidadAlertas(ctx context.Context, tx pgx.Tx, loteID string, out *[]reportes.AlertaTrazabilidad) error {
	rows, err := tx.Query(ctx, `
		SELECT id::text, created_at, tipo, severidad, estado, mensaje, acked_at
		FROM alertas
		WHERE lote_id = $1
		ORDER BY created_at ASC`, loteID)
	if err != nil {
		return fmt.Errorf("query trazabilidad alertas: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var a reportes.AlertaTrazabilidad
		if err := rows.Scan(&a.ID, &a.CreadaEn, &a.Tipo, &a.Severidad, &a.Estado, &a.Mensaje, &a.AckedAt); err != nil {
			return fmt.Errorf("scan trazabilidad alertas: %w", err)
		}
		*out = append(*out, a)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows trazabilidad alertas: %w", err)
	}
	return nil
}
