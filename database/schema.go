// Package schema embebe el esquema canónico de instalación limpia y lo aplica
// al arrancar el backend. Solo crea objetos faltantes: no actualiza
// definiciones de tablas existentes.
package schema

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var SQL []byte

// Apply ejecuta el schema completo contra la base de datos. schema.sql es
// idempotente (CREATE TABLE/INDEX IF NOT EXISTS y seeds con ON CONFLICT DO
// NOTHING), por lo que es seguro ejecutarlo en cada arranque del servidor.
// No migra bases existentes: un cambio estructural requiere recrear la base o
// aplicar una migración manual.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, string(SQL)); err != nil {
		return fmt.Errorf("schema apply failed: %w", err)
	}
	return nil
}
