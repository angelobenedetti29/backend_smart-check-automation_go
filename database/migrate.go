package schema

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const (
	migrationsDir      = "migrations"
	migrationsLockName = "smart-check:migrations"
)

// Migrate aplica en orden las migraciones pendientes de database/migrations y
// registra cada una en schema_migrations. Usa un advisory lock de sesión para
// serializar instancias concurrentes. Cada archivo se ejecuta en su propia
// transacción: si falla, se revierte y se devuelve el nombre del archivo.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("adquirir conexión para migraciones: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, migrationsLockName); err != nil {
		return fmt.Errorf("adquirir lock de migraciones: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, migrationsLockName)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("crear schema_migrations: %w", err)
	}

	versions, err := migrationVersions()
	if err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, conn)
	if err != nil {
		return err
	}

	for _, version := range versions {
		if applied[version] {
			continue
		}
		if err := applyMigration(ctx, conn, version); err != nil {
			return err
		}
	}
	return nil
}

// migrationVersions devuelve los archivos .sql de migrations/ ordenados
// lexicográficamente (el prefijo numérico define el orden).
func migrationVersions() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("leer %s: %w", migrationsDir, err)
	}

	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versions = append(versions, entry.Name())
	}
	sort.Strings(versions)
	return versions, nil
}

func appliedVersions(ctx context.Context, conn *pgxpool.Conn) (map[string]bool, error) {
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("consultar schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("leer schema_migrations: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterar schema_migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, conn *pgxpool.Conn, version string) error {
	body, err := migrationsFS.ReadFile(migrationsDir + "/" + version)
	if err != nil {
		return fmt.Errorf("leer migración %s: %w", version, err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("iniciar migración %s: %w", version, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Otra instancia pudo aplicarla mientras esperábamos el lock.
	var already bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&already); err != nil {
		return fmt.Errorf("verificar migración %s: %w", version, err)
	}
	if already {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, string(body)); err != nil {
		return fmt.Errorf("aplicar migración %s: %w", version, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("registrar migración %s: %w", version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("confirmar migración %s: %w", version, err)
	}
	return nil
}
