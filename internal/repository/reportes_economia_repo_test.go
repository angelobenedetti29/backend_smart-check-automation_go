package repository

import (
	"context"
	"testing"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reportes"
	"github.com/stretchr/testify/require"
)

func TestImpactoEconomicoIntegracion(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)
	productos := NewProductoPostgresRepository(pool)
	newProduct := func(nombre string, costo *float64) string {
		p := nuevoProductoDePrueba(nombre)
		p.CostoFabricacionUnitarioARS = costo
		out, err := productos.Create(ctx, p)
		require.NoError(t, err)
		return out.ID
	}
	pan := newProduct("Pan económico", floatPtr(125.50))
	otro := newProduct("Otra variedad", floatPtr(10.25))
	sinCosto := newProduct("Sin costo", nil)
	gratis := newProduct("Costo cero", floatPtr(0))
	_, err := pool.Exec(ctx, `INSERT INTO sectores(id,nombre) VALUES ($1,'Economía')`, testSectorID)
	require.NoError(t, err)
	art := time.FixedZone("ART", -3*60*60)
	inicio := time.Date(2026, 11, 10, 0, 0, 0, 0, art)
	periodo := reportes.Periodo{Desde: inicio, Hasta: inicio}
	insertLote := func(productoID string, crudas, quemadas *int, fecha time.Time, estado string) string {
		var id string
		err := pool.QueryRow(ctx, `INSERT INTO lotes_productivos
			(producto_id,sector_id,estado,turno,inicio_at,fin_at,correctos,crudas,quemados,total_unidades)
			VALUES ($1,$2,$3::varchar,'mañana',$4::timestamptz,CASE WHEN $3::varchar='CERRADO' THEN $4::timestamptz+interval '1 hour' END,90,$5,$6,90+COALESCE($5::int,0)+COALESCE($6::int,0)) RETURNING id`,
			productoID, testSectorID, estado, fecha, crudas, quemadas).Scan(&id)
		require.NoError(t, err)
		return id
	}
	i := func(v int) *int { return &v }
	panLote := insertLote(pan, i(2), i(3), inicio, "CERRADO")
	insertLote(otro, nil, i(3), inicio.Add(23*time.Hour), "CERRADO")
	insertLote(pan, i(100), i(100), inicio.Add(24*time.Hour), "CERRADO") // fuera del período ART
	repo := NewReportesPostgresRepository(pool)
	impacto, err := repo.ImpactoEconomico(ctx, periodo, reportes.Filtros{})
	require.NoError(t, err)
	require.Equal(t, 658.25, *impacto.PerdidaARS)
	require.EqualValues(t, 8, impacto.UnidadesMerma)
	require.Equal(t, "ARS", impacto.Moneda)

	// El resumen, la paginación y la trazabilidad comparten la valorización.
	resumen, err := repo.Resumen(ctx, periodo, reportes.Filtros{})
	require.NoError(t, err)
	require.Equal(t, impacto, &resumen.ImpactoEconomico)
	lotes, _, err := repo.Lotes(ctx, reportes.LotesParams{Periodo: periodo, Limite: 100})
	require.NoError(t, err)
	require.Len(t, lotes, 2)
	traza, err := repo.Trazabilidad(ctx, panLote)
	require.NoError(t, err)
	require.Equal(t, 627.50, *traza.Lote.PerdidaMermaARS)
	require.Equal(t, 125.50, *traza.Lote.CostoFabricacionUnitarioARS)

	insertLote(sinCosto, i(1), i(2), inicio.Add(time.Hour), "CERRADO")
	insertLote(gratis, i(1), i(1), inicio.Add(2*time.Hour), "CERRADO")
	insertLote(pan, nil, nil, inicio.Add(3*time.Hour), "ABIERTO")
	impacto, err = repo.ImpactoEconomico(ctx, periodo, reportes.Filtros{})
	require.NoError(t, err)
	require.Nil(t, impacto.PerdidaARS)
	require.Equal(t, 658.25, impacto.PerdidaValorizadaARS)
	require.EqualValues(t, 3, impacto.UnidadesSinCosto)
	require.Equal(t, 1, impacto.LotesSinConteos)
	zero, err := repo.ImpactoEconomico(ctx, periodo, reportes.Filtros{ProductoID: &gratis})
	require.NoError(t, err)
	require.Equal(t, 0.0, *zero.PerdidaARS)
	require.EqualValues(t, 2, zero.UnidadesMerma)
	turno := "tarde"
	empty, err := repo.ImpactoEconomico(ctx, periodo, reportes.Filtros{Turno: &turno})
	require.NoError(t, err)
	require.Empty(t, empty.PorProducto)
	require.Equal(t, 0.0, *empty.PerdidaARS)

	// Los costos actuales se aplican también a los lotes históricos de
	// productos retirados. No se confunde inactivo con sin costo.
	_, err = pool.Exec(ctx, `UPDATE productos SET costo_fabricacion_unitario_ars=0.10,activo=false WHERE id=$1`, otro)
	require.NoError(t, err)
	sector := testSectorID
	updated, err := repo.ImpactoEconomico(ctx, periodo, reportes.Filtros{ProductoID: &otro, SectorID: &sector})
	require.NoError(t, err)
	require.Equal(t, 0.30, *updated.PerdidaARS)
}

func TestCostoProductoPersistenciaCompatible(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)
	repo := NewProductoPostgresRepository(pool)
	p := nuevoProductoDePrueba("Costo persistido")
	p.CostoFabricacionUnitarioARS = floatPtr(125.50)
	p, err := repo.Create(ctx, p)
	require.NoError(t, err)
	require.Equal(t, 125.50, *p.CostoFabricacionUnitarioARS)
	// PUT anterior que no conoce el campo conserva el costo.
	p.CostoFabricacionUnitarioARS = nil
	p, err = repo.Update(ctx, p)
	require.NoError(t, err)
	require.Equal(t, 125.50, *p.CostoFabricacionUnitarioARS)
	// Null explícito lo elimina.
	p.CostoFabricacionUnitarioARS = nil
	p.CostoFabricacionInformado = true
	p, err = repo.Update(ctx, p)
	require.NoError(t, err)
	require.Nil(t, p.CostoFabricacionUnitarioARS)
	p.CostoFabricacionUnitarioARS = floatPtr(0)
	p, err = repo.Update(ctx, p)
	require.NoError(t, err)
	require.Equal(t, 0.0, *p.CostoFabricacionUnitarioARS)
	_, err = pool.Exec(ctx, `UPDATE productos SET costo_fabricacion_unitario_ars=-1 WHERE id=$1`, p.ID)
	require.Error(t, err)
}
