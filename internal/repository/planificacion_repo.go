package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/planificacion"
)

// PlanificacionPostgresRepository implementa planificacion.Repository sobre
// las tablas plan_produccion y eventos_parada.
type PlanificacionPostgresRepository struct {
	db *pgxpool.Pool
}

// NewPlanificacionPostgresRepository crea el repositorio de planificación.
func NewPlanificacionPostgresRepository(db *pgxpool.Pool) *PlanificacionPostgresRepository {
	return &PlanificacionPostgresRepository{db: db}
}

// SectoresExistentes devuelve el subconjunto de ids presentes en sectores.
func (r *PlanificacionPostgresRepository) SectoresExistentes(ctx context.Context, ids []string) (map[string]bool, error) {
	return r.existentes(ctx, `SELECT id FROM sectores WHERE id = ANY($1)`, ids)
}

// ProductosExistentes devuelve el subconjunto de ids presentes en productos.
// Compara como texto para tolerar ids malformados sin error de cast.
func (r *PlanificacionPostgresRepository) ProductosExistentes(ctx context.Context, ids []string) (map[string]bool, error) {
	return r.existentes(ctx, `SELECT id::text FROM productos WHERE id::text = ANY($1)`, ids)
}

// LotesExistentes devuelve el subconjunto de ids presentes en lotes_productivos.
func (r *PlanificacionPostgresRepository) LotesExistentes(ctx context.Context, ids []string) (map[string]bool, error) {
	return r.existentes(ctx, `SELECT id::text FROM lotes_productivos WHERE id::text = ANY($1)`, ids)
}

// existentes ejecuta un SELECT de una sola columna de texto y arma el set.
func (r *PlanificacionPostgresRepository) existentes(ctx context.Context, query string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("planificacion: consultar existentes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("planificacion: scan existentes: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("planificacion: rows existentes: %w", err)
	}
	return out, nil
}

// UpsertPlan inserta o actualiza el plan por su clave única
// (sector_id, inicio_at, producto_id) dentro de una transacción.
func (r *PlanificacionPostgresRepository) UpsertPlan(ctx context.Context, rows []planificacion.PlanRow) (int, int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("planificacion: begin plan: %w", err)
	}
	defer tx.Rollback(ctx)

	insertadas, actualizadas := 0, 0
	for _, row := range rows {
		var insertada bool
		err := tx.QueryRow(ctx, `
			INSERT INTO plan_produccion
				(sector_id, producto_id, fecha, turno, inicio_at, fin_at, kg_plan)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (sector_id, inicio_at, producto_id) DO UPDATE SET
				fecha = EXCLUDED.fecha,
				turno = EXCLUDED.turno,
				fin_at = EXCLUDED.fin_at,
				kg_plan = EXCLUDED.kg_plan,
				updated_at = now()
			RETURNING (xmax = 0)`,
			row.SectorID, row.ProductoID, row.Fecha, row.Turno,
			row.InicioAt, row.FinAt, row.KgPlan).Scan(&insertada)
		if err != nil {
			return 0, 0, fmt.Errorf("planificacion: upsert plan: %w", err)
		}
		if insertada {
			insertadas++
		} else {
			actualizadas++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("planificacion: commit plan: %w", err)
	}
	return insertadas, actualizadas, nil
}

// UpsertParadas inserta o actualiza paradas por (sector_id, inicio_at, causa_codigo).
func (r *PlanificacionPostgresRepository) UpsertParadas(ctx context.Context, rows []planificacion.ParadaRow) (int, int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("planificacion: begin paradas: %w", err)
	}
	defer tx.Rollback(ctx)

	insertadas, actualizadas := 0, 0
	for _, row := range rows {
		var insertada bool
		err := tx.QueryRow(ctx, `
			INSERT INTO eventos_parada
				(sector_id, lote_id, inicio_at, fin_at, causa_codigo, tipo, notas)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (sector_id, inicio_at, causa_codigo) DO UPDATE SET
				lote_id = EXCLUDED.lote_id,
				fin_at = EXCLUDED.fin_at,
				tipo = EXCLUDED.tipo,
				notas = EXCLUDED.notas
			RETURNING (xmax = 0)`,
			row.SectorID, nullIfEmpty(row.LoteID), row.InicioAt, row.FinAt,
			row.CausaCodigo, row.Tipo, nullIfEmpty(row.Notas)).Scan(&insertada)
		if err != nil {
			return 0, 0, fmt.Errorf("planificacion: upsert parada: %w", err)
		}
		if insertada {
			insertadas++
		} else {
			actualizadas++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("planificacion: commit paradas: %w", err)
	}
	return insertadas, actualizadas, nil
}

// nullIfEmpty convierte una cadena vacía en NULL para columnas nullable.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ListPlan lista el plan del sector (vacío = todos) en [desde, hasta).
func (r *PlanificacionPostgresRepository) ListPlan(ctx context.Context, sectorID string, desde, hasta time.Time) ([]planificacion.PlanRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT sector_id, producto_id::text, fecha, turno, inicio_at, fin_at, kg_plan
		FROM plan_produccion
		WHERE ($1 = '' OR sector_id = $1) AND inicio_at >= $2 AND inicio_at < $3
		ORDER BY inicio_at, producto_id`, sectorID, desde, hasta)
	if err != nil {
		return nil, fmt.Errorf("planificacion: list plan: %w", err)
	}
	defer rows.Close()

	out := []planificacion.PlanRow{}
	for rows.Next() {
		var row planificacion.PlanRow
		if err := rows.Scan(&row.SectorID, &row.ProductoID, &row.Fecha, &row.Turno,
			&row.InicioAt, &row.FinAt, &row.KgPlan); err != nil {
			return nil, fmt.Errorf("planificacion: scan plan: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("planificacion: rows plan: %w", err)
	}
	return out, nil
}

// PlanSolapados lista el plan del sector que solapa [desde, hasta) con
// semántica half-open: inicio_at < hasta AND fin_at > desde.
func (r *PlanificacionPostgresRepository) PlanSolapados(ctx context.Context, sectorID string, desde, hasta time.Time) ([]planificacion.PlanRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT sector_id, producto_id::text, fecha, turno, inicio_at, fin_at, kg_plan
		FROM plan_produccion
		WHERE sector_id = $1 AND inicio_at < $3 AND fin_at > $2
		ORDER BY inicio_at`, sectorID, desde, hasta)
	if err != nil {
		return nil, fmt.Errorf("planificacion: plan solapados: %w", err)
	}
	defer rows.Close()

	out := []planificacion.PlanRow{}
	for rows.Next() {
		var row planificacion.PlanRow
		if err := rows.Scan(&row.SectorID, &row.ProductoID, &row.Fecha, &row.Turno,
			&row.InicioAt, &row.FinAt, &row.KgPlan); err != nil {
			return nil, fmt.Errorf("planificacion: scan plan solapados: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("planificacion: rows plan solapados: %w", err)
	}
	return out, nil
}

// ListParadas lista las paradas del sector (vacío = todos) en [desde, hasta).
func (r *PlanificacionPostgresRepository) ListParadas(ctx context.Context, sectorID string, desde, hasta time.Time) ([]planificacion.ParadaRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT sector_id, lote_id::text, inicio_at, fin_at, causa_codigo, tipo, notas
		FROM eventos_parada
		WHERE ($1 = '' OR sector_id = $1) AND inicio_at >= $2 AND inicio_at < $3
		ORDER BY inicio_at`, sectorID, desde, hasta)
	if err != nil {
		return nil, fmt.Errorf("planificacion: list paradas: %w", err)
	}
	defer rows.Close()

	out := []planificacion.ParadaRow{}
	for rows.Next() {
		var (
			row    planificacion.ParadaRow
			loteID *string
			notas  *string
		)
		if err := rows.Scan(&row.SectorID, &loteID, &row.InicioAt, &row.FinAt,
			&row.CausaCodigo, &row.Tipo, &notas); err != nil {
			return nil, fmt.Errorf("planificacion: scan parada: %w", err)
		}
		if loteID != nil {
			row.LoteID = *loteID
		}
		if notas != nil {
			row.Notas = *notas
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("planificacion: rows paradas: %w", err)
	}
	return out, nil
}

// art es la zona de negocio para serializar timestamps como -03:00.
var art = time.FixedZone("ART", -3*60*60)

// ListPlanItems lista el plan con los nombres de sector y producto.
func (r *PlanificacionPostgresRepository) ListPlanItems(ctx context.Context, f planificacion.FiltroListado) (planificacion.Listado[planificacion.PlanItem], error) {
	const where = ` WHERE ($1 = '' OR p.sector_id = $1) AND p.inicio_at >= $2 AND p.inicio_at < $3`
	args := []any{f.SectorID, f.Desde, f.Hasta}
	return listarItems(ctx, r.db,
		`SELECT count(*) FROM plan_produccion p`+where,
		`SELECT p.id::text, p.sector_id, s.nombre, p.producto_id::text, pr.nombre,
		        to_char(p.fecha, 'YYYY-MM-DD'), p.turno, p.inicio_at, p.fin_at, p.kg_plan
		 FROM plan_produccion p
		 JOIN sectores s ON s.id = p.sector_id
		 JOIN productos pr ON pr.id = p.producto_id`+where+`
		 ORDER BY p.inicio_at DESC
		 LIMIT $4`,
		args, append(args, f.Limite),
		func(rows pgx.Rows) (planificacion.PlanItem, error) {
			var it planificacion.PlanItem
			err := rows.Scan(&it.ID, &it.SectorID, &it.SectorNombre, &it.ProductoID,
				&it.ProductoNombre, &it.Fecha, &it.Turno, &it.InicioAt, &it.FinAt, &it.KgPlan)
			it.InicioAt = it.InicioAt.In(art)
			it.FinAt = it.FinAt.In(art)
			return it, err
		})
}

// ListParadaItems lista las paradas con el nombre del sector.
func (r *PlanificacionPostgresRepository) ListParadaItems(ctx context.Context, f planificacion.FiltroListado) (planificacion.Listado[planificacion.ParadaItem], error) {
	const where = ` WHERE ($1 = '' OR e.sector_id = $1) AND e.inicio_at >= $2 AND e.inicio_at < $3`
	args := []any{f.SectorID, f.Desde, f.Hasta}
	return listarItems(ctx, r.db,
		`SELECT count(*) FROM eventos_parada e`+where,
		`SELECT e.id::text, e.sector_id, s.nombre, e.lote_id::text, e.inicio_at, e.fin_at,
		        e.causa_codigo, e.tipo, e.notas
		 FROM eventos_parada e
		 JOIN sectores s ON s.id = e.sector_id`+where+`
		 ORDER BY e.inicio_at DESC
		 LIMIT $4`,
		args, append(args, f.Limite),
		func(rows pgx.Rows) (planificacion.ParadaItem, error) {
			var it planificacion.ParadaItem
			err := rows.Scan(&it.ID, &it.SectorID, &it.SectorNombre, &it.LoteID,
				&it.InicioAt, &it.FinAt, &it.CausaCodigo, &it.Tipo, &it.Notas)
			it.InicioAt = it.InicioAt.In(art)
			it.FinAt = it.FinAt.In(art)
			return it, err
		})
}

// listarItems corre el count y la lista en una tx read-only REPEATABLE READ
// para que total e items sean consistentes.
func listarItems[T any](ctx context.Context, db *pgxpool.Pool, countSQL, listSQL string, args, listArgs []any, scan func(pgx.Rows) (T, error)) (planificacion.Listado[T], error) {
	out := planificacion.Listado[T]{Items: []T{}}
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("planificacion: begin listado: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := tx.QueryRow(ctx, countSQL, args...).Scan(&out.Total); err != nil {
		return out, fmt.Errorf("planificacion: count listado: %w", err)
	}
	rows, err := tx.Query(ctx, listSQL, listArgs...)
	if err != nil {
		return out, fmt.Errorf("planificacion: listar items: %w", err)
	}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			rows.Close()
			return out, fmt.Errorf("planificacion: scan item: %w", err)
		}
		out.Items = append(out.Items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("planificacion: rows items: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("planificacion: commit listado: %w", err)
	}
	out.Truncada = out.Total > len(out.Items)
	return out, nil
}

// DeletePlan elimina una fila del plan por id.
func (r *PlanificacionPostgresRepository) DeletePlan(ctx context.Context, id string) (bool, error) {
	return r.deleteByID(ctx, `DELETE FROM plan_produccion WHERE id = $1`, id)
}

// DeleteParada elimina un evento de parada por id.
func (r *PlanificacionPostgresRepository) DeleteParada(ctx context.Context, id string) (bool, error) {
	return r.deleteByID(ctx, `DELETE FROM eventos_parada WHERE id = $1`, id)
}

func (r *PlanificacionPostgresRepository) deleteByID(ctx context.Context, query, id string) (bool, error) {
	tag, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return false, fmt.Errorf("planificacion: delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Compile-time check del contrato.
var _ planificacion.Repository = (*PlanificacionPostgresRepository)(nil)
