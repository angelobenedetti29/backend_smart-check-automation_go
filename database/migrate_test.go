package schema

import (
	"context"
	"regexp"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

var migrationNamePattern = regexp.MustCompile(`^\d{4}_[a-z0-9_]+\.sql$`)

func TestMigrationFiles(t *testing.T) {
	versions, err := migrationVersions()
	require.NoError(t, err)
	require.NotEmpty(t, versions)
	require.Equal(t, "0001_baseline.sql", versions[0])

	seen := make(map[string]bool, len(versions))
	for _, version := range versions {
		require.Regexp(t, migrationNamePattern, version)
		require.False(t, seen[version], "migración duplicada: %s", version)
		seen[version] = true
	}
	require.True(t, sort.StringsAreSorted(versions), "las migraciones deben estar ordenadas")
}

func TestMigrate_RegistraVersiones(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	var versions []string
	rows, err := p.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var version string
		require.NoError(t, rows.Scan(&version))
		versions = append(versions, version)
	}
	require.NoError(t, rows.Err())
	require.Contains(t, versions, "0001_baseline.sql")
	require.Contains(t, versions, "0002_legacy_ubicacion.sql")
	require.Contains(t, versions, "0003_notificaciones.sql")
	require.Contains(t, versions, "0004_alertas_calidad.sql")
	require.Contains(t, versions, "0005_alertas_dispositivo.sql")
	require.Contains(t, versions, "0006_notificaciones_html.sql")
}

func TestMigrate_NoReaplica(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	var before string
	require.NoError(t, p.QueryRow(ctx,
		`SELECT applied_at::text FROM schema_migrations WHERE version='0002_legacy_ubicacion.sql'`,
	).Scan(&before))

	require.NoError(t, Migrate(ctx, p))

	var after string
	require.NoError(t, p.QueryRow(ctx,
		`SELECT applied_at::text FROM schema_migrations WHERE version='0002_legacy_ubicacion.sql'`,
	).Scan(&after))
	require.Equal(t, before, after, "una migración ya aplicada no debe volver a ejecutarse")
}

func TestMigrate_LegacyUbicacion(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	// Simular una base vieja: la columna ubicacion todavía existe.
	_, err := p.Exec(ctx, `ALTER TABLE dispositivos ADD COLUMN ubicacion VARCHAR(100)`)
	require.NoError(t, err)
	_, err = p.Exec(ctx,
		`INSERT INTO dispositivos (nombre, tipo, ubicacion) VALUES ('Pi entrada', 'ENTRADA_HORNO', 'Línea A')`,
	)
	require.NoError(t, err)

	// Forzar la re-ejecución de la migración.
	_, err = p.Exec(ctx, `DELETE FROM schema_migrations WHERE version='0002_legacy_ubicacion.sql'`)
	require.NoError(t, err)
	require.NoError(t, Migrate(ctx, p))

	var sectorID string
	require.NoError(t, p.QueryRow(ctx,
		`SELECT sector_id FROM dispositivos WHERE nombre='Pi entrada'`,
	).Scan(&sectorID))
	require.Equal(t, "linea-a", sectorID)

	var nombre string
	require.NoError(t, p.QueryRow(ctx, `SELECT nombre FROM sectores WHERE id='linea-a'`).Scan(&nombre))
	require.Equal(t, "Línea A", nombre)

	var exists bool
	require.NoError(t, p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name='dispositivos' AND column_name='ubicacion'
		)`).Scan(&exists))
	require.False(t, exists, "la columna ubicacion debería haber sido eliminada")
}

func TestMigrate_LegacyUbicacionUnDispositivoPorTipo(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	_, err := p.Exec(ctx, `ALTER TABLE dispositivos ADD COLUMN ubicacion VARCHAR(100)`)
	require.NoError(t, err)
	_, err = p.Exec(ctx, `
		INSERT INTO dispositivos (nombre, tipo, ubicacion) VALUES
			('Pi 1', 'ENTRADA_HORNO', 'Línea A'),
			('Pi 2', 'ENTRADA_HORNO', 'Línea A')`)
	require.NoError(t, err)

	_, err = p.Exec(ctx, `DELETE FROM schema_migrations WHERE version='0002_legacy_ubicacion.sql'`)
	require.NoError(t, err)
	require.NoError(t, Migrate(ctx, p))

	var asignados int
	require.NoError(t, p.QueryRow(ctx,
		`SELECT count(*) FROM dispositivos WHERE sector_id='linea-a'`,
	).Scan(&asignados))
	require.Equal(t, 1, asignados, "sólo un dispositivo por sector/tipo puede quedar asignado")
}

// TestMigrate_AlertasDispositivo verifica la columna y el índice parcial de la
// migración 0005.
func TestMigrate_AlertasDispositivo(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	var existe bool
	require.NoError(t, p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'alertas'
			  AND column_name = 'dispositivo_id'
		)`).Scan(&existe))
	require.True(t, existe, "alertas.dispositivo_id debe existir")

	var indice string
	require.NoError(t, p.QueryRow(ctx, `
		SELECT indexname FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND tablename = 'alertas'
		  AND indexname = 'uq_alertas_activa_dispositivo_tipo'`).Scan(&indice))
	require.Equal(t, "uq_alertas_activa_dispositivo_tipo", indice)
}

// TestMigrate_NotificacionesHTML verifica la columna cuerpo_html de la
// migración 0006.
func TestMigrate_NotificacionesHTML(t *testing.T) {
	p := isolatedPool(t)
	ctx := context.Background()
	require.NoError(t, Apply(ctx, p))

	var existe bool
	require.NoError(t, p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'notificaciones_outbox'
			  AND column_name = 'cuerpo_html'
		)`).Scan(&existe))
	require.True(t, existe, "notificaciones_outbox.cuerpo_html debe existir")
}
