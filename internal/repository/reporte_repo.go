package repository

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reporte"
)

// ReportePostgresRepository implementa reporte.Repository usando PostgreSQL.
type ReportePostgresRepository struct {
	db *pgxpool.Pool
}

// NewReportePostgresRepository instancia un nuevo repositorio de reportes conectado a PostgreSQL.
func NewReportePostgresRepository(db *pgxpool.Pool) *ReportePostgresRepository {
	return &ReportePostgresRepository{db: db}
}

// roundTo2Decimals redondea un valor float64 a 2 decimales sin usar librerías externas.
func roundTo2Decimals(val float64) float64 {
	return math.Round(val*100) / 100
}

// GetKPIFinanciero ejecuta la consulta agregada para calcular el impacto económico por mermas
// respetando la historización de costos y detectando datos de costos faltantes o parciales.
func (r *ReportePostgresRepository) GetKPIFinanciero(ctx context.Context, filtro reporte.FiltroKPI) (*reporte.KPIFinanciero, error) {
	var whereClauses []string
	var args []interface{}
	argIdx := 1

	if filtro.FechaInicio != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("lp.inicio_at >= $%d", argIdx))
		args = append(args, *filtro.FechaInicio)
		argIdx++
	}

	if filtro.FechaFin != nil {
		whereClauses = append(whereClauses, fmt.Sprintf("lp.inicio_at <= $%d", argIdx))
		args = append(args, *filtro.FechaFin)
		argIdx++
	}

	if strings.TrimSpace(filtro.ProductoID) != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("lp.producto_id = $%d", argIdx))
		args = append(args, filtro.ProductoID)
		argIdx++
	}

	if strings.TrimSpace(filtro.Turno) != "" {
		whereClauses = append(whereClauses, fmt.Sprintf("lp.turno = $%d", argIdx))
		args = append(args, filtro.Turno)
		argIdx++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Query que agrupa por producto y calcula métricas históricas
	query := fmt.Sprintf(`
		SELECT
			pr.id AS producto_id,
			pr.nombre AS producto_nombre,
			COALESCE(SUM(lp.quemados + COALESCE(lp.crudas, 0)), 0)::BIGINT AS total_mermas,
			COALESCE(SUM(CASE WHEN COALESCE(lp.costo_unitario, pp.costo_unitario) IS NOT NULL THEN (lp.quemados + COALESCE(lp.crudas, 0)) ELSE 0 END), 0)::BIGINT AS mermas_con_costo,
			COALESCE(SUM(CASE WHEN COALESCE(lp.costo_unitario, pp.costo_unitario) IS NULL THEN (lp.quemados + COALESCE(lp.crudas, 0)) ELSE 0 END), 0)::BIGINT AS mermas_sin_costo,
			COALESCE(SUM((lp.quemados + COALESCE(lp.crudas, 0)) * COALESCE(lp.costo_unitario, pp.costo_unitario, 0)), 0)::NUMERIC(14,2) AS impacto_economico,
			AVG(COALESCE(lp.costo_unitario, pp.costo_unitario)) AS costo_promedio,
			pp.costo_unitario AS costo_vigente,
			COUNT(lp.id)::INT AS lotes_totales,
			COUNT(CASE WHEN COALESCE(lp.costo_unitario, pp.costo_unitario) IS NULL THEN 1 END)::INT AS lotes_sin_costo
		FROM lotes_productivos lp
		JOIN productos pr ON lp.producto_id = pr.id
		LEFT JOIN parametros_producto pp ON pr.id = pp.producto_id
		%s
		GROUP BY pr.id, pr.nombre, pp.costo_unitario
		ORDER BY impacto_economico DESC, pr.nombre ASC
	`, whereSQL)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query kpi financiero: %w", err)
	}
	defer rows.Close()

	var desgloses []reporte.DesgloseProducto
	var totalImpacto float64
	var totalMermas int
	var totalMermasConCosto int
	var totalMermasSinCosto int
	var totalLotes int
	var totalLotesSinCosto int
	var totalProductos int
	var productosSinCostoCount int

	for rows.Next() {
		var d reporte.DesgloseProducto
		var mermasConCosto int64
		var mermasSinCosto int64
		var totalMermasProd int64
		var impacto numericLoss
		var costoPromedio *float64
		var costoVigente *float64

		if err := rows.Scan(
			&d.ProductoID,
			&d.ProductoNombre,
			&totalMermasProd,
			&mermasConCosto,
			&mermasSinCosto,
			&impacto,
			&costoPromedio,
			&costoVigente,
			&d.LotesTotales,
			&d.LotesSinCosto,
		); err != nil {
			return nil, fmt.Errorf("failed to scan desglose producto: %w", err)
		}

		d.TotalMermasUnidades = int(totalMermasProd)
		if costoPromedio != nil {
			prom := roundTo2Decimals(*costoPromedio)
			d.CostoUnitarioPromedio = &prom
		}
		if costoVigente != nil {
			vig := roundTo2Decimals(*costoVigente)
			d.CostoConfiguradoVigente = &vig
		}
		d.ImpactoEconomico = roundTo2Decimals(float64(impacto))

		// Determinar si este producto tiene costos configurados (o si tiene lotes sin costo)
		d.TieneCostoConfigurado = (d.CostoConfiguradoVigente != nil || d.CostoUnitarioPromedio != nil) && d.LotesSinCosto == 0

		if !d.TieneCostoConfigurado || d.LotesSinCosto > 0 {
			productosSinCostoCount++
		}

		totalImpacto += d.ImpactoEconomico
		totalMermas += d.TotalMermasUnidades
		totalMermasConCosto += int(mermasConCosto)
		totalMermasSinCosto += int(mermasSinCosto)
		totalLotes += d.LotesTotales
		totalLotesSinCosto += d.LotesSinCosto
		totalProductos++

		desgloses = append(desgloses, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error in kpi financiero: %w", err)
	}

	if desgloses == nil {
		desgloses = []reporte.DesgloseProducto{}
	}

	tieneCostosFaltantes := productosSinCostoCount > 0 || totalLotesSinCosto > 0

	var advertencia string
	if totalLotes == 0 {
		advertencia = "No se registraron lotes de producción para el período seleccionado"
	} else if tieneCostosFaltantes {
		if productosSinCostoCount == 1 {
			advertencia = "Cálculo parcial: hay 1 producto con costo no configurado"
		} else {
			advertencia = fmt.Sprintf("Cálculo parcial: hay %d productos con costo no configurado", productosSinCostoCount)
		}
	}

	return &reporte.KPIFinanciero{
		TotalImpactoEconomico:  roundTo2Decimals(totalImpacto),
		Moneda:                 "ARS",
		TotalMermasUnidades:    totalMermas,
		TotalMermasConCosto:    totalMermasConCosto,
		TotalMermasSinCosto:    totalMermasSinCosto,
		TotalLotes:             totalLotes,
		LotesSinCosto:          totalLotesSinCosto,
		TotalProductos:         totalProductos,
		ProductosSinCostoCount: productosSinCostoCount,
		TieneCostosFaltantes:   tieneCostosFaltantes,
		Advertencia:            advertencia,
		DesgloseProductos:      desgloses,
		CalculadoAt:            time.Now().UTC(),
	}, nil
}

type numericLoss float64
