package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/oee"
)

// OEERepository implementa oee.Repository sobre PostgreSQL.
type OEERepository struct {
	db *pgxpool.Pool
}

// NewOEERepository crea el repositorio de OEE.
func NewOEERepository(db *pgxpool.Pool) *OEERepository {
	return &OEERepository{db: db}
}

// Datos ejecuta todas las consultas sobre una única transacción read-only
// REPEATABLE READ para que compartan el mismo snapshot.
func (r *OEERepository) Datos(ctx context.Context, q oee.Query) (*oee.Datos, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("oee: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// El período inclusivo se traduce a [desde, hasta+1día).
	var filtroSector any
	if q.Filtros.SectorID != nil {
		filtroSector = *q.Filtros.SectorID
	}
	hasta := q.Periodo.Hasta.AddDate(0, 0, 1)
	datos := &oee.Datos{
		Capacidades: map[string]*float64{},
		Sectores:    map[string]string{},
	}

	rows, err := tx.Query(ctx, `
		SELECT sector_id, producto_id::text, inicio_at, fin_at, kg_plan::float8
		FROM plan_produccion
		WHERE ($1::text IS NULL OR sector_id = $1) AND inicio_at < $2 AND fin_at > $3
		ORDER BY sector_id, inicio_at, producto_id`, filtroSector, hasta, q.Periodo.Desde)
	if err != nil {
		return nil, fmt.Errorf("oee: list plan: %w", err)
	}
	for rows.Next() {
		var row oee.PlanOEE
		if err := rows.Scan(&row.SectorID, &row.ProductoID, &row.InicioAt, &row.FinAt, &row.KgPlan); err != nil {
			rows.Close()
			return nil, fmt.Errorf("oee: scan plan: %w", err)
		}
		datos.Plan = append(datos.Plan, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("oee: rows plan: %w", err)
	}

	paradas, err := tx.Query(ctx, `
		SELECT sector_id, inicio_at, fin_at, tipo
		FROM eventos_parada
		WHERE ($1::text IS NULL OR sector_id = $1) AND inicio_at < $2 AND fin_at > $3
		ORDER BY sector_id, inicio_at`, filtroSector, hasta, q.Periodo.Desde)
	if err != nil {
		return nil, fmt.Errorf("oee: list paradas: %w", err)
	}
	for paradas.Next() {
		var row oee.ParadaOEE
		if err := paradas.Scan(&row.SectorID, &row.InicioAt, &row.FinAt, &row.Tipo); err != nil {
			paradas.Close()
			return nil, fmt.Errorf("oee: scan parada: %w", err)
		}
		datos.Paradas = append(datos.Paradas, row)
	}
	paradas.Close()
	if err := paradas.Err(); err != nil {
		return nil, fmt.Errorf("oee: rows paradas: %w", err)
	}

	// Los lotes usan el mismo criterio que el resumen: CERRADOS con inicio_at
	// dentro del período (sin prorratear).
	lotes, err := tx.Query(ctx, `
		SELECT sector_id, producto_id::text,
			COALESCE(correctos_kg,0)+COALESCE(crudos_kg,0)+COALESCE(quemados_kg,0),
			COALESCE(correctos_kg,0)
		FROM lotes_productivos
		WHERE estado = 'CERRADO'
		  AND ($1::text IS NULL OR sector_id = $1)
		  AND inicio_at >= $2 AND inicio_at < $3
		ORDER BY sector_id, inicio_at`, filtroSector, q.Periodo.Desde, hasta)
	if err != nil {
		return nil, fmt.Errorf("oee: list lotes: %w", err)
	}
	for lotes.Next() {
		var row oee.LoteOEE
		if err := lotes.Scan(&row.SectorID, &row.ProductoID, &row.KgTotal, &row.KgOK); err != nil {
			lotes.Close()
			return nil, fmt.Errorf("oee: scan lote: %w", err)
		}
		datos.Lotes = append(datos.Lotes, row)
	}
	lotes.Close()
	if err := lotes.Err(); err != nil {
		return nil, fmt.Errorf("oee: rows lotes: %w", err)
	}

	productos, err := tx.Query(ctx, `SELECT id::text, capacidad_nominal_kg_h::float8 FROM productos`)
	if err != nil {
		return nil, fmt.Errorf("oee: list capacidades: %w", err)
	}
	for productos.Next() {
		var id string
		var capacidad *float64
		if err := productos.Scan(&id, &capacidad); err != nil {
			productos.Close()
			return nil, fmt.Errorf("oee: scan capacidad: %w", err)
		}
		datos.Capacidades[id] = capacidad
	}
	productos.Close()
	if err := productos.Err(); err != nil {
		return nil, fmt.Errorf("oee: rows capacidades: %w", err)
	}

	sectores, err := tx.Query(ctx, `SELECT id, nombre FROM sectores`)
	if err != nil {
		return nil, fmt.Errorf("oee: list sectores: %w", err)
	}
	for sectores.Next() {
		var id, nombre string
		if err := sectores.Scan(&id, &nombre); err != nil {
			sectores.Close()
			return nil, fmt.Errorf("oee: scan sector: %w", err)
		}
		datos.Sectores[id] = nombre
	}
	sectores.Close()
	if err := sectores.Err(); err != nil {
		return nil, fmt.Errorf("oee: rows sectores: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("oee: commit tx: %w", err)
	}
	return datos, nil
}

// Compile-time check del contrato.
var _ oee.Repository = (*OEERepository)(nil)
