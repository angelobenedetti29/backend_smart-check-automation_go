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
		{"productos", "capacidad_nominal_kg_h"},
		{"dispositivos", "secret_hash"},
		{"dispositivos", "tipo"},
		{"dispositivos", "rename_url"},
		{"dispositivos", "rename_token"},
		{"lotes_productivos", "dispositivo_id"},
		{"historial_consignas", "dispositivo_id"},
		{"historial_consignas", "decision_id"},
		{"historial_consignas", "estado"},
		{"registration_requests", "request_id"},
		{"registration_requests", "secret"},
		{"registration_requests", "tipo"},
		{"registration_requests", "rename_url"},
	} {
		var exists bool
		err := p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`, tc.table, tc.column).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "%s.%s no fue creada", tc.table, tc.column)
	}
}

// TestApply_EliminaColumnasListenLegacy verifica que el refactor de rename_url
// haya eliminado el anuncio previo por par (listen_address/listen_port) en ambas
// tablas, dejando solo rename_url. Las bases de test se crean aisladas y frescas,
// así que la ausencia es determinista.
func TestApply_EliminaColumnasListenLegacy(t *testing.T) {
	p := isolatedPool(t)
	require.NoError(t, Apply(context.Background(), p))
	for _, tc := range []struct{ table, column string }{
		{"dispositivos", "listen_address"},
		{"dispositivos", "listen_port"},
		{"registration_requests", "listen_address"},
		{"registration_requests", "listen_port"},
	} {
		var exists bool
		err := p.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`, tc.table, tc.column).Scan(&exists)
		require.NoError(t, err)
		require.False(t, exists, "%s.%s no debería existir tras el refactor", tc.table, tc.column)
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

// TestApply_CreaEsquemaSectorLotes verifica las columnas nuevas del contrato
// legacy de sectores: baja lógica de productos, sectores, sector de dispositivos y el
// ciclo ABIERTO/CERRADO de lotes_productivos más eventos_lote.
func TestApply_CreaEsquemaSectorLotes(t *testing.T) {
	p := isolatedPool(t)
	require.NoError(t, Apply(context.Background(), p))
	for _, tc := range []struct{ table, column string }{
		{"productos", "activo"},
		{"productos", "peso_referencia_kg"},
		{"productos", "tolerancia_peso_pct"},
		{"productos", "dimension_base_cm"},
		{"productos", "tolerancia_dimension_cm"},
		{"productos", "temp_min"},
		{"productos", "temp_max"},
		{"productos", "velocidad_cinta_min"},
		{"productos", "velocidad_cinta_max"},
		{"productos", "temp_setpoint"},
		{"productos", "velocidad_cinta_setpoint"},
		{"productos", "updated_at"},
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

// TestApply_ParametrosControlNotNull verifica que los 8 parámetros de control
// hayan quedado NOT NULL y que los setpoints sigan siendo nullable.
func TestApply_ParametrosControlNotNull(t *testing.T) {
	p := isolatedPool(t)
	require.NoError(t, Apply(context.Background(), p))

	for _, column := range []string{
		"peso_referencia_kg", "tolerancia_peso_pct",
		"dimension_base_cm", "tolerancia_dimension_cm",
		"temp_min", "temp_max",
		"velocidad_cinta_min", "velocidad_cinta_max",
	} {
		var nullable string
		err := p.QueryRow(context.Background(), `SELECT is_nullable FROM information_schema.columns WHERE table_name='productos' AND column_name=$1`, column).Scan(&nullable)
		require.NoError(t, err)
		require.Equal(t, "NO", nullable, "%s debería ser NOT NULL", column)
	}

	for _, column := range []string{"temp_setpoint", "velocidad_cinta_setpoint"} {
		var nullable string
		err := p.QueryRow(context.Background(), `SELECT is_nullable FROM information_schema.columns WHERE table_name='productos' AND column_name=$1`, column).Scan(&nullable)
		require.NoError(t, err)
		require.Equal(t, "YES", nullable, "%s debería ser nullable", column)
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
