package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pingMaxRetries y pingRetryDelay controlan la tolerancia a hiccups
// transitorios del pooler (ej. Supabase Supavisor) al verificar conectividad.
const (
	pingMaxRetries = 3
	pingRetryDelay = 2 * time.Second
	pingTimeout    = 15 * time.Second
)

// NewPostgresPool creates a configured connection pool to PostgreSQL and
// verifies connectivity with a Ping before returning.
// Pool limits are tuned for Supabase pooler en plan Free (pool_size=15 compartido).
func NewPostgresPool(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("error parsing database config: %w", err)
	}

	// Connection pool limits — calibrado para el pooler de Supabase (Nano/Free)
	config.MaxConns = 5
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("error creating postgres pool: %w", err)
	}

	var pingErr error
	for attempt := 1; attempt <= pingMaxRetries; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		pingErr = pool.Ping(pingCtx)
		cancel()

		if pingErr == nil {
			return pool, nil
		}

		if attempt < pingMaxRetries {
			time.Sleep(pingRetryDelay)
		}
	}

	pool.Close()
	return nil, fmt.Errorf("postgres ping failed after %d intentos: %w", pingMaxRetries, pingErr)
}
