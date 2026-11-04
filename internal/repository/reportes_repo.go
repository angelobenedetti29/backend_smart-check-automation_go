package repository

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reportes"
)

// ReportesPostgresRepository implementa reportes.Repository sobre PostgreSQL.
type ReportesPostgresRepository struct {
	db *pgxpool.Pool
}

// NewReportesPostgresRepository crea el repositorio de reportes.
func NewReportesPostgresRepository(db *pgxpool.Pool) *ReportesPostgresRepository {
	return &ReportesPostgresRepository{db: db}
}

const (
	sqlUnidades = "COALESCE(lp.total_unidades, COALESCE(lp.correctos,0)+COALESCE(lp.crudas,0)+COALESCE(lp.quemados,0))"
	sqlKgTotal  = "COALESCE(lp.correctos_kg,0)+COALESCE(lp.crudos_kg,0)+COALESCE(lp.quemados_kg,0)"
)

// argsBuilder acumula argumentos y devuelve el placeholder correspondiente.
type argsBuilder struct {
	args []any
}

func (b *argsBuilder) add(v any) string {
	b.args = append(b.args, v)
	return fmt.Sprintf("$%d", len(b.args))
}

// loteFiltros arma el WHERE de lotes_productivos con el período ya traducido a
// [desde, hasta) y los filtros opcionales. El alias cambia por query (lp, l, a,
// d, m), por eso se recibe como parámetro.
type loteFiltros struct {
	desde    time.Time
	hasta    time.Time
	sector   *string
	producto *string
	turno    *string
}

func (f loteFiltros) where(b *argsBuilder, alias string) string {
	return f.whereConPeriodo(b, alias, alias+".inicio_at")
}

// whereConPeriodo arma el filtro usando una columna temporal explícita (los
// bloques de proceso/alertas/automatización filtran por su propio timestamp).
func (f loteFiltros) whereConPeriodo(b *argsBuilder, alias, columnaTiempo string) string {
	conds := []string{
		fmt.Sprintf("%s >= %s", columnaTiempo, b.add(f.desde)),
		fmt.Sprintf("%s < %s", columnaTiempo, b.add(f.hasta)),
	}
	if f.sector != nil {
		conds = append(conds, fmt.Sprintf("%s.sector_id = %s", alias, b.add(*f.sector)))
	}
	if f.producto != nil {
		conds = append(conds, fmt.Sprintf("%s.producto_id = %s", alias, b.add(*f.producto)))
	}
	if f.turno != nil {
		conds = append(conds, fmt.Sprintf("%s.turno = %s", alias, b.add(*f.turno)))
	}
	return strings.Join(conds, " AND ")
}

// whereProceso filtra metricas_horno por received_at y los lotes por sector,
// producto y turno.
func (f loteFiltros) whereProceso() (*argsBuilder, string) {
	b := &argsBuilder{}
	return b, f.whereConPeriodo(b, "lp", "m.received_at")
}

// whereAlerta filtra alertas por created_at; el filtro de sector usa
// a.sector_id y producto/turno exigen lote (LEFT JOIN + igualdad).
func (f loteFiltros) whereAlerta() (*argsBuilder, string) {
	b := &argsBuilder{}
	conds := []string{
		fmt.Sprintf("a.created_at >= %s", b.add(f.desde)),
		fmt.Sprintf("a.created_at < %s", b.add(f.hasta)),
	}
	if f.sector != nil {
		conds = append(conds, fmt.Sprintf("a.sector_id = %s", b.add(*f.sector)))
	}
	if f.producto != nil {
		conds = append(conds, fmt.Sprintf("lp.producto_id = %s", b.add(*f.producto)))
	}
	if f.turno != nil {
		conds = append(conds, fmt.Sprintf("lp.turno = %s", b.add(*f.turno)))
	}
	return b, strings.Join(conds, " AND ")
}

// whereDecision filtra decisiones_control por created_at y los lotes por
// sector, producto y turno.
func (f loteFiltros) whereDecision() (*argsBuilder, string) {
	b := &argsBuilder{}
	return b, f.whereConPeriodo(b, "lp", "d.created_at")
}

// Resumen ejecuta todos los bloques sobre una única transacción read-only
// REPEATABLE READ para que compartan el mismo snapshot.
func (r *ReportesPostgresRepository) Resumen(ctx context.Context, periodo reportes.Periodo, filtros reportes.Filtros) (*reportes.Resumen, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin tx reportes: %w", err)
	}
	defer tx.Rollback(ctx)

	f := loteFiltros{
		desde:    periodo.Desde,
		hasta:    periodo.Hasta.AddDate(0, 0, 1),
		sector:   filtros.SectorID,
		producto: filtros.ProductoID,
		turno:    filtros.Turno,
	}

	resumen := &reportes.Resumen{
		Produccion: reportes.Produccion{
			PorTurno:    []reportes.GrupoProduccion{},
			PorSector:   []reportes.GrupoProduccion{},
			PorProducto: []reportes.GrupoProduccion{},
		},
		Calidad: reportes.Calidad{SerieDiaria: []reportes.CalidadDia{}},
		Proceso: reportes.Proceso{SerieDiaria: []reportes.ProcesoDia{}},
		Alertas: reportes.Alertas{
			PorSeveridad: map[string]int{"INFO": 0, "WARNING": 0, "CRITICAL": 0},
			PorTipo:      []reportes.Conteo{},
			SerieDiaria:  []reportes.AlertasDia{},
		},
		Automatizacion: reportes.Automatizacion{
			TopSkip: []reportes.Conteo{},
			ModoPct: map[string]*float64{"AUTOMATICO": nil, "SUPERVISION": nil, "MANUAL": nil},
		},
	}

	if err := r.produccion(ctx, tx, f, resumen); err != nil {
		return nil, err
	}
	if err := r.proceso(ctx, tx, f, resumen); err != nil {
		return nil, err
	}
	if err := r.alertas(ctx, tx, f, resumen); err != nil {
		return nil, err
	}
	if err := r.automatizacion(ctx, tx, f, resumen); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx reportes: %w", err)
	}
	return resumen, nil
}

func (r *ReportesPostgresRepository) produccion(ctx context.Context, tx pgx.Tx, f loteFiltros, res *reportes.Resumen) error {
	var b argsBuilder
	where := f.where(&b, "lp")

	var (
		lotes                             int
		unidades, ok, crudas, quemadas    int64
		kgOK, kgCrudo, kgQuemado          float64
		duracionMedia, duracionP95, horas *float64
		confianzaNum, confianzaDen        float64
		lotesConTotal, lotesConformes     int
	)
	query := fmt.Sprintf(`
		SELECT count(*)::int,
		       COALESCE(SUM(%s),0)::bigint,
		       COALESCE(SUM(COALESCE(lp.correctos,0)),0)::bigint,
		       COALESCE(SUM(COALESCE(lp.crudas,0)),0)::bigint,
		       COALESCE(SUM(COALESCE(lp.quemados,0)),0)::bigint,
		       COALESCE(SUM(COALESCE(lp.correctos_kg,0)),0)::float8,
		       COALESCE(SUM(COALESCE(lp.crudos_kg,0)),0)::float8,
		       COALESCE(SUM(COALESCE(lp.quemados_kg,0)),0)::float8,
		       (AVG(EXTRACT(EPOCH FROM (lp.fin_at-lp.inicio_at))/60.0) FILTER (WHERE lp.fin_at IS NOT NULL))::float8,
		       (percentile_cont(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (lp.fin_at-lp.inicio_at))/60.0) FILTER (WHERE lp.fin_at IS NOT NULL))::float8,
		       COALESCE(SUM(EXTRACT(EPOCH FROM (lp.fin_at-lp.inicio_at))/3600.0) FILTER (WHERE lp.fin_at IS NOT NULL),0)::float8,
		       COALESCE(SUM(lp.confianza_promedio*COALESCE(lp.total_unidades,0)) FILTER (WHERE lp.confianza_promedio IS NOT NULL),0)::float8,
		       COALESCE(SUM(COALESCE(lp.total_unidades,0)) FILTER (WHERE lp.confianza_promedio IS NOT NULL),0)::float8,
		       count(*) FILTER (WHERE %s > 0)::int,
		       count(*) FILTER (
		           WHERE %s > 0
		             AND COALESCE(lp.crudas,0)::float8*100/NULLIF(%s,0) <= lp.umbral_aceptacion_crudo
		             AND COALESCE(lp.quemados,0)::float8*100/NULLIF(%s,0) <= lp.umbral_aceptacion_quemado
		       )::int
		FROM lotes_productivos lp
		WHERE lp.estado = 'CERRADO' AND %s`,
		sqlUnidades, sqlUnidades, sqlUnidades, sqlUnidades, sqlUnidades, where)

	if err := tx.QueryRow(ctx, query, b.args...).Scan(
		&lotes, &unidades, &ok, &crudas, &quemadas,
		&kgOK, &kgCrudo, &kgQuemado,
		&duracionMedia, &duracionP95, &horas,
		&confianzaNum, &confianzaDen,
		&lotesConTotal, &lotesConformes,
	); err != nil {
		return fmt.Errorf("query reportes produccion: %w", err)
	}

	p := &res.Produccion
	p.LotesCerrados = lotes
	p.LotesTotal = lotes
	p.Unidades = int(unidades)
	p.UnidadesOK = int(ok)
	p.UnidadesCrudas = int(crudas)
	p.UnidadesQuemadas = int(quemadas)
	p.KgOKEstimados = round3(kgOK)
	p.KgMermaEstimados = round3(kgCrudo + kgQuemado)
	p.KgTotalEstimados = round3(kgOK + kgCrudo + kgQuemado)
	p.DuracionMediaMin = duracionMedia
	p.DuracionP95Min = duracionP95
	if horas != nil && *horas > 0 {
		p.UnidadesPorHora = floatPtr(float64(unidades) / *horas)
		p.KgPorHora = floatPtr(p.KgTotalEstimados / *horas)
	}

	// Lotes abiertos: se cuentan aparte y no suman unidades/kg.
	bAbiertos := argsBuilder{}
	whereAbiertos := f.where(&bAbiertos, "lp")
	if err := tx.QueryRow(ctx, fmt.Sprintf(
		`SELECT count(*)::int FROM lotes_productivos lp WHERE lp.estado = 'ABIERTO' AND %s`,
		whereAbiertos), bAbiertos.args...).Scan(&p.LotesAbiertos); err != nil {
		return fmt.Errorf("query reportes lotes abiertos: %w", err)
	}
	p.LotesTotal = p.LotesCerrados + p.LotesAbiertos

	// FPY/crudo/quemado globales salen de los buckets, no de total_unidades.
	den := float64(ok + crudas + quemadas)
	if den > 0 {
		res.Calidad.FPYPct = floatPtr(float64(ok) / den * 100)
		res.Calidad.CrudoPct = floatPtr(float64(crudas) / den * 100)
		res.Calidad.QuemadoPct = floatPtr(float64(quemadas) / den * 100)
	}
	if confianzaDen > 0 {
		res.Calidad.ConfianzaPromedio = floatPtr(confianzaNum / confianzaDen)
	}
	if lotesConTotal > 0 {
		res.Calidad.LotesConformesPct = floatPtr(float64(lotesConformes) / float64(lotesConTotal) * 100)
	}

	if err := r.grupos(ctx, tx, f, res); err != nil {
		return err
	}
	return r.serieCalidad(ctx, tx, f, res)
}

// grupos carga los cortes por turno, sector y producto con una query cada uno.
func (r *ReportesPostgresRepository) grupos(ctx context.Context, tx pgx.Tx, f loteFiltros, res *reportes.Resumen) error {
	type grupoDef struct {
		clave, nombre, from string
		destino             *[]reportes.GrupoProduccion
	}
	defs := []grupoDef{
		{
			clave:   "COALESCE(lp.turno,'')",
			nombre:  "''",
			from:    "FROM lotes_productivos lp",
			destino: &res.Produccion.PorTurno,
		},
		{
			clave:   "COALESCE(lp.sector_id,'')",
			nombre:  "COALESCE(s.nombre,'')",
			from:    "FROM lotes_productivos lp LEFT JOIN sectores s ON s.id = lp.sector_id",
			destino: &res.Produccion.PorSector,
		},
		{
			clave:   "lp.producto_id::text",
			nombre:  "pr.nombre",
			from:    "FROM lotes_productivos lp JOIN productos pr ON pr.id = lp.producto_id",
			destino: &res.Produccion.PorProducto,
		},
	}

	for _, def := range defs {
		var b argsBuilder
		where := f.where(&b, "lp")
		query := fmt.Sprintf(`
			SELECT %s, %s, count(*)::int,
			       COALESCE(SUM(%s),0)::bigint,
			       COALESCE(SUM(%s),0)::float8,
			       COALESCE(SUM(COALESCE(lp.correctos,0)),0)::bigint,
			       COALESCE(SUM(COALESCE(lp.correctos,0)+COALESCE(lp.crudas,0)+COALESCE(lp.quemados,0)),0)::bigint
			%s
			WHERE lp.estado = 'CERRADO' AND %s
			GROUP BY 1,2
			ORDER BY 4 DESC, 1`,
			def.clave, def.nombre, sqlUnidades, sqlKgTotal, def.from, where)

		rows, err := tx.Query(ctx, query, b.args...)
		if err != nil {
			return fmt.Errorf("query reportes grupos: %w", err)
		}
		grupos, err := scanGrupos(rows)
		if err != nil {
			return err
		}
		*def.destino = grupos
	}
	return nil
}

func scanGrupos(rows pgx.Rows) ([]reportes.GrupoProduccion, error) {
	defer rows.Close()

	out := []reportes.GrupoProduccion{}
	for rows.Next() {
		var (
			g      reportes.GrupoProduccion
			unidad int64
			ok     int64
			den    int64
		)
		if err := rows.Scan(&g.Clave, &g.Nombre, &g.Lotes, &unidad, &g.KgEstimados, &ok, &den); err != nil {
			return nil, fmt.Errorf("scan reportes grupo: %w", err)
		}
		g.Unidades = int(unidad)
		g.KgEstimados = round3(g.KgEstimados)
		if den > 0 {
			g.FPYPct = floatPtr(float64(ok) / float64(den) * 100)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows reportes grupos: %w", err)
	}
	return out, nil
}

func (r *ReportesPostgresRepository) serieCalidad(ctx context.Context, tx pgx.Tx, f loteFiltros, res *reportes.Resumen) error {
	var b argsBuilder
	where := f.where(&b, "lp")
	query := fmt.Sprintf(`
		SELECT %s, count(*)::int,
		       COALESCE(SUM(%s),0)::bigint,
		       COALESCE(SUM(%s),0)::float8,
		       COALESCE(SUM(COALESCE(lp.correctos,0)),0)::bigint,
		       COALESCE(SUM(COALESCE(lp.crudas,0)),0)::bigint,
		       COALESCE(SUM(COALESCE(lp.quemados,0)),0)::bigint
		FROM lotes_productivos lp
		WHERE lp.estado = 'CERRADO' AND %s
		GROUP BY 1 ORDER BY 1`,
		fechaART("lp", "inicio_at"), sqlUnidades, sqlKgTotal, where)

	rows, err := tx.Query(ctx, query, b.args...)
	if err != nil {
		return fmt.Errorf("query reportes serie calidad: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			d                    reportes.CalidadDia
			unidades, ok, cr, qm int64
		)
		if err := rows.Scan(&d.Fecha, &d.Lotes, &unidades, &d.KgEstimados, &ok, &cr, &qm); err != nil {
			return fmt.Errorf("scan reportes serie calidad: %w", err)
		}
		d.Unidades = int(unidades)
		d.KgEstimados = round3(d.KgEstimados)
		den := float64(ok + cr + qm)
		if den > 0 {
			d.FPYPct = floatPtr(float64(ok) / den * 100)
			d.CrudoPct = floatPtr(float64(cr) / den * 100)
			d.QuemadoPct = floatPtr(float64(qm) / den * 100)
		}
		res.Calidad.SerieDiaria = append(res.Calidad.SerieDiaria, d)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows reportes serie calidad: %w", err)
	}
	return nil
}

func (r *ReportesPostgresRepository) proceso(ctx context.Context, tx pgx.Tx, f loteFiltros, res *reportes.Resumen) error {
	b, where := f.whereProceso()

	objTemp := "COALESCE(m.temp_objetivo, lp.temp1_objetivo)"
	diffTemp := fmt.Sprintf("(m.temp_horno_1 - %s)", objTemp)
	objVel := "COALESCE(m.vel_objetivo, lp.vel_objetivo)"
	diffVel := fmt.Sprintf("(m.velocidad_cinta - %s)", objVel)
	tolVel := "COALESCE(lp.delta_vel_max, pr.delta_vel_max)"
	paresTemp := fmt.Sprintf("m.temp_horno_1 IS NOT NULL AND %s IS NOT NULL", objTemp)
	paresVel := fmt.Sprintf("m.velocidad_cinta IS NOT NULL AND %s IS NOT NULL", objVel)

	query := fmt.Sprintf(`
		SELECT (AVG(ABS(%s)) FILTER (WHERE %s))::float8,
		       (100.0 * count(*) FILTER (WHERE %s AND ABS(%s) <= lp.tolerancia_temp_c)
		            / NULLIF(count(*) FILTER (WHERE %s),0))::float8,
		       (AVG(ABS(%s)) FILTER (WHERE %s))::float8,
		       (100.0 * count(*) FILTER (WHERE %s AND ABS(%s) <= %s)
		            / NULLIF(count(*) FILTER (WHERE %s),0))::float8
		FROM metricas_horno m
		JOIN lotes_productivos lp ON lp.id = m.lote_id
		JOIN productos pr ON pr.id = lp.producto_id
		WHERE %s`,
		diffTemp, paresTemp, paresTemp, diffTemp, paresTemp,
		diffVel, paresVel, paresVel, diffVel, tolVel, paresVel, where)

	if err := tx.QueryRow(ctx, query, b.args...).Scan(
		&res.Proceso.TempMAEC,
		&res.Proceso.TempEnToleranciaPct,
		&res.Proceso.VelMAE,
		&res.Proceso.VelEnToleranciaPct,
	); err != nil {
		return fmt.Errorf("query reportes proceso: %w", err)
	}

	// Serie diaria: sólo días con muestras.
	bSerie, whereSerie := f.whereProceso()
	serieQuery := fmt.Sprintf(`
		SELECT %s,
		       AVG(m.temp_horno_1)::float8,
		       AVG(%s)::float8,
		       (100.0 * count(*) FILTER (WHERE %s AND ABS(%s) <= lp.tolerancia_temp_c)
		            / NULLIF(count(*) FILTER (WHERE %s),0))::float8
		FROM metricas_horno m
		JOIN lotes_productivos lp ON lp.id = m.lote_id
		JOIN productos pr ON pr.id = lp.producto_id
		WHERE %s
		GROUP BY 1 ORDER BY 1`,
		fechaART("m", "received_at"), objTemp, paresTemp, diffTemp, paresTemp, whereSerie)

	rows, err := tx.Query(ctx, serieQuery, bSerie.args...)
	if err != nil {
		return fmt.Errorf("query reportes serie proceso: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var d reportes.ProcesoDia
		if err := rows.Scan(&d.Fecha, &d.TempRealProm, &d.TempObjetivoProm, &d.TempEnToleranciaPct); err != nil {
			return fmt.Errorf("scan reportes serie proceso: %w", err)
		}
		res.Proceso.SerieDiaria = append(res.Proceso.SerieDiaria, d)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows reportes serie proceso: %w", err)
	}
	return nil
}

func (r *ReportesPostgresRepository) alertas(ctx context.Context, tx pgx.Tx, f loteFiltros, res *reportes.Resumen) error {
	b, where := f.whereAlerta()

	query := fmt.Sprintf(`
		SELECT count(*)::int,
		       count(*) FILTER (WHERE a.severidad = 'INFO')::int,
		       count(*) FILTER (WHERE a.severidad = 'WARNING')::int,
		       count(*) FILTER (WHERE a.severidad = 'CRITICAL')::int,
		       count(*) FILTER (WHERE a.acked_at IS NOT NULL)::int,
		       (AVG(EXTRACT(EPOCH FROM (a.acked_at-a.created_at))/60.0) FILTER (WHERE a.acked_at IS NOT NULL))::float8
		FROM alertas a
		LEFT JOIN lotes_productivos lp ON lp.id = a.lote_id
		WHERE %s`, where)

	var total, info, warning, critical, acked int
	if err := tx.QueryRow(ctx, query, b.args...).Scan(
		&total, &info, &warning, &critical, &acked, &res.Alertas.MTTAMin,
	); err != nil {
		return fmt.Errorf("query reportes alertas: %w", err)
	}
	res.Alertas.Total = total
	res.Alertas.PorSeveridad["INFO"] = info
	res.Alertas.PorSeveridad["WARNING"] = warning
	res.Alertas.PorSeveridad["CRITICAL"] = critical
	if total > 0 {
		res.Alertas.AckPct = floatPtr(float64(acked) / float64(total) * 100)
	}

	bTipo, whereTipo := f.whereAlerta()
	tipoQuery := fmt.Sprintf(`
		SELECT a.tipo, count(*)::int
		FROM alertas a
		LEFT JOIN lotes_productivos lp ON lp.id = a.lote_id
		WHERE %s
		GROUP BY a.tipo ORDER BY 2 DESC, 1`, whereTipo)
	rows, err := tx.Query(ctx, tipoQuery, bTipo.args...)
	if err != nil {
		return fmt.Errorf("query reportes alertas por tipo: %w", err)
	}
	for rows.Next() {
		var c reportes.Conteo
		if err := rows.Scan(&c.Clave, &c.Total); err != nil {
			rows.Close()
			return fmt.Errorf("scan reportes alertas por tipo: %w", err)
		}
		res.Alertas.PorTipo = append(res.Alertas.PorTipo, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows reportes alertas por tipo: %w", err)
	}

	bSerie, whereSerie := f.whereAlerta()
	serieQuery := fmt.Sprintf(`
		SELECT %s, count(*)::int,
		       count(*) FILTER (WHERE a.severidad = 'CRITICAL')::int
		FROM alertas a
		LEFT JOIN lotes_productivos lp ON lp.id = a.lote_id
		WHERE %s
		GROUP BY 1 ORDER BY 1`, fechaART("a", "created_at"), whereSerie)
	rowsSerie, err := tx.Query(ctx, serieQuery, bSerie.args...)
	if err != nil {
		return fmt.Errorf("query reportes serie alertas: %w", err)
	}
	defer rowsSerie.Close()
	for rowsSerie.Next() {
		var d reportes.AlertasDia
		if err := rowsSerie.Scan(&d.Fecha, &d.Total, &d.Criticas); err != nil {
			return fmt.Errorf("scan reportes serie alertas: %w", err)
		}
		res.Alertas.SerieDiaria = append(res.Alertas.SerieDiaria, d)
	}
	if err := rowsSerie.Err(); err != nil {
		return fmt.Errorf("rows reportes serie alertas: %w", err)
	}
	return nil
}

func (r *ReportesPostgresRepository) automatizacion(ctx context.Context, tx pgx.Tx, f loteFiltros, res *reportes.Resumen) error {
	b, where := f.whereDecision()

	query := fmt.Sprintf(`
		SELECT count(*)::int,
		       count(*) FILTER (WHERE d.aplicado_por = 'MPC')::int,
		       count(*) FILTER (WHERE d.aplicado_por = 'OPERARIO')::int,
		       count(*) FILTER (WHERE d.aplicado_por IS NULL OR d.aplicado_por = 'NADIE')::int,
		       (AVG(ABS(d.z_observado-d.z_predicho))
		            FILTER (WHERE d.z_observado IS NOT NULL AND d.z_predicho IS NOT NULL))::float8
		FROM decisiones_control d
		JOIN lotes_productivos lp ON lp.id = d.lote_id
		WHERE %s`, where)

	var total, mpc, operario, sinAplicar int
	if err := tx.QueryRow(ctx, query, b.args...).Scan(
		&total, &mpc, &operario, &sinAplicar, &res.Automatizacion.ZMAE,
	); err != nil {
		return fmt.Errorf("query reportes automatizacion: %w", err)
	}
	res.Automatizacion.DecisionesTotal = total
	if total > 0 {
		res.Automatizacion.MPCPct = floatPtr(float64(mpc) / float64(total) * 100)
		res.Automatizacion.OperarioPct = floatPtr(float64(operario) / float64(total) * 100)
		res.Automatizacion.SinAplicarPct = floatPtr(float64(sinAplicar) / float64(total) * 100)
	}

	bSkip, whereSkip := f.whereDecision()
	skipQuery := fmt.Sprintf(`
		SELECT d.razon_skipped, count(*)::int
		FROM decisiones_control d
		JOIN lotes_productivos lp ON lp.id = d.lote_id
		WHERE %s AND d.razon_skipped IS NOT NULL
		GROUP BY d.razon_skipped ORDER BY 2 DESC, 1 LIMIT 5`, whereSkip)
	rows, err := tx.Query(ctx, skipQuery, bSkip.args...)
	if err != nil {
		return fmt.Errorf("query reportes top skip: %w", err)
	}
	for rows.Next() {
		var c reportes.Conteo
		if err := rows.Scan(&c.Clave, &c.Total); err != nil {
			rows.Close()
			return fmt.Errorf("scan reportes top skip: %w", err)
		}
		res.Automatizacion.TopSkip = append(res.Automatizacion.TopSkip, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows reportes top skip: %w", err)
	}

	// modo_pct: distribución de la duración de los lotes cerrados del período.
	bModo := argsBuilder{}
	whereModo := f.where(&bModo, "lp")
	modoQuery := fmt.Sprintf(`
		SELECT COALESCE(lp.modo,'MANUAL'),
		       SUM(EXTRACT(EPOCH FROM (lp.fin_at-lp.inicio_at)))::float8
		FROM lotes_productivos lp
		WHERE lp.estado = 'CERRADO' AND lp.fin_at IS NOT NULL AND %s
		GROUP BY 1`, whereModo)
	rowsModo, err := tx.Query(ctx, modoQuery, bModo.args...)
	if err != nil {
		return fmt.Errorf("query reportes modo: %w", err)
	}
	defer rowsModo.Close()

	var totalSegundos float64
	presentes := map[string]float64{}
	for rowsModo.Next() {
		var modo string
		var segundos float64
		if err := rowsModo.Scan(&modo, &segundos); err != nil {
			return fmt.Errorf("scan reportes modo: %w", err)
		}
		presentes[modo] = segundos
		totalSegundos += segundos
	}
	if err := rowsModo.Err(); err != nil {
		return fmt.Errorf("rows reportes modo: %w", err)
	}
	if totalSegundos > 0 {
		for modo, segundos := range presentes {
			res.Automatizacion.ModoPct[modo] = floatPtr(segundos / totalSegundos * 100)
		}
	}
	return nil
}

// fechaART devuelve la expresión SQL que agrupa por día ART en formato string.
func fechaART(alias, columna string) string {
	return fmt.Sprintf("((%s.%s AT TIME ZONE '%s')::date)::text", alias, columna, reportes.Timezone)
}

// loteReporteSelect es el SELECT compartido de una fila de LoteReporte. El
// marcador %s corresponde a sqlUnidades.
const loteReporteSelect = `lp.id::text, COALESCE(lp.sector_id,''), COALESCE(s.nombre,''),
		       lp.producto_id::text, pr.nombre, lp.turno, lp.estado,
		       lp.inicio_at, lp.fin_at, lp.motivo_cierre, COALESCE(lp.modo,'MANUAL'),
		       lp.correctos, lp.crudas, lp.quemados,
		       (%s)::int,
		       lp.correctos_kg, lp.crudos_kg, lp.quemados_kg,
		       lp.confianza_promedio`

// scanLoteReporte mapea las columnas de loteReporteSelect y deriva las
// métricas calculadas.
func scanLoteReporte(row rowScanner) (reportes.LoteReporte, error) {
	var l reportes.LoteReporte
	if err := row.Scan(
		&l.ID, &l.SectorID, &l.SectorNombre,
		&l.ProductoID, &l.ProductoNombre, &l.Turno, &l.Estado,
		&l.AbiertoEn, &l.CerradoEn, &l.MotivoCierre, &l.Modo,
		&l.Conteos.OK, &l.Conteos.Crudo, &l.Conteos.Quemado, &l.Conteos.Total,
		&l.Kg.OK, &l.Kg.Crudo, &l.Kg.Quemado,
		&l.ConfianzaPromedio,
	); err != nil {
		return l, err
	}
	derivarLoteReporte(&l)
	return l, nil
}

// Lotes devuelve una página del detalle de lotes (CERRADO y ABIERTO) cuyo
// inicio_at cae en el período, más el total que matchea los filtros sin
// cursor. Comparte el WHERE de loteFiltros con el resumen para no divergir.
func (r *ReportesPostgresRepository) Lotes(ctx context.Context, params reportes.LotesParams) ([]reportes.LoteReporte, int, error) {
	f := loteFiltros{
		desde:    params.Periodo.Desde,
		hasta:    params.Periodo.Hasta.AddDate(0, 0, 1),
		sector:   params.Filtros.SectorID,
		producto: params.Filtros.ProductoID,
		turno:    params.Filtros.Turno,
	}

	var b argsBuilder
	where := f.where(&b, "lp")
	if params.AntesDe != nil {
		timeIdx := b.add(*params.AntesDe)
		idIdx := b.add(params.AntesDeID)
		where += fmt.Sprintf(" AND (lp.inicio_at < %s OR (lp.inicio_at = %s AND lp.id < %s))", timeIdx, timeIdx, idIdx)
	}
	limitIdx := b.add(params.Limite)

	query := fmt.Sprintf(`
		SELECT %s
		FROM lotes_productivos lp
		LEFT JOIN sectores s ON s.id = lp.sector_id
		JOIN productos pr ON pr.id = lp.producto_id
		WHERE %s
		ORDER BY lp.inicio_at DESC, lp.id DESC
		LIMIT %s`, fmt.Sprintf(loteReporteSelect, sqlUnidades), where, limitIdx)

	rows, err := r.db.Query(ctx, query, b.args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query reportes lotes: %w", err)
	}
	defer rows.Close()

	out := []reportes.LoteReporte{}
	for rows.Next() {
		l, err := scanLoteReporte(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan reportes lotes: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows reportes lotes: %w", err)
	}

	var cb argsBuilder
	countWhere := f.where(&cb, "lp")
	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT count(*)::int FROM lotes_productivos lp WHERE `+countWhere, cb.args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count reportes lotes: %w", err)
	}
	return out, total, nil
}

// derivarLoteReporte completa duración, kg total/merma y tasas de calidad a
// partir de los buckets persistidos. Los kg nulos de un bucket no aportan a
// las sumas; total y merma quedan en 0 si todos son nulos.
func derivarLoteReporte(l *reportes.LoteReporte) {
	if l.CerradoEn != nil {
		min := l.CerradoEn.Sub(l.AbiertoEn).Minutes()
		l.DuracionMin = &min
	}

	var ok, crudo, quemado int
	if l.Conteos.OK != nil {
		ok = *l.Conteos.OK
	}
	if l.Conteos.Crudo != nil {
		crudo = *l.Conteos.Crudo
	}
	if l.Conteos.Quemado != nil {
		quemado = *l.Conteos.Quemado
	}
	if den := float64(ok + crudo + quemado); den > 0 {
		l.FPYPct = floatPtr(float64(ok) / den * 100)
		l.CrudoPct = floatPtr(float64(crudo) / den * 100)
		l.QuemadoPct = floatPtr(float64(quemado) / den * 100)
	}

	var kgOK, kgCrudo, kgQuemado float64
	if l.Kg.OK != nil {
		kgOK = *l.Kg.OK
	}
	if l.Kg.Crudo != nil {
		kgCrudo = *l.Kg.Crudo
	}
	if l.Kg.Quemado != nil {
		kgQuemado = *l.Kg.Quemado
	}
	l.Kg.Total = round3(kgOK + kgCrudo + kgQuemado)
	l.Kg.Merma = round3(kgCrudo + kgQuemado)
}

// Alertas exporta las alertas del período, sin el tope operativo de 200, más
// el total con los mismos filtros. Lista y count comparten una transacción
// read-only REPEATABLE READ para que ambos vean el mismo snapshot.
func (r *ReportesPostgresRepository) Alertas(ctx context.Context, params reportes.AlertasParams) ([]reportes.AlertaReporte, int, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, fmt.Errorf("begin tx reportes alertas: %w", err)
	}
	defer tx.Rollback(ctx)

	var b argsBuilder
	conds := []string{
		fmt.Sprintf("a.created_at >= %s", b.add(params.Periodo.Desde)),
		fmt.Sprintf("a.created_at < %s", b.add(params.Periodo.Hasta.AddDate(0, 0, 1))),
	}
	if params.Filtros.SectorID != nil {
		conds = append(conds, fmt.Sprintf("a.sector_id = %s", b.add(*params.Filtros.SectorID)))
	}
	// producto/turno exigen lote: el LEFT JOIN deja fuera las alertas sin lote.
	if params.Filtros.ProductoID != nil {
		conds = append(conds, fmt.Sprintf("lp.producto_id = %s", b.add(*params.Filtros.ProductoID)))
	}
	if params.Filtros.Turno != nil {
		conds = append(conds, fmt.Sprintf("lp.turno = %s", b.add(*params.Filtros.Turno)))
	}
	if params.Estado != nil {
		conds = append(conds, fmt.Sprintf("a.estado = %s", b.add(*params.Estado)))
	}
	where := strings.Join(conds, " AND ")

	from := `FROM alertas a
		LEFT JOIN lotes_productivos lp ON lp.id = a.lote_id
		LEFT JOIN sectores s ON s.id = a.sector_id
		LEFT JOIN productos pr ON pr.id = lp.producto_id
		LEFT JOIN usuarios u ON u.id = a.acked_by`

	var total int
	if err := tx.QueryRow(ctx, "SELECT count(*)::int "+from+" WHERE "+where, b.args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count reportes alertas: %w", err)
	}

	limitIdx := b.add(params.Limite)
	query := fmt.Sprintf(`
		SELECT a.id::text, a.created_at,
		       a.sector_id, COALESCE(s.nombre,''),
		       a.lote_id::text, lp.producto_id::text, COALESCE(pr.nombre,''), lp.turno,
		       a.tipo, a.severidad, a.estado, a.mensaje,
		       a.acked_at, u.email,
		       (EXTRACT(EPOCH FROM (a.acked_at-a.created_at))/60.0)::float8
		%s
		WHERE %s
		ORDER BY a.created_at DESC
		LIMIT %s`, from, where, limitIdx)

	rows, err := tx.Query(ctx, query, b.args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query reportes alertas: %w", err)
	}
	defer rows.Close()

	out := []reportes.AlertaReporte{}
	for rows.Next() {
		var a reportes.AlertaReporte
		if err := rows.Scan(
			&a.ID, &a.CreadaEn,
			&a.SectorID, &a.SectorNombre,
			&a.LoteID, &a.ProductoID, &a.ProductoNombre, &a.Turno,
			&a.Tipo, &a.Severidad, &a.Estado, &a.Mensaje,
			&a.AckedAt, &a.AckedPor, &a.MTTAMin,
		); err != nil {
			return nil, 0, fmt.Errorf("scan reportes alertas: %w", err)
		}
		a.CreadaEn = a.CreadaEn.In(zonaARTRepo)
		if a.AckedAt != nil {
			acked := a.AckedAt.In(zonaARTRepo)
			a.AckedAt = &acked
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows reportes alertas: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("commit tx reportes alertas: %w", err)
	}
	return out, total, nil
}

func floatPtr(v float64) *float64 { return &v }

// round3 evita el ruido de punto flotante al exponer kg en JSON/CSV.
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
