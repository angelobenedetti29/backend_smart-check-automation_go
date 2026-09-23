package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoteProductivoRepository_LeeLoteAbierto es la regresión del 500 en
// GET /lotes-productivos: un lote ABIERTO del flujo nuevo no trae turno ni
// conteos (columnas nullable) y el read-model legado debe leerlo sin error,
// mapeando los NULL a los cero-valores del dominio.
func TestLoteProductivoRepository_LeeLoteAbierto(t *testing.T) {
	pool := repositoryTestPool(t)
	ctx := context.Background()
	repo := NewLoteProductivoPostgresRepository(pool)

	const productoID = "aaaaaaaa-0000-0000-0000-0000000000f1"
	const sectorID = "sector-lp-test"
	_, err := pool.Exec(ctx, `INSERT INTO productos (id, nombre, activo) VALUES ($1, 'Producto LP', true)`, productoID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sectores (id, nombre) VALUES ($1, 'Sector LP')`, sectorID)
	require.NoError(t, err)

	var loteID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO lotes_productivos (producto_id, inicio_at, estado, sector_id)
		VALUES ($1, now(), 'ABIERTO', $2)
		RETURNING id`, productoID, sectorID).Scan(&loteID))

	res, err := repo.GetAll("", 1, 10)
	require.NoError(t, err, "GetAll no debe fallar con un lote ABIERTO nullable")
	require.NotEmpty(t, res.Items)

	filtered, err := repo.GetAll(productoID, 1, 10)
	require.NoError(t, err, "GetAll filtrado no debe fallar con un lote ABIERTO nullable")
	require.Len(t, filtered.Items, 1)

	got, err := repo.GetByID(loteID)
	require.NoError(t, err, "GetByID no debe fallar con un lote ABIERTO nullable")
	require.Equal(t, loteID, got.ID)
	require.Equal(t, "", got.Turno)
	require.Equal(t, 0, got.TotalUnidades)
	require.Equal(t, 0, got.Correctos)
	require.Equal(t, 0, got.Quemados)
	require.Equal(t, 0.0, got.CorrectosKg)
	require.Equal(t, 0.0, got.QuemadosKg)
	require.Nil(t, got.FinAt)
}
