package repository

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote"
)

// testUUID generates a random UUID v4 string for test isolation.
func testUUID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:])
}

// setupTestPool creates a pgxpool connected to the test database.
// It uses TEST_DATABASE_URL (never the production .env) so that
// running `go test ./...` never touches the Aiven production instance.
// If TEST_DATABASE_URL is not set the test is skipped cleanly.
func setupTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	connStr := os.Getenv("TEST_DATABASE_URL")
	if connStr == "" {
		t.Skip("TEST_DATABASE_URL no configurada — saltando test de integración (no tocar producción)")
	}

	pool, err := pgxpool.New(context.Background(), connStr)
	require.NoError(t, err, "fallo al crear pool de conexión a PostgreSQL de test")

	t.Cleanup(pool.Close)
	return pool
}

// verifyLoteInDB reads back a Lote from the database and asserts it matches.
func verifyLoteInDB(t *testing.T, pool *pgxpool.Pool, expected lote.Lote) {
	t.Helper()

	var retrieved lote.Lote
	query := `SELECT id, producto_id, turno, inicio_at, fin_at,
		total_unidades, correctos, quemados, crudas,
		correctos_kg, quemados_kg, crudos_kg,
		temp_horno_1, temp_comb_horno_1,
		temp_horno_2, temp_comb_horno_2,
		velocidad_cinta,
		created_at, updated_at
		FROM lotes_productivos WHERE id = $1`

	err := pool.QueryRow(context.Background(), query, expected.ID).Scan(
		&retrieved.ID,
		&retrieved.ProductoID,
		&retrieved.Turno,
		&retrieved.InicioAt,
		&retrieved.FinAt,
		&retrieved.TotalUnidades,
		&retrieved.Correctos,
		&retrieved.Quemados,
		&retrieved.Crudas,
		&retrieved.CorrectosKg,
		&retrieved.QuemadosKg,
		&retrieved.CrudosKg,
		&retrieved.TempHorno1,
		&retrieved.TempCombHorno1,
		&retrieved.TempHorno2,
		&retrieved.TempCombHorno2,
		&retrieved.VelocidadCinta,
		&retrieved.CreatedAt,
		&retrieved.UpdatedAt,
	)
	require.NoError(t, err, "fallo al leer lote insertado")

	assert.Equal(t, expected.ID, retrieved.ID)
	assert.Equal(t, expected.ProductoID, retrieved.ProductoID)
	assert.Equal(t, expected.Turno, retrieved.Turno)
	assert.WithinDuration(t, expected.InicioAt, retrieved.InicioAt, time.Second)
	assert.WithinDuration(t, expected.FinAt, retrieved.FinAt, time.Second)
	assert.Equal(t, expected.TotalUnidades, retrieved.TotalUnidades)
	assert.Equal(t, expected.Correctos, retrieved.Correctos)
	assert.Equal(t, expected.Quemados, retrieved.Quemados)
	assert.Equal(t, expected.Crudas, retrieved.Crudas)
	assert.Equal(t, expected.CorrectosKg, retrieved.CorrectosKg)
	assert.Equal(t, expected.QuemadosKg, retrieved.QuemadosKg)
	assert.Equal(t, expected.CrudosKg, retrieved.CrudosKg)

	if expected.TempHorno1 == nil {
		assert.Nil(t, retrieved.TempHorno1)
	} else {
		assert.Equal(t, *expected.TempHorno1, *retrieved.TempHorno1)
	}

	if expected.TempCombHorno1 == nil {
		assert.Nil(t, retrieved.TempCombHorno1)
	} else {
		assert.Equal(t, *expected.TempCombHorno1, *retrieved.TempCombHorno1)
	}

	if expected.TempHorno2 == nil {
		assert.Nil(t, retrieved.TempHorno2)
	} else {
		assert.Equal(t, *expected.TempHorno2, *retrieved.TempHorno2)
	}

	if expected.TempCombHorno2 == nil {
		assert.Nil(t, retrieved.TempCombHorno2)
	} else {
		assert.Equal(t, *expected.TempCombHorno2, *retrieved.TempCombHorno2)
	}

	if expected.VelocidadCinta == nil {
		assert.Nil(t, retrieved.VelocidadCinta)
	} else {
		assert.Equal(t, *expected.VelocidadCinta, *retrieved.VelocidadCinta)
	}

	assert.WithinDuration(t, expected.CreatedAt, retrieved.CreatedAt, time.Second)
	assert.WithinDuration(t, expected.UpdatedAt, retrieved.UpdatedAt, time.Second)
}

// cleanupLote deletes the inserted row so tests don't pollute the database.
func cleanupLote(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), "DELETE FROM lotes_productivos WHERE id = $1", id)
	if err != nil {
		t.Logf("cleanup: no se pudo borrar lote %s: %v", id, err)
	}
}

func ptr[T any](v T) *T { return &v }

func buildTestLote() lote.Lote {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return lote.Lote{
		ID:             testUUID(),
		ProductoID:     "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		Turno:          "mañana",
		InicioAt:       now.Add(-3 * time.Hour),
		FinAt:          now,
		TotalUnidades:  1200,
		Correctos:      1150,
		Quemados:       50,
		Crudas:         nil,
		CorrectosKg:    138.00,
		QuemadosKg:     6.00,
		CrudosKg:       nil,
		TempHorno1:     ptr(210.50),
		TempCombHorno1: nil,
		TempHorno2:     ptr(215.00),
		TempCombHorno2: nil,
		VelocidadCinta: ptr(3.20),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestCreateLote_Success(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewPostgresRepository(pool)

	l := buildTestLote()
	err := repo.Create(context.Background(), &l)
	require.NoError(t, err, "Create debería insertar sin errores")

	t.Cleanup(func() { cleanupLote(t, pool, l.ID) })

	verifyLoteInDB(t, pool, l)
}

func TestCreateLote_EmptyID_UsesDefault(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewPostgresRepository(pool)

	l := buildTestLote()
	l.ID = "" // Forzar generación por PostgreSQL
	err := repo.Create(context.Background(), &l)
	require.NoError(t, err, "Create con ID vacío debería generar UUID automáticamente")

	// El ID debe haber sido seteado por gen_random_uuid()
	assert.NotEmpty(t, l.ID, "El ID no debería quedar vacío después del INSERT")
	t.Logf("UUID generado por PostgreSQL: %s", l.ID)

	t.Cleanup(func() { cleanupLote(t, pool, l.ID) })

	verifyLoteInDB(t, pool, l)
}

func TestCreateLote_ConstraintViolation_NegativeUnits(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewPostgresRepository(pool)

	l := buildTestLote()
	l.TotalUnidades = -5
	l.Correctos = 0
	l.Quemados = 0

	err := repo.Create(context.Background(), &l)
	assert.Error(t, err, "Debería fallar por total_unidades negativo")
	assert.Contains(t, err.Error(), "failed to insert lote")
}

func TestCreateLote_InvalidForeignKey(t *testing.T) {
	pool := setupTestPool(t)
	repo := NewPostgresRepository(pool)

	l := buildTestLote()
	l.ProductoID = "00000000-0000-0000-0000-000000000000"

	err := repo.Create(context.Background(), &l)
	assert.Error(t, err, "Debería fallar por producto_id inexistente")
	assert.Contains(t, err.Error(), "failed to insert lote")

	// No hay cleanup porque el INSERT nunca ocurrió
}
