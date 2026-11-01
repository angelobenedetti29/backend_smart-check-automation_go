package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
)

// LoteSectorPostgresRepository implementa lote_sector.Repository sobre la tabla
// lotes_productivos (ciclo ABIERTO/CERRADO) y eventos_lote.
type LoteSectorPostgresRepository struct {
	db *pgxpool.Pool
}

// NewLoteSectorPostgresRepository crea un repositorio real del ciclo de lotes
// por sector.
func NewLoteSectorPostgresRepository(db *pgxpool.Pool) *LoteSectorPostgresRepository {
	return &LoteSectorPostgresRepository{db: db}
}

// isInvalidTextRepresentation indica si el error de PostgreSQL es 22P02 (input
// de texto inválido para el tipo destino, p.ej. un id no-UUID contra una
// columna uuid). Permite traducirlo a un error de dominio en vez de un 500.
func isInvalidTextRepresentation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgErrCodeInvalidTextRepresentation
}

// loteSectorSelect es el SELECT compartido del read mapping de un lote: trae el
// nombre del producto vía JOIN y el tipo del dispositivo que lo abrió vía LEFT
// JOIN. Las columnas nullable se escanean a punteros para preservar los null.
//
// Desde SCA-400 también trae modo, ventanas, tolerancias, umbrales, pesos
// (JSONB), flag de inicio manual y últimos setpoints aplicados al horno.
const loteSectorSelect = `
	SELECT
		lp.id, lp.sector_id, lp.estado, lp.turno, lp.producto_id, pr.nombre,
		lp.inicio_at, lp.abierto_por, d.tipo,
		lp.correctos, lp.crudas, lp.quemados,
		lp.ultimo_evento_en, lp.fin_at, lp.motivo_cierre,
		lp.modo, lp.iniciado_manualmente,
		lp.ventana_evaluacion_segundos, lp.ventana_actualizacion_segundos,
		lp.tolerancia_temp_c,
		lp.umbral_aceptacion_crudo, lp.umbral_aceptacion_quemado,
		lp.pesos_objetivo,
		lp.temp1_objetivo, lp.vel_objetivo,
		lp.delta_temp1_max_c, lp.delta_vel_max,
		lp.confianza_promedio
	FROM lotes_productivos lp
	JOIN productos pr ON pr.id = lp.producto_id
	LEFT JOIN dispositivos d ON d.id = lp.abierto_por
`

// scanLoteSector mapea una fila del read mapping compartido a lotesector.Lote.
// InactividadSegundos queda en 0: lo calcula el service con el reloj del server.
func scanLoteSector(row pgx.Row) (*lotesector.Lote, error) {
	var (
		l          lotesector.Lote
		sectorID   *string
		abiertoPor *string
		tipo       *string
		ok         *int
		crudo      *int
		quemado    *int
		pesosJSON  []byte
	)
	if err := row.Scan(
		&l.ID, &sectorID, &l.Estado, &l.Turno, &l.ProductoID, &l.ProductoNombre,
		&l.AbiertoEn, &abiertoPor, &tipo,
		&ok, &crudo, &quemado,
		&l.UltimoEventoEn, &l.CerradoEn, &l.MotivoCierre,
		&l.Modo, &l.IniciadoManualmente,
		&l.VentanaEvaluacionSegundos, &l.VentanaActualizacionSegundos,
		&l.ToleranciaTempC,
		&l.UmbralAceptacionCrudo, &l.UmbralAceptacionQuemado,
		&pesosJSON,
		&l.Temp1Objetivo, &l.VelObjetivo,
		&l.DeltaTemp1MaxC, &l.DeltaVelMax,
		&l.ConfianzaPromedio,
	); err != nil {
		return nil, err
	}
	if sectorID != nil {
		l.SectorID = *sectorID
	}
	if abiertoPor != nil {
		ap := lotesector.AbiertoPor{DeviceID: *abiertoPor}
		if tipo != nil {
			ap.Tipo = *tipo
		}
		l.AbiertoPor = &ap
	}
	l.Conteos = buildConteosLote(ok, crudo, quemado)
	if len(pesosJSON) > 0 {
		var p lotesector.PesosObjetivo
		if err := json.Unmarshal(pesosJSON, &p); err == nil {
			l.PesosObjetivo = &p
		}
	}
	return &l, nil
}

// buildConteosLote construye Conteos preservando los buckets nulos (un estado
// que el modelo no produce viaja null, nunca 0) y sumando solo los no nulos.
func buildConteosLote(ok, crudo, quemado *int) lotesector.Conteos {
	total := 0
	for _, n := range []*int{ok, crudo, quemado} {
		if n != nil {
			total += *n
		}
	}
	return lotesector.Conteos{OK: ok, Crudo: crudo, Quemado: quemado, Total: total}
}

// GetAbiertoBySector devuelve el lote ABIERTO del sector, o nil si no hay.
func (r *LoteSectorPostgresRepository) GetAbiertoBySector(ctx context.Context, sectorID string) (*lotesector.Lote, error) {
	l, err := scanLoteSector(r.db.QueryRow(ctx, loteSectorSelect+`
		WHERE lp.estado='ABIERTO' AND lp.sector_id=$1`, sectorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get lote abierto by sector: %w", err)
	}
	return l, nil
}

// GetByID busca un lote por id. Devuelve lote_sector.ErrNotFound si no existe.
func (r *LoteSectorPostgresRepository) GetByID(ctx context.Context, loteID string) (*lotesector.Lote, error) {
	l, err := scanLoteSector(r.db.QueryRow(ctx, loteSectorSelect+` WHERE lp.id=$1`, loteID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, lotesector.ErrNotFound
	}
	if err != nil {
		if isInvalidTextRepresentation(err) {
			return nil, lotesector.ErrNotFound
		}
		return nil, fmt.Errorf("get lote by id: %w", err)
	}
	return l, nil
}

// updateModoSQL persiste el modo vigente del lote. El caller ya validó el modo
// y la transición.
const updateModoSQL = `UPDATE lotes_productivos SET modo = $2 WHERE id = $1`

// UpdateModo persiste el modo vigente del lote. No valida transiciones — el
// caller lo hace. Devuelve ErrNotFound si el lote no existe (o el id no es un
// UUID válido).
func (r *LoteSectorPostgresRepository) UpdateModo(ctx context.Context, loteID, modo string) error {
	tag, err := r.db.Exec(ctx, updateModoSQL, loteID, modo)
	if err != nil {
		if isInvalidTextRepresentation(err) {
			return lotesector.ErrNotFound
		}
		return fmt.Errorf("update lote modo: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return lotesector.ErrNotFound
	}
	return nil
}

// ListAbiertos devuelve todos los lotes ABIERTO ordenados por inicio.
func (r *LoteSectorPostgresRepository) ListAbiertos(ctx context.Context) ([]lotesector.Lote, error) {
	rows, err := r.db.Query(ctx, loteSectorSelect+`
		WHERE lp.estado='ABIERTO'
		ORDER BY lp.inicio_at, lp.id`)
	if err != nil {
		return nil, fmt.Errorf("list lotes abiertos: %w", err)
	}
	defer rows.Close()

	out := []lotesector.Lote{}
	for rows.Next() {
		l, serr := scanLoteSector(rows)
		if serr != nil {
			return nil, fmt.Errorf("list lotes abiertos: scan: %w", serr)
		}
		out = append(out, *l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list lotes abiertos: rows iteration: %w", err)
	}
	return out, nil
}

// updateParametrosControlSQL aplica un tuning parcial: COALESCE preserva el
// valor vigente cuando el parámetro llega NULL.
const updateParametrosControlSQL = `
	UPDATE lotes_productivos SET
		tolerancia_temp_c = COALESCE($2::numeric, tolerancia_temp_c),
		umbral_aceptacion_crudo = COALESCE($3::numeric, umbral_aceptacion_crudo),
		umbral_aceptacion_quemado = COALESCE($4::numeric, umbral_aceptacion_quemado),
		delta_temp1_max_c = COALESCE($5::numeric, delta_temp1_max_c),
		delta_vel_max = COALESCE($6::numeric, delta_vel_max),
		updated_at = now()
	WHERE id = $1`

// UpdateParametrosControl aplica el tuning parcial del lote. Devuelve
// ErrNotFound si el lote no existe (o el id no es un UUID válido).
func (r *LoteSectorPostgresRepository) UpdateParametrosControl(ctx context.Context, loteID string, p lotesector.ParametrosControl) error {
	tag, err := r.db.Exec(ctx, updateParametrosControlSQL,
		loteID, p.ToleranciaTempC, p.UmbralAceptacionCrudo, p.UmbralAceptacionQuemado,
		p.DeltaTemp1MaxC, p.DeltaVelMax)
	if err != nil {
		if isInvalidTextRepresentation(err) {
			return lotesector.ErrNotFound
		}
		return fmt.Errorf("update parametros control: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return lotesector.ErrNotFound
	}
	return nil
}

// UpdateSetpointsAplicados persiste los setpoints confirmados por el ack de la
// Pi. Devuelve ErrNotFound si el lote no existe.
func (r *LoteSectorPostgresRepository) UpdateSetpointsAplicados(ctx context.Context, loteID string, temp, vel float64) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE lotes_productivos
		SET temp1_objetivo = $2, vel_objetivo = $3, updated_at = now()
		WHERE id = $1`, loteID, temp, vel)
	if err != nil {
		if isInvalidTextRepresentation(err) {
			return lotesector.ErrNotFound
		}
		return fmt.Errorf("update setpoints aplicados: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return lotesector.ErrNotFound
	}
	return nil
}

// Abrir es get-or-create por sector. Toma un advisory lock transaccional por
// sector, resuelve primero la idempotencia por clave (acotada al sector) y luego
// el lote abierto vigente; sólo si no hay ninguno inserta un lote ABIERTO nuevo.
// Una violación única de uq_lotes_abierto_por_sector (carrera) se recupera
// releyendo el lote ganador; una de uq_lotes_abrir_idem (clave usada en otro
// sector) devuelve ErrIdempotencyKeyConflicto.
func (r *LoteSectorPostgresRepository) Abrir(ctx context.Context, params lotesector.AbrirParams) (*lotesector.Lote, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("abrir lote: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('lote-sector:'||$1::text,0))`, params.SectorID); err != nil {
		return nil, false, fmt.Errorf("abrir lote: advisory lock: %w", err)
	}

	// Idempotencia por clave de apertura: misma clave + mismo sector → mismo
	// lote. El lookup está acotado al sector: la clave es única global, así que
	// una clave de otro sector no debe devolver ese lote como si fuera propio.
	if params.IdempotencyKey != "" {
		existing, qerr := scanLoteSector(tx.QueryRow(ctx,
			loteSectorSelect+` WHERE lp.abrir_idempotency_key=$1 AND lp.sector_id=$2`,
			params.IdempotencyKey, params.SectorID))
		if qerr == nil {
			if err = tx.Commit(ctx); err != nil {
				return nil, false, fmt.Errorf("abrir lote: commit existing: %w", err)
			}
			return existing, false, nil
		}
		if !errors.Is(qerr, pgx.ErrNoRows) {
			return nil, false, fmt.Errorf("abrir lote: lookup idempotency key: %w", qerr)
		}
	}

	// Un sector tiene a lo sumo un lote ABIERTO: la Pi hace attach, no error.
	open, qerr := scanLoteSector(tx.QueryRow(ctx,
		loteSectorSelect+` WHERE lp.estado='ABIERTO' AND lp.sector_id=$1`, params.SectorID))
	if qerr == nil {
		if err = tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("abrir lote: commit open: %w", err)
		}
		return open, false, nil
	}
	if !errors.Is(qerr, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("abrir lote: lookup open: %w", qerr)
	}

	var abrirKey *string
	if params.IdempotencyKey != "" {
		abrirKey = &params.IdempotencyKey
	}
	var abiertoPor *string
	if params.AbiertoPor != "" {
		abiertoPor = &params.AbiertoPor
	}
	// nil si no hay turno, para no violar el CHECK con un string vacío.
	var turno *string
	if params.Turno != "" {
		turno = &params.Turno
	}

	// Parámetros normalizados para el INSERT. modo default MANUAL si vacío.
	modo := params.Modo
	if modo == "" {
		modo = lotesector.ModoManual
	}

	// Pesos: si no llegaron, se persiste PesosObjetivoDefault.
	pesos := params.PesosObjetivo
	if pesos == nil {
		def := lotesector.PesosObjetivoDefault
		pesos = &def
	}
	pesosJSON, err := json.Marshal(pesos)
	if err != nil {
		return nil, false, fmt.Errorf("abrir lote: marshal pesos_objetivo: %w", err)
	}

	// Si IniciadoManualmente=true y llega EstadoInicial, sembramos los setpoints
	// del lote con las lecturas reales para que el loop arranque en fase de
	// adaptación. Si no, todos NULL hasta el primer despacho.
	var temp1Seed, velSeed *float64
	if params.IniciadoManualmente && params.EstadoInicial != nil {
		ei := params.EstadoInicial
		t1 := ei.TempObjetivo
		temp1Seed = &t1
		v := ei.VelObjetivo
		velSeed = &v
	} else {
		// Setpoints de la receta del producto, resueltos por el service. Un
		// arranque manual con EstadoInicial tiene prioridad sobre ellos.
		temp1Seed = params.Temp1Objetivo
		velSeed = params.VelObjetivo
	}

	var newID string
	err = tx.QueryRow(ctx, `
		INSERT INTO lotes_productivos
			(producto_id, inicio_at, estado, sector_id, abierto_por, abrir_idempotency_key, turno, updated_at,
			 modo, iniciado_manualmente,
			 ventana_evaluacion_segundos, ventana_actualizacion_segundos,
			 tolerancia_temp_c, umbral_aceptacion_crudo, umbral_aceptacion_quemado,
			 pesos_objetivo,
			 temp1_objetivo, vel_objetivo)
		VALUES ($1, now(), 'ABIERTO', $2, $3, $4, $5, now(),
		        $6, $7,
		        $8, $9,
		        $10, $11, $12,
		        $13,
		        $14, $15)
		RETURNING id`,
		params.ProductoID, params.SectorID, abiertoPor, abrirKey, turno,
		modo, params.IniciadoManualmente,
		params.VentanaEvaluacionSegundos, params.VentanaActualizacionSegundos,
		params.ToleranciaTempC, params.UmbralAceptacionCrudo, params.UmbralAceptacionQuemado,
		pesosJSON,
		temp1Seed, velSeed).Scan(&newID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgErrCodeUniqueViolation {
			// Una transacción ya falló, así que se descarta y se resuelve según
			// el índice que disparó la violación.
			_ = tx.Rollback(ctx)
			switch pgErr.ConstraintName {
			case "uq_lotes_abrir_idem":
				// La clave es única global: si chocó, otro sector ya la usó y
				// el lote ganador no pertenece al sector solicitante.
				return nil, false, lotesector.ErrIdempotencyKeyConflicto
			default:
				// uq_lotes_abierto_por_sector (u otra carrera): relectura en una
				// conexión nueva del lote abierto del sector.
				winner, werr := scanLoteSector(r.db.QueryRow(ctx,
					loteSectorSelect+` WHERE lp.estado='ABIERTO' AND lp.sector_id=$1`, params.SectorID))
				if werr != nil {
					return nil, false, fmt.Errorf("abrir lote: resolve duplicate: %w", werr)
				}
				return winner, false, nil
			}
		}
		return nil, false, fmt.Errorf("abrir lote: insert: %w", err)
	}

	created, qerr := scanLoteSector(tx.QueryRow(ctx, loteSectorSelect+` WHERE lp.id=$1`, newID))
	if qerr != nil {
		return nil, false, fmt.Errorf("abrir lote: read created: %w", qerr)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("abrir lote: commit: %w", err)
	}
	return created, true, nil
}

// RegistrarEventos aplica un batch de eventos sobre un lote ABIERTO del sector.
// Bloquea el lote con FOR UPDATE, valida sector/producto/estado, inserta cada
// evento deduplicando por evento_id y actualiza los conteos con los aceptados.
// Persiste en dispositivo_id la procedencia del reporte (NULL si deviceID es
// vacío). Los eventos con estado NULL se aceptan pero no suman a ningún bucket ni
// al total; un bucket que nunca recibe eventos permanece NULL. La ventana de
// inactividad (ultimo_evento_en) sólo se extiende cuando el batch acepta al
// menos un evento: un reintento que sólo trae duplicados no la mueve.
func (r *LoteSectorPostgresRepository) RegistrarEventos(ctx context.Context, loteID, sectorID, deviceID string, eventos []lotesector.Evento) (int, int, *lotesector.Lote, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("registrar eventos: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		dbSectorID *string
		dbEstado   string
		dbProducto string
	)
	err = tx.QueryRow(ctx, `
		SELECT sector_id, estado, producto_id
		FROM lotes_productivos
		WHERE id=$1
		FOR UPDATE`, loteID).Scan(&dbSectorID, &dbEstado, &dbProducto)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil, lotesector.ErrNotFound
	}
	if err != nil {
		if isInvalidTextRepresentation(err) {
			return 0, 0, nil, lotesector.ErrNotFound
		}
		return 0, 0, nil, fmt.Errorf("registrar eventos: lock lote: %w", err)
	}
	if dbSectorID == nil || *dbSectorID != sectorID {
		return 0, 0, nil, lotesector.ErrAjeno
	}
	if dbEstado != lotesector.EstadoAbierto {
		return 0, 0, nil, lotesector.ErrCerrado
	}
	// Validación previa: si algún evento referencia otro producto se rechaza el
	// batch completo sin aplicar nada (no hay aplicación parcial).
	for i := range eventos {
		if eventos[i].ProductoID != dbProducto {
			return 0, 0, nil, lotesector.ErrProductoInconsistente
		}
	}

	aceptados := 0
	var okN, crudoN, quemadoN int
	var reporter *string
	if deviceID != "" {
		reporter = &deviceID
	}
	for i := range eventos {
		ev := eventos[i]
		cmd, ierr := tx.Exec(ctx, `
			INSERT INTO eventos_lote
				(lote_id, evento_id, producto_id, estado, confianza, pista, frame, modelo_id, momento, dispositivo_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (evento_id) DO NOTHING`,
			loteID, ev.EventoID, ev.ProductoID, ev.Estado, ev.Confianza, ev.Pista, ev.Frame, ev.ModeloID, ev.Momento, reporter)
		if ierr != nil {
			return 0, 0, nil, fmt.Errorf("registrar eventos: insert: %w", ierr)
		}
		if cmd.RowsAffected() == 1 {
			aceptados++
			if ev.Estado != nil {
				switch *ev.Estado {
				case lotesector.EstadoOK:
					okN++
				case lotesector.EstadoCrudo:
					crudoN++
				case lotesector.EstadoQuemado:
					quemadoN++
				}
			}
		}
	}
	duplicados := len(eventos) - aceptados

	// Sólo se tocan los buckets con al menos un evento aceptado (COALESCE+n),
	// de modo que un estado sin eventos permanece NULL. El total se recalcula
	// como la suma de los buckets resultantes usando los deltas aceptados. La
	// ventana de inactividad avanza por eventos aceptados (incluidos los de
	// estado NULL, que no suman a ningún bucket): un reintento sólo-duplicados
	// no debe extenderla.
	if _, err = tx.Exec(ctx, `
		UPDATE lotes_productivos
		SET correctos = CASE WHEN $1::int > 0 THEN COALESCE(correctos,0)+$1::int ELSE correctos END,
		    crudas    = CASE WHEN $2::int > 0 THEN COALESCE(crudas,0)+$2::int ELSE crudas END,
		    quemados  = CASE WHEN $3::int > 0 THEN COALESCE(quemados,0)+$3::int ELSE quemados END,
		    total_unidades = COALESCE(correctos,0)+COALESCE(crudas,0)+COALESCE(quemados,0)+($1::int+$2::int+$3::int),
		    ultimo_evento_en = CASE WHEN $5::int > 0 THEN now() ELSE ultimo_evento_en END,
		    updated_at = now()
		WHERE id=$4`, okN, crudoN, quemadoN, loteID, aceptados); err != nil {
		return 0, 0, nil, fmt.Errorf("registrar eventos: update conteos: %w", err)
	}

	lote, err := scanLoteSector(tx.QueryRow(ctx, loteSectorSelect+` WHERE lp.id=$1`, loteID))
	if err != nil {
		return 0, 0, nil, fmt.Errorf("registrar eventos: read updated: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, 0, nil, fmt.Errorf("registrar eventos: commit: %w", err)
	}
	return aceptados, duplicados, lote, nil
}

// Cerrar fija los conteos finales autoritativos y marca el lote CERRADO. Si el
// lote ya estaba cerrado devuelve su estado actual con yaCerrado=true, para que
// el reintento del mismo cierre sea idempotente.
func (r *LoteSectorPostgresRepository) Cerrar(ctx context.Context, params lotesector.CerrarParams) (*lotesector.Lote, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("cerrar lote: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		dbSectorID *string
		dbEstado   string
	)
	err = tx.QueryRow(ctx, `
		SELECT sector_id, estado
		FROM lotes_productivos
		WHERE id=$1
		FOR UPDATE`, params.LoteID).Scan(&dbSectorID, &dbEstado)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, lotesector.ErrNotFound
	}
	if err != nil {
		if isInvalidTextRepresentation(err) {
			return nil, false, lotesector.ErrNotFound
		}
		return nil, false, fmt.Errorf("cerrar lote: lock: %w", err)
	}
	if dbSectorID == nil || *dbSectorID != params.SectorID {
		return nil, false, lotesector.ErrAjeno
	}

	if dbEstado == lotesector.EstadoCerrado {
		lote, qerr := scanLoteSector(tx.QueryRow(ctx, loteSectorSelect+` WHERE lp.id=$1`, params.LoteID))
		if qerr != nil {
			return nil, false, fmt.Errorf("cerrar lote: read closed: %w", qerr)
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("cerrar lote: commit closed: %w", err)
		}
		return lote, true, nil
	}

	var motivo *string
	if params.Motivo != "" {
		motivo = &params.Motivo
	}
	var cerrarKey *string
	if params.IdempotencyKey != "" {
		cerrarKey = &params.IdempotencyKey
	}

	if _, err = tx.Exec(ctx, `
		UPDATE lotes_productivos
		SET correctos = $1,
		    crudas = $2,
		    quemados = $3,
		    total_unidades = $4,
		    estado = 'CERRADO',
		    fin_at = now(),
		    motivo_cierre = $5,
		    cerrar_idempotency_key = COALESCE($6, cerrar_idempotency_key),
		    confianza_promedio = (
		        SELECT AVG(confianza) FROM eventos_lote
		        WHERE lote_id = $7 AND confianza IS NOT NULL
		    ),
		    updated_at = now()
		WHERE id = $7`,
		params.Conteos.OK, params.Conteos.Crudo, params.Conteos.Quemado,
		params.Conteos.Total, motivo, cerrarKey, params.LoteID); err != nil {
		return nil, false, fmt.Errorf("cerrar lote: update: %w", err)
	}

	lote, err := scanLoteSector(tx.QueryRow(ctx, loteSectorSelect+` WHERE lp.id=$1`, params.LoteID))
	if err != nil {
		return nil, false, fmt.Errorf("cerrar lote: read: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("cerrar lote: commit: %w", err)
	}
	return lote, false, nil
}

// confianzaPorClaseSQL agrega la confianza de las últimas detecciones del lote,
// agrupada por clase. El subquery se queda con las N más recientes (orden de
// llegada al backend) y el agregado ignora las filas sin estado o sin confianza.
const confianzaPorClaseSQL = `
	SELECT estado, AVG(confianza), STDDEV_SAMP(confianza), COUNT(*)
	FROM (
		SELECT estado, confianza
		FROM eventos_lote
		WHERE lote_id = $1 AND estado IS NOT NULL AND confianza IS NOT NULL
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	) ultimas
	GROUP BY estado
	ORDER BY estado`

// ConfianzaPorClase agrega la confianza del modelo por clase sobre las últimas
// `limite` detecciones del lote. Devuelve una slice vacía (nunca nil) cuando no
// hay muestras. Un lote_id no-UUID se traduce a ErrNotFound.
func (r *LoteSectorPostgresRepository) ConfianzaPorClase(ctx context.Context, loteID string, limite int) ([]lotesector.ConfianzaClase, error) {
	rows, err := r.db.Query(ctx, confianzaPorClaseSQL, loteID, limite)
	if err != nil {
		if isInvalidTextRepresentation(err) {
			return nil, lotesector.ErrNotFound
		}
		return nil, fmt.Errorf("confianza por clase: query: %w", err)
	}
	defer rows.Close()

	out := []lotesector.ConfianzaClase{}
	for rows.Next() {
		var c lotesector.ConfianzaClase
		if serr := rows.Scan(&c.Clase, &c.ConfianzaMedia, &c.ConfianzaStddev, &c.Muestras); serr != nil {
			if isInvalidTextRepresentation(serr) {
				return nil, lotesector.ErrNotFound
			}
			return nil, fmt.Errorf("confianza por clase: scan: %w", serr)
		}
		out = append(out, c)
	}
	if rerr := rows.Err(); rerr != nil {
		if isInvalidTextRepresentation(rerr) {
			return nil, lotesector.ErrNotFound
		}
		return nil, fmt.Errorf("confianza por clase: rows iteration: %w", rerr)
	}
	return out, nil
}

// Historial devuelve una página de lotes del sector más nuevos primero, con un
// cursor opaco (inicio_at, id) y filtro opcional por producto, junto con el
// total que matchea los filtros (sin cursor).
func (r *LoteSectorPostgresRepository) Historial(ctx context.Context, params lotesector.HistorialParams) ([]lotesector.Lote, int, error) {
	limite := params.Limite
	if limite <= 0 {
		limite = 20
	}

	var where strings.Builder
	where.WriteString(` WHERE lp.sector_id = $1`)
	args := []any{params.SectorID}
	if params.ProductoID != "" {
		args = append(args, params.ProductoID)
		where.WriteString(fmt.Sprintf(` AND lp.producto_id = $%d`, len(args)))
	}
	if params.AntesDe != nil {
		args = append(args, *params.AntesDe)
		timeIdx := len(args)
		args = append(args, params.AntesDeID)
		idIdx := len(args)
		where.WriteString(fmt.Sprintf(
			` AND (lp.inicio_at < $%d OR (lp.inicio_at = $%d AND lp.id < $%d))`, timeIdx, timeIdx, idIdx))
	}
	args = append(args, limite)
	limitIdx := len(args)

	query := loteSectorSelect + where.String() +
		fmt.Sprintf(` ORDER BY lp.inicio_at DESC, lp.id DESC LIMIT $%d`, limitIdx)
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		if isInvalidTextRepresentation(err) {
			return nil, 0, lotesector.ErrPayloadInvalido
		}
		return nil, 0, fmt.Errorf("historial lotes: query: %w", err)
	}
	defer rows.Close()

	out := []lotesector.Lote{}
	for rows.Next() {
		l, serr := scanLoteSector(rows)
		if serr != nil {
			if isInvalidTextRepresentation(serr) {
				return nil, 0, lotesector.ErrPayloadInvalido
			}
			return nil, 0, fmt.Errorf("historial lotes: scan: %w", serr)
		}
		out = append(out, *l)
	}
	if err := rows.Err(); err != nil {
		if isInvalidTextRepresentation(err) {
			return nil, 0, lotesector.ErrPayloadInvalido
		}
		return nil, 0, fmt.Errorf("historial lotes: rows iteration: %w", err)
	}

	var countWhere strings.Builder
	countWhere.WriteString(` WHERE sector_id = $1`)
	countArgs := []any{params.SectorID}
	if params.ProductoID != "" {
		countArgs = append(countArgs, params.ProductoID)
		countWhere.WriteString(fmt.Sprintf(` AND producto_id = $%d`, len(countArgs)))
	}
	var total int
	if err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM lotes_productivos`+countWhere.String(), countArgs...).Scan(&total); err != nil {
		if isInvalidTextRepresentation(err) {
			return nil, 0, lotesector.ErrPayloadInvalido
		}
		return nil, 0, fmt.Errorf("historial lotes: count: %w", err)
	}
	return out, total, nil
}

// CountEventosEnVentana cuenta los eventos del lote cuyo timestamp cae en
// [desde, hasta] agrupados por estado. Los NULL se omiten de cualquier bucket.
// total==0 cuando la ventana está vacía; el caller lo traduce a z_observado=NULL.
func (r *LoteSectorPostgresRepository) CountEventosEnVentana(
	ctx context.Context, loteID string, desde, hasta time.Time,
) (okN, crudoN, quemadoN int, err error) {
	rows, qerr := r.db.Query(ctx, `
		SELECT estado, COUNT(*)
		FROM eventos_lote
		WHERE lote_id = $1 AND momento >= $2 AND momento < $3
		GROUP BY estado`,
		loteID, desde, hasta)
	if qerr != nil {
		return 0, 0, 0, fmt.Errorf("count eventos en ventana: %w", qerr)
	}
	defer rows.Close()
	for rows.Next() {
		var estado *string
		var n int
		if scanErr := rows.Scan(&estado, &n); scanErr != nil {
			return 0, 0, 0, fmt.Errorf("count eventos scan: %w", scanErr)
		}
		if estado == nil {
			continue
		}
		switch *estado {
		case lotesector.EstadoOK:
			okN = n
		case lotesector.EstadoCrudo:
			crudoN = n
		case lotesector.EstadoQuemado:
			quemadoN = n
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return 0, 0, 0, fmt.Errorf("count eventos rows: %w", rowsErr)
	}
	return okN, crudoN, quemadoN, nil
}

// ConfianzaPromedioEnVentana agrega la confianza de detección reportada por la
// Pi SALIDA en eventos_lote para [desde, hasta). Ignora las filas sin
// confianza; muestras==0 cuando la ventana no tiene detecciones confiables.
func (r *LoteSectorPostgresRepository) ConfianzaPromedioEnVentana(
	ctx context.Context, loteID string, desde, hasta time.Time,
) (promedio float64, muestras int, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(AVG(confianza),0), COUNT(confianza)
		FROM eventos_lote
		WHERE lote_id = $1 AND momento >= $2 AND momento < $3 AND confianza IS NOT NULL`,
		loteID, desde, hasta).Scan(&promedio, &muestras)
	if err != nil {
		return 0, 0, fmt.Errorf("confianza promedio en ventana: %w", err)
	}
	return promedio, muestras, nil
}
