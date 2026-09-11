package schema

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// setupTestPool crea un pool contra la base de datos de test. Usa
// TEST_DATABASE_URL (nunca el .env de producción); si no está seteada,
// el test se saltea limpiamente.
func setupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	connStr := os.Getenv("TEST_DATABASE_URL")
	if connStr == "" {
		t.Skip("TEST_DATABASE_URL no configurada — saltando test de integración (no tocar producción)")
	}

	pool, err := pgxpool.New(context.Background(), connStr)
	require.NoError(t, err, "fallo al crear pool de conexión a PostgreSQL de test")

	t.Cleanup(pool.Close)
	return pool
}

// TestApply_EsIdempotente verifica que el schema pueda ejecutarse dos veces
// consecutivas sin error (migración segura en cada arranque).
func TestApply_EsIdempotente(t *testing.T) {
	pool := setupTestPool(t)
	ctx := context.Background()

	require.NoError(t, Apply(ctx, pool), "primera aplicación del schema falló")
	require.NoError(t, Apply(ctx, pool), "segunda aplicación del schema falló (no es idempotente)")
}

// TestApply_CreaColumnaAiProcessorPct verifica que la migración SCA-172
// (ai_processor_pct en metricas_dispositivo) quede presente tras aplicar
// el schema, incluso sobre una base preexistente.
func TestApply_CreaColumnaAiProcessorPct(t *testing.T) {
	pool := setupTestPool(t)
	ctx := context.Background()

	require.NoError(t, Apply(ctx, pool), "aplicación del schema falló")

	var existe bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_name = 'metricas_dispositivo'
			  AND column_name = 'ai_processor_pct'
		)
	`).Scan(&existe)
	require.NoError(t, err, "fallo al consultar information_schema")
	require.True(t, existe, "la columna ai_processor_pct no existe tras aplicar el schema")
}

// TestApply_CreaColumnaWhepURL verifica que la migración de cámaras por
// dispositivo (whep_url en dispositivos) quede presente tras aplicar el
// schema, incluso sobre una base preexistente.
func TestApply_CreaColumnaWhepURL(t *testing.T) {
	pool := setupTestPool(t)
	ctx := context.Background()

	require.NoError(t, Apply(ctx, pool), "aplicación del schema falló")

	var existe bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_name = 'dispositivos'
			  AND column_name = 'whep_url'
		)
	`).Scan(&existe)
	require.NoError(t, err, "fallo al consultar information_schema")
	require.True(t, existe, "la columna whep_url no existe tras aplicar el schema")
}
