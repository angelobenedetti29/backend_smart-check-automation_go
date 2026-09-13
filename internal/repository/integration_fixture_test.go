package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	schema "github.com/angelobenedetti29/smart-check-automation/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// repositoryTestPool provisions one database per test and applies the real
// schema. TEST_DATABASE_URL must point at an isolated PostgreSQL server.
func repositoryTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL no configurada — se requiere PostgreSQL aislado")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	require.NoError(t, err)
	name := fmt.Sprintf("sca_repo_test_%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, `CREATE DATABASE "`+name+`"`)
	require.NoError(t, err)
	require.NoError(t, admin.Close(ctx))
	cfg, err := pgxpool.ParseConfig(base)
	require.NoError(t, err)
	cfg.ConnConfig.Database = name
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	require.NoError(t, p.Ping(ctx))
	require.NoError(t, schema.Apply(ctx, p))
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
