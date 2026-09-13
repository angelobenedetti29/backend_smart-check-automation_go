package schema

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// isolatedPool creates a disposable database from the explicitly supplied
// TEST_DATABASE_URL. It never reads .env or a deployment connection string.
func isolatedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL no configurada — se requiere PostgreSQL aislado")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	require.NoError(t, err, "no se pudo conectar al PostgreSQL de test")
	name := fmt.Sprintf("sca_test_%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, `CREATE DATABASE "`+name+`"`)
	require.NoError(t, err, "no se pudo crear la base aislada")
	require.NoError(t, admin.Close(ctx))

	cfg, err := pgxpool.ParseConfig(base)
	require.NoError(t, err)
	cfg.ConnConfig.Database = name
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	require.NoError(t, p.Ping(ctx))
	t.Cleanup(func() {
		p.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		admin, err := pgx.Connect(dropCtx, base)
		if err == nil {
			_, _ = admin.Exec(dropCtx, `DROP DATABASE "`+name+`" WITH (FORCE)`)
			_ = admin.Close(dropCtx)
		}
	})
	return p
}

func TestApply_EsIdempotente(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))
	_, err := p.Exec(ctx, `INSERT INTO dispositivos(nombre) VALUES('preserved-device')`)
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, p), "segunda aplicación no es idempotente")
	var count int
	require.NoError(t, p.QueryRow(ctx, `SELECT count(*) FROM dispositivos WHERE nombre='preserved-device'`).Scan(&count))
	require.Equal(t, 1, count, "Apply no debe resetear datos existentes")
}

func TestApply_CreaColumnasModernas(t *testing.T) {
	p := isolatedPool(t)
	require.NoError(t, Apply(context.Background(), p))
	for table, column := range map[string]string{
		"metricas_dispositivo": "ai_processor_pct",
		"dispositivos":         "whep_url",
		"lotes_productivos":    "dispositivo_id",
		"historial_consignas":  "dispositivo_id",
	} {
		var exists bool
		err := p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`, table, column).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "%s.%s no fue creada", table, column)
	}
}

func TestApply_ConcurrentPoolsNoDeadlock(t *testing.T) {
	p := isolatedPool(t)
	secondCfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	require.NoError(t, err)
	// The fixture's database name is obtained from the first pool's connection,
	// avoiding any shared or configured database name in the test.
	var databaseName string
	require.NoError(t, p.QueryRow(context.Background(), `SELECT current_database()`).Scan(&databaseName))
	secondCfg.ConnConfig.Database = databaseName
	p2, err := pgxpool.NewWithConfig(context.Background(), secondCfg)
	require.NoError(t, err)
	t.Cleanup(p2.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, pool := range []*pgxpool.Pool{p, p2} {
		wg.Add(1)
		go func(pool *pgxpool.Pool) { defer wg.Done(); results <- Apply(ctx, pool) }(pool)
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
}

func TestApply_LegacyUpgradePreservesDeviceTelemetryAndHistory(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	legacy := `
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE TABLE productos (id UUID PRIMARY KEY, nombre VARCHAR(100) NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE parametros_producto (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), producto_id UUID NOT NULL UNIQUE, peso_referencia_kg NUMERIC NOT NULL, tolerancia_peso_pct NUMERIC NOT NULL, dimension_base_cm NUMERIC NOT NULL, tolerancia_dimension_cm NUMERIC NOT NULL, temp_min NUMERIC NOT NULL, temp_max NUMERIC NOT NULL, velocidad_cinta_min NUMERIC NOT NULL, velocidad_cinta_max NUMERIC NOT NULL, activo BOOLEAN NOT NULL DEFAULT true, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE lotes_productivos (id UUID PRIMARY KEY, producto_id UUID NOT NULL, turno VARCHAR(10) NOT NULL, inicio_at TIMESTAMPTZ NOT NULL, fin_at TIMESTAMPTZ NOT NULL, total_unidades INTEGER NOT NULL, correctos INTEGER NOT NULL, quemados INTEGER NOT NULL, crudas INTEGER, correctos_kg NUMERIC NOT NULL, quemados_kg NUMERIC NOT NULL, crudos_kg NUMERIC, temp_horno_1 NUMERIC, temp_comb_horno_1 NUMERIC, temp_horno_2 NUMERIC, temp_comb_horno_2 NUMERIC, velocidad_cinta NUMERIC, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE dispositivos (id UUID PRIMARY KEY, nombre VARCHAR(100) NOT NULL, ubicacion VARCHAR(100), created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE metricas_dispositivo (id UUID PRIMARY KEY, dispositivo_id UUID NOT NULL, cpu_pct NUMERIC NOT NULL, mem_ram_disponible_mb NUMERIC NOT NULL, temp_chip NUMERIC NOT NULL, received_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE historial_consignas (id UUID PRIMARY KEY, horno_id VARCHAR(50) NOT NULL, lote_id UUID, producto_id UUID, temperatura_objetivo NUMERIC NOT NULL, velocidad_cinta_objetivo NUMERIC NOT NULL, origen VARCHAR(12) NOT NULL, usuario VARCHAR(150), exitosa BOOLEAN NOT NULL, motivo_error TEXT, temperatura_previa NUMERIC, velocidad_cinta_previa NUMERIC, creada_en TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE usuarios (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), email VARCHAR(254) NOT NULL UNIQUE, nombre VARCHAR(150) NOT NULL, rol VARCHAR(20) NOT NULL, password_hash VARCHAR(255), activo BOOLEAN NOT NULL DEFAULT true, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
`
	_, err := p.Exec(ctx, legacy)
	require.NoError(t, err)
	_, err = p.Exec(ctx, `INSERT INTO productos(id,nombre) VALUES('a1b2c3d4-5678-90ab-cdef-1234567890ab','legacy-product'); INSERT INTO parametros_producto(id,producto_id,peso_referencia_kg,tolerancia_peso_pct,dimension_base_cm,tolerancia_dimension_cm,temp_min,temp_max,velocidad_cinta_min,velocidad_cinta_max) VALUES('44444444-4444-4444-4444-444444444444','a1b2c3d4-5678-90ab-cdef-1234567890ab',.03,10,8,.5,160,180,.1,.3)`)
	require.NoError(t, err)
	deviceID := "11111111-1111-1111-1111-111111111111"
	metricID := "22222222-2222-2222-2222-222222222222"
	historyID := "33333333-3333-3333-3333-333333333333"
	_, err = p.Exec(ctx, `INSERT INTO dispositivos(id,nombre) VALUES($1,'legacy-device')`, deviceID)
	require.NoError(t, err)
	_, err = p.Exec(ctx, `INSERT INTO metricas_dispositivo(id,dispositivo_id,cpu_pct,mem_ram_disponible_mb,temp_chip) VALUES($1,$2,12,512,40)`, metricID, deviceID)
	require.NoError(t, err)
	_, err = p.Exec(ctx, `INSERT INTO historial_consignas(id,horno_id,temperatura_objetivo,velocidad_cinta_objetivo,origen,exitosa) VALUES($1,'legacy-oven',170,.2,'MANUAL',true)`, historyID)
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, p))
	var name string
	require.NoError(t, p.QueryRow(ctx, `SELECT nombre FROM dispositivos WHERE id=$1`, deviceID).Scan(&name))
	require.Equal(t, "legacy-device", name)
	var gotMetric, gotHistory int
	require.NoError(t, p.QueryRow(ctx, `SELECT count(*) FROM metricas_dispositivo WHERE id=$1`, metricID).Scan(&gotMetric))
	require.NoError(t, p.QueryRow(ctx, `SELECT count(*) FROM historial_consignas WHERE id=$1`, historyID).Scan(&gotHistory))
	require.Equal(t, 1, gotMetric)
	require.Equal(t, 1, gotHistory)
	var status string
	require.NoError(t, p.QueryRow(ctx, `SELECT auth_status FROM dispositivos WHERE id=$1`, deviceID).Scan(&status))
	require.Equal(t, "unenrolled", status)
}

func TestSchemaSQL_LockIsBeforeDDL(t *testing.T) {
	s := string(SQL)
	begin := strings.Index(s, "BEGIN;")
	lock := strings.Index(s, "pg_advisory_xact_lock")
	extension := strings.Index(s, "CREATE EXTENSION")
	require.True(t, begin >= 0 && begin < lock && lock < extension)
}
