package database

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPostgresPool creates a connection pool to PostgreSQL, verifies connectivity with a Ping,
// and fatally exits if the database is unreachable — preventing the server from starting with a broken connection.
func NewPostgresPool(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		log.Fatalf("No se puede conectar a PostgreSQL: ping falló — %v", err)
	}

	log.Println("Pool de conexiones a PostgreSQL inicializado y verificado exitosamente")
	return pool, nil
}
