// Package schema embebe el esquema canónico de instalación limpia y las
// migraciones incrementales, y los aplica al arrancar el backend.
package schema

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var SQL []byte

// Apply ejecuta schema.sql (instalación limpia e idempotente) y luego aplica
// las migraciones pendientes de database/migrations. Es seguro ejecutarlo en
// cada arranque: schema.sql sólo crea objetos faltantes y Migrate registra las
// versiones ya aplicadas en schema_migrations.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, string(SQL)); err != nil {
		return fmt.Errorf("schema apply failed: %w", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		return fmt.Errorf("schema migrate failed: %w", err)
	}
	return nil
}
