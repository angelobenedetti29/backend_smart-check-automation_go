package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPostgresPool creates a configured connection pool to PostgreSQL and
// verifies connectivity with a Ping before returning.
// Pool limits are tuned for Aiven Cloud Free Tier (max 5 simultaneous connections).
func NewPostgresPool(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("error parsing database config: %w", err)
	}

	// Connection pool limits — calibrated for Aiven Free Tier
	config.MaxConns = 5
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("error creating postgres pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping failed: %w", err)
	}

	return pool, nil
}

// RunMigrations aplica de forma idempotente las migraciones de esquema requeridas.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	const migrationSQL = `
		ALTER TABLE parametros_producto
			ADD COLUMN IF NOT EXISTS temp_setpoint             NUMERIC(6,2),
			ADD COLUMN IF NOT EXISTS velocidad_cinta_setpoint   NUMERIC(6,2),
			ADD COLUMN IF NOT EXISTS costo_unitario            NUMERIC(12,2);

		ALTER TABLE lotes_productivos
			ADD COLUMN IF NOT EXISTS costo_unitario            NUMERIC(12,2);

		DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_parametros_costo_unitario_no_negativo') THEN
				ALTER TABLE parametros_producto
					ADD CONSTRAINT chk_parametros_costo_unitario_no_negativo
						CHECK (costo_unitario IS NULL OR costo_unitario >= 0);
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_lotes_costo_unitario_no_negativo') THEN
				ALTER TABLE lotes_productivos
					ADD CONSTRAINT chk_lotes_costo_unitario_no_negativo
						CHECK (costo_unitario IS NULL OR costo_unitario >= 0);
			END IF;
		END $$;
	`
	_, err := pool.Exec(ctx, migrationSQL)
	if err != nil {
		return fmt.Errorf("error al ejecutar migraciones de base de datos: %w", err)
	}
	return nil
}
