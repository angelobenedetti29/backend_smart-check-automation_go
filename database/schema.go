// Package schema embebe el DDL idempotente de la base de datos y lo aplica
// al arrancar el backend, de modo que los cambios de esquema se propaguen a
// bases existentes sin recrear volúmenes ni ejecutar ALTERs a mano.
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
// idempotente (CREATE IF NOT EXISTS, ADD COLUMN IF NOT EXISTS, seeds con
// ON CONFLICT DO NOTHING, constraints con guards), por lo que es seguro
// ejecutarlo en cada arranque del servidor.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, string(SQL)); err != nil {
		return fmt.Errorf("schema apply failed: %w", err)
	}
	return nil
}
