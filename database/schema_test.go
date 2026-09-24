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
	"golang.org/x/crypto/bcrypt"
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
	for _, tc := range []struct{ table, column string }{
		{"metricas_dispositivo", "ai_processor_pct"},
		{"dispositivos", "whep_url"},
		{"dispositivos", "secret_hash"},
		{"dispositivos", "tipo"},
		{"lotes_productivos", "dispositivo_id"},
		{"historial_consignas", "dispositivo_id"},
		{"registration_requests", "request_id"},
		{"registration_requests", "secret"},
		{"registration_requests", "tipo"},
	} {
		var exists bool
		err := p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`, tc.table, tc.column).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "%s.%s no fue creada", tc.table, tc.column)
	}
}

// TestApply_ReemplazaEnrolamientoEd25519 verifica que el registro por
// solicitud/aprobación reemplace al enrolamiento Ed25519: las tablas de
// credenciales por clave pública, invitaciones y replay ya no existen, y la
// identidad del dispositivo se guarda como secret_hash.
func TestApply_ReemplazaEnrolamientoEd25519(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))
	for _, table := range []string{"device_credentials", "device_enrollments", "device_request_replays"} {
		var exists bool
		require.NoError(t, p.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists))
		require.False(t, exists, "la tabla %s debería haber sido eliminada", table)
	}
	var exists bool
	require.NoError(t, p.QueryRow(ctx, `SELECT to_regclass('registration_requests') IS NOT NULL`).Scan(&exists))
	require.True(t, exists, "registration_requests no fue creada")

	// El registro deja el dispositivo ACTIVE con hash de secret, nunca con clave
	// pública ni fingerprint.
	_, err := p.Exec(ctx, `INSERT INTO dispositivos(nombre,auth_status,secret_hash) VALUES('registrado','active',repeat('a',64))`)
	require.NoError(t, err)
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

// TestApply_CreaEsquemaSectorLotes verifica las columnas nuevas del contrato
// rework-rb: baja lógica de productos, sectores, sector de dispositivos y el
// ciclo ABIERTO/CERRADO de lotes_productivos más eventos_lote.
func TestApply_CreaEsquemaSectorLotes(t *testing.T) {
	p := isolatedPool(t)
	require.NoError(t, Apply(context.Background(), p))
	for _, tc := range []struct{ table, column string }{
		{"productos", "activo"},
		{"sectores", "id"},
		{"sectores", "nombre"},
		{"dispositivos", "sector_id"},
		{"lotes_productivos", "sector_id"},
		{"lotes_productivos", "estado"},
		{"lotes_productivos", "abierto_por"},
		{"lotes_productivos", "ultimo_evento_en"},
		{"lotes_productivos", "motivo_cierre"},
		{"lotes_productivos", "abrir_idempotency_key"},
		{"lotes_productivos", "cerrar_idempotency_key"},
		{"eventos_lote", "evento_id"},
		{"eventos_lote", "confianza"},
		{"eventos_lote", "frame"},
	} {
		var exists bool
		err := p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`, tc.table, tc.column).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "%s.%s no fue creada", tc.table, tc.column)
	}
}

// TestApply_LotesCamposNullable verifica que las columnas del flujo legado que
// un lote ABIERTO nuevo no trae hayan quedado nullable.
func TestApply_LotesCamposNullable(t *testing.T) {
	p := isolatedPool(t)
	require.NoError(t, Apply(context.Background(), p))
	for _, column := range []string{"turno", "fin_at", "total_unidades", "correctos", "quemados", "correctos_kg", "quemados_kg"} {
		var nullable string
		err := p.QueryRow(context.Background(), `SELECT is_nullable FROM information_schema.columns WHERE table_name='lotes_productivos' AND column_name=$1`, column).Scan(&nullable)
		require.NoError(t, err)
		require.Equal(t, "YES", nullable, "%s debería ser nullable", column)
	}
}

// TestSchema_UnLoteAbiertoPorSector verifica que el índice único parcial impida
// dos lotes ABIERTO del mismo sector.
func TestSchema_UnLoteAbiertoPorSector(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	_, err := p.Exec(ctx, `INSERT INTO sectores(id,nombre) VALUES('sector-test','Sector Test')`)
	require.NoError(t, err)

	insertLote := func() error {
		_, err := p.Exec(ctx, `
			INSERT INTO lotes_productivos (producto_id, inicio_at, estado, sector_id)
			VALUES ('a1b2c3d4-5678-90ab-cdef-1234567890ab', now(), 'ABIERTO', 'sector-test')`)
		return err
	}
	require.NoError(t, insertLote())
	err = insertLote()
	require.Error(t, err, "un segundo lote ABIERTO del mismo sector debe violar uq_lotes_abierto_por_sector")
	require.Contains(t, err.Error(), "uq_lotes_abierto_por_sector")
}

// TestSchema_EventosLoteEventoIDUnico verifica que evento_id sea la clave de
// deduplicación global de eventos_lote.
func TestSchema_EventosLoteEventoIDUnico(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	_, err := p.Exec(ctx, `INSERT INTO sectores(id,nombre) VALUES('sector-ev','Sector Ev')`)
	require.NoError(t, err)

	var loteID string
	require.NoError(t, p.QueryRow(ctx, `
		INSERT INTO lotes_productivos (producto_id, inicio_at, estado, sector_id)
		VALUES ('a1b2c3d4-5678-90ab-cdef-1234567890ab', now(), 'ABIERTO', 'sector-ev')
		RETURNING id`).Scan(&loteID))

	const eventoID = "11111111-1111-1111-1111-111111111111"
	_, err = p.Exec(ctx, `
		INSERT INTO eventos_lote (lote_id, evento_id, producto_id, estado)
		VALUES ($1, $2, 'a1b2c3d4-5678-90ab-cdef-1234567890ab', 'ok')`, loteID, eventoID)
	require.NoError(t, err)

	_, err = p.Exec(ctx, `
		INSERT INTO eventos_lote (lote_id, evento_id, producto_id, estado)
		VALUES ($1, $2, 'a1b2c3d4-5678-90ab-cdef-1234567890ab', 'quemado')`, loteID, eventoID)
	require.Error(t, err, "un evento_id duplicado debe ser rechazado")
	require.Contains(t, err.Error(), "eventos_lote_evento_id_key")
}

// TestSchema_UnDispositivoPorSectorYTipo verifica que un sector admita a lo
// sumo un dispositivo de cada tipo funcional. Los dispositivos legados sin
// sector/tipo (NULL) quedan fuera del índice parcial.
func TestSchema_UnDispositivoPorSectorYTipo(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	_, err := p.Exec(ctx, `INSERT INTO sectores(id,nombre) VALUES('sector-dt','Sector DT')`)
	require.NoError(t, err)

	_, err = p.Exec(ctx, `
		INSERT INTO dispositivos (id, nombre, tipo, sector_id)
		VALUES ('11111111-1111-1111-1111-111111111111', 'pi-salida-1', 'SALIDA_HORNO', 'sector-dt')`)
	require.NoError(t, err)

	_, err = p.Exec(ctx, `
		INSERT INTO dispositivos (id, nombre, tipo, sector_id)
		VALUES ('22222222-2222-2222-2222-222222222222', 'pi-salida-2', 'SALIDA_HORNO', 'sector-dt')`)
	require.Error(t, err, "un segundo SALIDA_HORNO del mismo sector debe violar uq_dispositivos_sector_tipo")
	require.Contains(t, err.Error(), "uq_dispositivos_sector_tipo")

	// Dispositivos legados sin sector no colisionan entre sí (índice parcial).
	_, err = p.Exec(ctx, `INSERT INTO dispositivos (nombre, tipo) VALUES ('legacy-1', 'SALIDA_HORNO')`)
	require.NoError(t, err)
	_, err = p.Exec(ctx, `INSERT INTO dispositivos (nombre, tipo) VALUES ('legacy-2', 'SALIDA_HORNO')`)
	require.NoError(t, err)
}

// TestApply_MigraUbicacionASectores verifica la migración idempotente de la
// columna legacy dispositivos.ubicacion: crea un sector por ubicación, asigna a
// los dispositivos sin sector y elimina la columna. Si dos dispositivos del
// mismo tipo comparten ubicación, solo el primero queda asignado.
func TestApply_MigraUbicacionASectores(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	// Simula una base legada: re-agrega la columna y carga dispositivos.
	_, err := p.Exec(ctx, `ALTER TABLE dispositivos ADD COLUMN ubicacion VARCHAR(100)`)
	require.NoError(t, err)

	const (
		salida1 = "11111111-1111-1111-1111-111111111111"
		salida2 = "22222222-2222-2222-2222-222222222222"
		entrada = "33333333-3333-3333-3333-333333333333"
	)
	_, err = p.Exec(ctx, `
		INSERT INTO dispositivos (id, nombre, tipo, ubicacion, created_at) VALUES
			($1, 'pi-salida-1', 'SALIDA_HORNO', 'Línea A', '2020-01-01T00:00:00Z'),
			($2, 'pi-salida-2', 'SALIDA_HORNO', 'Línea A', '2021-01-01T00:00:00Z'),
			($3, 'pi-entrada',  'ENTRADA_HORNO', 'Línea A', '2022-01-01T00:00:00Z')`,
		salida1, salida2, entrada)
	require.NoError(t, err)

	require.NoError(t, Apply(ctx, p))

	// (a) se creó el sector con slug sin acentos.
	var nombre string
	require.NoError(t, p.QueryRow(ctx, `SELECT nombre FROM sectores WHERE id='linea-a'`).Scan(&nombre))
	require.Equal(t, "Línea A", nombre)

	// (b) el primero por created_at queda asignado; el segundo del mismo tipo no.
	var sector1, sector2, sector3 *string
	require.NoError(t, p.QueryRow(ctx, `SELECT sector_id FROM dispositivos WHERE id=$1`, salida1).Scan(&sector1))
	require.NoError(t, p.QueryRow(ctx, `SELECT sector_id FROM dispositivos WHERE id=$1`, salida2).Scan(&sector2))
	require.NoError(t, p.QueryRow(ctx, `SELECT sector_id FROM dispositivos WHERE id=$1`, entrada).Scan(&sector3))
	require.NotNil(t, sector1)
	require.Equal(t, "linea-a", *sector1)
	require.Nil(t, sector2, "el segundo dispositivo del mismo tipo no debe asignarse")
	require.NotNil(t, sector3)
	require.Equal(t, "linea-a", *sector3)

	// (c) la columna legacy fue eliminada.
	var exists bool
	require.NoError(t, p.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='dispositivos' AND column_name='ubicacion')`).Scan(&exists))
	require.False(t, exists, "la columna ubicacion debería haber sido eliminada")

	// Idempotencia: una segunda aplicación no debe fallar.
	require.NoError(t, Apply(ctx, p))
}

// TestApply_MigraUbicacionSinTipoAsigna verifica que los dispositivos sin tipo
// (fuera del índice parcial) también se asignen al sector migrado.
func TestApply_MigraUbicacionSinTipoAsigna(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	_, err := p.Exec(ctx, `ALTER TABLE dispositivos ADD COLUMN ubicacion VARCHAR(100)`)
	require.NoError(t, err)
	const devID = "44444444-4444-4444-4444-444444444444"
	_, err = p.Exec(ctx, `INSERT INTO dispositivos (id, nombre, ubicacion) VALUES ($1, 'pi-sin-tipo', 'Planta Baja')`, devID)
	require.NoError(t, err)

	require.NoError(t, Apply(ctx, p))

	var sectorID *string
	require.NoError(t, p.QueryRow(ctx, `SELECT sector_id FROM dispositivos WHERE id=$1`, devID).Scan(&sectorID))
	require.NotNil(t, sectorID)
	require.Equal(t, "planta-baja", *sectorID)
}

// TestApply_MigraUbicacionNoRompeConOcupante verifica que la migración no viole
// uq_dispositivos_sector_tipo cuando (sector, tipo) ya está ocupado por un
// dispositivo pre-asignado cuya ubicación es NULL. El candidato debe quedar en
// NULL y Apply no debe fallar (regresión C1).
func TestApply_MigraUbicacionNoRompeConOcupante(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	_, err := p.Exec(ctx, `ALTER TABLE dispositivos ADD COLUMN ubicacion VARCHAR(100)`)
	require.NoError(t, err)

	_, err = p.Exec(ctx, `INSERT INTO sectores(id,nombre) VALUES('linea-a','Línea A')`)
	require.NoError(t, err)

	const (
		ocupante  = "11111111-1111-1111-1111-111111111111"
		candidato = "22222222-2222-2222-2222-222222222222"
	)
	// Ocupante pre-asignado con ubicación NULL; el candidato compite por el mismo
	// (sector, tipo) al migrar su ubicación.
	_, err = p.Exec(ctx, `
		INSERT INTO dispositivos (id, nombre, tipo, sector_id) VALUES
			($1, 'pi-entrada-ocupante', 'ENTRADA_HORNO', 'linea-a'),
			($2, 'pi-entrada-candidato', 'ENTRADA_HORNO', NULL)`, ocupante, candidato)
	require.NoError(t, err)
	_, err = p.Exec(ctx, `UPDATE dispositivos SET ubicacion='Línea A' WHERE id=$1`, candidato)
	require.NoError(t, err)

	require.NoError(t, Apply(ctx, p), "Apply no debe fallar si (sector, tipo) ya está ocupado")

	var ocupanteSector, candidatoSector *string
	require.NoError(t, p.QueryRow(ctx, `SELECT sector_id FROM dispositivos WHERE id=$1`, ocupante).Scan(&ocupanteSector))
	require.NoError(t, p.QueryRow(ctx, `SELECT sector_id FROM dispositivos WHERE id=$1`, candidato).Scan(&candidatoSector))
	require.NotNil(t, ocupanteSector)
	require.Equal(t, "linea-a", *ocupanteSector)
	require.Nil(t, candidatoSector, "el candidato no debe asignarse si (sector, tipo) ya está ocupado")
}

// TestApply_MigraUbicacionOcupanteOtroSector cubre la variante en que el
// ocupante tiene una ubicación que sluggea a OTRO sector (queda excluido del
// ranking) y aun así debe bloquear al candidato (regresión C1).
func TestApply_MigraUbicacionOcupanteOtroSector(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	_, err := p.Exec(ctx, `ALTER TABLE dispositivos ADD COLUMN ubicacion VARCHAR(100)`)
	require.NoError(t, err)

	_, err = p.Exec(ctx, `INSERT INTO sectores(id,nombre) VALUES('linea-a','Línea A')`)
	require.NoError(t, err)

	const (
		ocupante  = "33333333-3333-3333-3333-333333333333"
		candidato = "44444444-4444-4444-4444-444444444444"
	)
	_, err = p.Exec(ctx, `
		INSERT INTO dispositivos (id, nombre, tipo, sector_id, ubicacion) VALUES
			($1, 'pi-salida-ocupante', 'SALIDA_HORNO', 'linea-a', 'Línea B'),
			($2, 'pi-salida-candidato', 'SALIDA_HORNO', NULL, 'Línea A')`, ocupante, candidato)
	require.NoError(t, err)

	require.NoError(t, Apply(ctx, p), "Apply no debe fallar con ocupante de otro sector")

	var candidatoSector *string
	require.NoError(t, p.QueryRow(ctx, `SELECT sector_id FROM dispositivos WHERE id=$1`, candidato).Scan(&candidatoSector))
	require.Nil(t, candidatoSector, "el ocupante de (linea-a, SALIDA_HORNO) debe bloquear al candidato")

	var ocupanteSector *string
	require.NoError(t, p.QueryRow(ctx, `SELECT sector_id FROM dispositivos WHERE id=$1`, ocupante).Scan(&ocupanteSector))
	require.NotNil(t, ocupanteSector)
	require.Equal(t, "linea-a", *ocupanteSector)
}

func TestSchemaSQL_LockIsBeforeDDL(t *testing.T) {
	s := string(SQL)
	begin := strings.Index(s, "BEGIN;")
	lock := strings.Index(s, "pg_advisory_xact_lock")
	extension := strings.Index(s, "CREATE EXTENSION")
	require.True(t, begin >= 0 && begin < lock && lock < extension)
}

// TestSeed_HashPassword123 verifica que el hash sembrado para los usuarios de
// prueba corresponda realmente a password123 (el literal previo no coincidía).
func TestSeed_HashPassword123(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))
	for _, email := range []string{"admin@fermar.com.ar", "supervisor@fermar.com.ar", "operario@fermar.com.ar"} {
		var hash string
		require.NoError(t, p.QueryRow(ctx, `SELECT password_hash FROM usuarios WHERE email=$1`, email).Scan(&hash))
		require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("password123")),
			"el hash seed de %s no corresponde a password123", email)
	}
}

// TestApply_ReparaHashSeedRoto verifica que Apply repare una base ya sembrada
// con el hash roto histórico.
func TestApply_ReparaHashSeedRoto(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))
	const hashRoto = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	_, err := p.Exec(ctx, `UPDATE usuarios SET password_hash=$1 WHERE email='admin@fermar.com.ar'`, hashRoto)
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, p))
	var hash string
	require.NoError(t, p.QueryRow(ctx, `SELECT password_hash FROM usuarios WHERE email='admin@fermar.com.ar'`).Scan(&hash))
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("password123")))
}

// TestApply_NoPisaPasswordCambiada verifica que la reparación no pise una
// contraseña distinta ya establecida (por ejemplo, por un operario).
func TestApply_NoPisaPasswordCambiada(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))
	propia, err := bcrypt.GenerateFromPassword([]byte("mi-clave-propia"), bcrypt.MinCost)
	require.NoError(t, err)
	_, err = p.Exec(ctx, `UPDATE usuarios SET password_hash=$1 WHERE email='admin@fermar.com.ar'`, string(propia))
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, p))
	var hash string
	require.NoError(t, p.QueryRow(ctx, `SELECT password_hash FROM usuarios WHERE email='admin@fermar.com.ar'`).Scan(&hash))
	require.Equal(t, string(propia), hash, "Apply no debe pisar una contraseña ya cambiada")
}
