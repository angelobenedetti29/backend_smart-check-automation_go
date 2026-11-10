package repository

import (
	"context"
	"fmt"
	"math"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reportes"
	"github.com/jackc/pgx/v5"
)

type economiaQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (r *ReportesPostgresRepository) ImpactoEconomico(ctx context.Context, periodo reportes.Periodo, filtros reportes.Filtros) (*reportes.ImpactoEconomico, error) {
	f := loteFiltros{desde: periodo.Desde, hasta: periodo.Hasta.AddDate(0, 0, 1), sector: filtros.SectorID, producto: filtros.ProductoID, turno: filtros.Turno}
	return consultarImpactoEconomico(ctx, r.db, f)
}

// Una sola consulta: sin paginación ni doble conteo de telemetría. El producto
// puede estar inactivo y sigue valorizando sus lotes históricos.
func consultarImpactoEconomico(ctx context.Context, db economiaQuerier, f loteFiltros) (*reportes.ImpactoEconomico, error) {
	var b argsBuilder
	where := f.where(&b, "lp")
	rows, err := db.Query(ctx, `
		SELECT pr.id::text, pr.nombre, pr.costo_fabricacion_unitario_ars,
		       SUM(COALESCE(lp.crudas,0)::bigint + COALESCE(lp.quemados,0))::bigint,
		       COUNT(*) FILTER (WHERE lp.crudas IS NULL AND lp.quemados IS NULL)::int,
		       COALESCE(SUM((COALESCE(lp.crudas,0)::numeric + COALESCE(lp.quemados,0)) * pr.costo_fabricacion_unitario_ars),0)::float8
		FROM lotes_productivos lp JOIN productos pr ON pr.id=lp.producto_id
		WHERE `+where+`
		GROUP BY pr.id, pr.nombre, pr.costo_fabricacion_unitario_ars
		ORDER BY pr.nombre, pr.id`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("consultar impacto económico: %w", err)
	}
	defer rows.Close()
	out := &reportes.ImpactoEconomico{Moneda: "ARS", ReferenciaCosto: "actual_producto", PorProducto: []reportes.MermaProducto{}}
	for rows.Next() {
		var p reportes.MermaProducto
		var valor float64
		if err := rows.Scan(&p.ProductoID, &p.Nombre, &p.CostoUnitarioARS, &p.UnidadesMerma, &p.LotesSinConteos, &valor); err != nil {
			return nil, fmt.Errorf("leer impacto económico: %w", err)
		}
		out.UnidadesMerma += p.UnidadesMerma
		out.LotesSinConteos += p.LotesSinConteos
		if p.CostoUnitarioARS == nil {
			out.UnidadesSinCosto += p.UnidadesMerma
		}
		out.PerdidaValorizadaARS += valor
		if p.LotesSinConteos == 0 && (p.CostoUnitarioARS != nil || p.UnidadesMerma == 0) {
			p.PerdidaARS = floatPtr(valor)
		}
		out.PorProducto = append(out.PorProducto, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterar impacto económico: %w", err)
	}
	out.PerdidaValorizadaARS = math.Round(out.PerdidaValorizadaARS*100) / 100
	if out.UnidadesSinCosto == 0 && out.LotesSinConteos == 0 {
		out.PerdidaARS = floatPtr(out.PerdidaValorizadaARS)
	}
	return out, nil
}
