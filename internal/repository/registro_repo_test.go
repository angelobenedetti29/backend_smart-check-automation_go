package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/registro"
)

// setupRegistrationRepo provisiona una base aislada y un usuario Supervisor
// para poder resolver al actor de aprobación/rechazo.
func setupRegistrationRepo(t *testing.T) (*RegistrationRepository, *pgxpool.Pool) {
	t.Helper()
	pool := repositoryTestPool(t)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO usuarios(email,nombre,rol) VALUES('sup@example.com','Sup','Supervisor') ON CONFLICT (email) DO NOTHING`)
	require.NoError(t, err)
	return NewRegistrationRepository(pool), pool
}

// fakeRepoID construye un request_id único y válido para longitud de columna.
func fakeRepoID(seed string) string {
	return "req_" + seed + strings.Repeat("0", 43-len(seed))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestRegistrationRepository_RegistroFlow(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupRegistrationRepo(t)

	t.Run("issue persiste tipo y hostname duplicado devuelve el mismo request_id", func(t *testing.T) {
		id := fakeRepoID("a")
		res, err := repo.Issue(ctx, "host-a", "ENTRADA_HORNO", id, time.Now().Add(15*time.Minute))
		require.NoError(t, err)
		require.Equal(t, id, res.RequestID)
		require.Equal(t, registro.StatusPending, res.Status)

		var tipo *string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT tipo FROM registration_requests WHERE request_id=$1`, id).Scan(&tipo))
		require.NotNil(t, tipo)
		require.Equal(t, "ENTRADA_HORNO", *tipo)

		res2, err := repo.Issue(ctx, "host-a", "ENTRADA_HORNO", fakeRepoID("b"), time.Now().Add(15*time.Minute))
		require.NoError(t, err)
		require.Equal(t, id, res2.RequestID, "no debe crear una segunda PENDING para el mismo hostname")
	})

	t.Run("issue idempotente actualiza el tipo de la PENDING (último gana)", func(t *testing.T) {
		id := fakeRepoID("a2")
		_, err := repo.Issue(ctx, "host-a2", "ENTRADA_HORNO", id, time.Now().Add(15*time.Minute))
		require.NoError(t, err)

		res, err := repo.Issue(ctx, "host-a2", "SALIDA_HORNO", fakeRepoID("b2"), time.Now().Add(15*time.Minute))
		require.NoError(t, err)
		require.Equal(t, id, res.RequestID, "debe conservar el request_id original")

		var tipo string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT tipo FROM registration_requests WHERE request_id=$1`, id).Scan(&tipo))
		require.Equal(t, "SALIDA_HORNO", tipo, "el último tipo solicitado debe ganar")
	})

	t.Run("approve crea dispositivo activo con tipo y pickup entrega el secret una vez", func(t *testing.T) {
		id := fakeRepoID("c")
		secret := "secreto-de-alta"
		_, err := repo.Issue(ctx, "host-c", "SALIDA_HORNO", id, time.Now().Add(15*time.Minute))
		require.NoError(t, err)

		ap, err := repo.Approve(ctx, "sup@example.com", id, secret, sha256Hex(secret), time.Now().Add(10*time.Minute))
		require.NoError(t, err)
		require.Equal(t, registro.StatusApproved, ap.Status)
		require.Equal(t, id, ap.RequestID)
		require.NotEmpty(t, ap.DeviceID)

		var authStatus string
		var secretHash string
		var deviceTipo *string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT auth_status, secret_hash, tipo FROM dispositivos WHERE id=$1`, ap.DeviceID).Scan(&authStatus, &secretHash, &deviceTipo))
		require.Equal(t, "active", authStatus)
		require.Len(t, secretHash, 64)
		require.NotNil(t, deviceTipo)
		require.Equal(t, "SALIDA_HORNO", *deviceTipo, "el dispositivo hereda el tipo de la solicitud")

		var reqStatus string
		var deviceID, storedSecret *string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT status, device_id, secret FROM registration_requests WHERE request_id=$1`, id).Scan(&reqStatus, &deviceID, &storedSecret))
		require.Equal(t, registro.StatusApproved, reqStatus)
		require.NotNil(t, deviceID)
		require.NotNil(t, storedSecret)

		// Primer pickup: entrega el secret y el tipo.
		p, err := repo.Pickup(ctx, id)
		require.NoError(t, err)
		require.Equal(t, registro.StatusApproved, p.Status)
		require.NotNil(t, p.Secret)
		require.Equal(t, secret, *p.Secret)
		require.NotNil(t, p.DeviceID)
		require.Equal(t, ap.DeviceID, *p.DeviceID)
		require.NotNil(t, p.Type)
		require.Equal(t, "SALIDA_HORNO", *p.Type)

		// Segundo pickup: ya no lo entrega, pero conserva device_id y tipo.
		p2, err := repo.Pickup(ctx, id)
		require.NoError(t, err)
		require.Equal(t, registro.StatusApproved, p2.Status)
		require.Nil(t, p2.Secret)
		require.NotNil(t, p2.DeviceID)
		require.NotNil(t, p2.Type)
		require.Equal(t, "SALIDA_HORNO", *p2.Type)
	})

	t.Run("múltiples dispositivos del mismo tipo están permitidos", func(t *testing.T) {
		const tipo = "ENTRADA_HORNO"
		for _, host := range []string{"host-m1", "host-m2"} {
			id := fakeRepoID(strings.ReplaceAll(host, "-", ""))
			_, err := repo.Issue(ctx, host, tipo, id, time.Now().Add(15*time.Minute))
			require.NoError(t, err)
			secret := "s-" + host
			_, err = repo.Approve(ctx, "sup@example.com", id, secret, sha256Hex(secret), time.Now().Add(10*time.Minute))
			require.NoError(t, err)
		}

		var count int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT count(*) FROM dispositivos WHERE tipo=$1`, tipo).Scan(&count))
		require.GreaterOrEqual(t, count, 2, "no hay unicidad por tipo")
	})

	t.Run("tipo inmutable tras Update", func(t *testing.T) {
		repoDisp := NewPostgresDispositivoRepository(pool)
		id := fakeRepoID("inv")
		_, err := repo.Issue(ctx, "host-inv", "ENTRADA_HORNO", id, time.Now().Add(15*time.Minute))
		require.NoError(t, err)
		secret := "s-inv"
		ap, err := repo.Approve(ctx, "sup@example.com", id, secret, sha256Hex(secret), time.Now().Add(10*time.Minute))
		require.NoError(t, err)

		d, err := repoDisp.GetDispositivoByID(ctx, ap.DeviceID)
		require.NoError(t, err)
		require.NotNil(t, d.Tipo)
		require.Equal(t, "ENTRADA_HORNO", *d.Tipo)

		d.Nombre = "host-inv-renombrado"
		require.NoError(t, repoDisp.Update(ctx, d))

		var tipo *string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT tipo FROM dispositivos WHERE id=$1`, ap.DeviceID).Scan(&tipo))
		require.NotNil(t, tipo)
		require.Equal(t, "ENTRADA_HORNO", *tipo, "Update no debe modificar el tipo")
	})

	t.Run("list pending expone tipo", func(t *testing.T) {
		id := fakeRepoID("lt")
		_, err := repo.Issue(ctx, "host-lt", "SALIDA_HORNO", id, time.Now().Add(15*time.Minute))
		require.NoError(t, err)

		list, err := repo.ListPending(ctx)
		require.NoError(t, err)
		var found *registro.RegistrationRequest
		for i := range list {
			if list[i].RequestID == id {
				found = &list[i]
			}
		}
		require.NotNil(t, found)
		require.NotNil(t, found.Type)
		require.Equal(t, "SALIDA_HORNO", *found.Type)
	})

	t.Run("reject marca REJECTED y no admite segunda resolución", func(t *testing.T) {
		id := fakeRepoID("d")
		_, err := repo.Issue(ctx, "host-d", "ENTRADA_HORNO", id, time.Now().Add(15*time.Minute))
		require.NoError(t, err)
		require.NoError(t, repo.Reject(ctx, "sup@example.com", id))

		var status string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT status FROM registration_requests WHERE request_id=$1`, id).Scan(&status))
		require.Equal(t, registro.StatusRejected, status)

		require.ErrorIs(t, repo.Reject(ctx, "sup@example.com", id), registro.ErrRequestNotPending)
	})

	t.Run("approve de solicitud vencida devuelve ErrRequestExpired", func(t *testing.T) {
		id := fakeRepoID("e")
		_, err := repo.Issue(ctx, "host-e", "ENTRADA_HORNO", id, time.Now().Add(-time.Minute))
		require.NoError(t, err)

		_, err = repo.Approve(ctx, "sup@example.com", id, "s", sha256Hex("s"), time.Now().Add(10*time.Minute))
		require.ErrorIs(t, err, registro.ErrRequestExpired)

		var status string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT status FROM registration_requests WHERE request_id=$1`, id).Scan(&status))
		require.Equal(t, registro.StatusExpired, status)
	})

	t.Run("list pending y request inexistente", func(t *testing.T) {
		id := fakeRepoID("f")
		_, err := repo.Issue(ctx, "host-f", "ENTRADA_HORNO", id, time.Now().Add(15*time.Minute))
		require.NoError(t, err)

		list, err := repo.ListPending(ctx)
		require.NoError(t, err)
		require.NotNil(t, list)

		found := false
		for _, item := range list {
			if item.RequestID == id {
				found = true
				require.Equal(t, "host-f", item.Hostname)
				require.Equal(t, registro.StatusPending, item.Status)
			}
		}
		require.True(t, found)

		_, err = repo.Approve(ctx, "sup@example.com", fakeRepoID("g"), "s", sha256Hex("s"), time.Now().Add(10*time.Minute))
		require.ErrorIs(t, err, registro.ErrRequestNotFound)

		_, err = repo.Pickup(ctx, fakeRepoID("h"))
		require.ErrorIs(t, err, registro.ErrRequestNotFound)
	})
}

// TestDispositivoRepository_CreatePersisteTipo verifica que el alta manual
// (panel) persista el tipo y que GetDispositivoByID lo devuelva.
func TestDispositivoRepository_CreatePersisteTipo(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)
	repo := NewPostgresDispositivoRepository(pool)

	tipo := "ENTRADA_HORNO"
	d := dispositivo.Dispositivo{Nombre: "Pi Entrada", Ubicacion: "Línea A", Tipo: &tipo}
	require.NoError(t, repo.Create(ctx, &d))

	got, err := repo.GetDispositivoByID(ctx, d.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Tipo)
	require.Equal(t, "ENTRADA_HORNO", *got.Tipo)

	reads, err := repo.ListDeviceReads(ctx)
	require.NoError(t, err)
	require.Len(t, reads, 1)
	require.NotNil(t, reads[0].Tipo)
	require.Equal(t, "ENTRADA_HORNO", *reads[0].Tipo)
}
