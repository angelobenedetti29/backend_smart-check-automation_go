package repository

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
)

const (
	alertaTestUsuarioID = "11111111-1111-1111-1111-111111111111"
	alertaTestEmail     = "supervisor.alertas@fermar.com.ar"
	alertaTestNombre    = "Supervisor Alertas"
)

// setupAlertaRepo provisiona una base con sector/producto/lote ABIERTO y un
// usuario para el ACK. Devuelve el repo, el pool y el id del lote.
func setupAlertaRepo(t *testing.T) (*AlertaPostgresRepository, *pgxpool.Pool, string) {
	t.Helper()
	loteRepo, pool := setupLoteSectorRepo(t)
	ctx := context.Background()
	lote := abrirLote(t, ctx, loteRepo)

	_, err := pool.Exec(ctx, `
		INSERT INTO usuarios (id, email, nombre, rol) VALUES ($1, $2, $3, 'Supervisor')`,
		alertaTestUsuarioID, alertaTestEmail, alertaTestNombre)
	require.NoError(t, err)

	return NewAlertaPostgresRepository(pool), pool, lote.ID
}

// alertaDePrueba arma una alerta ACTIVA asociada al lote indicado.
func alertaDePrueba(loteID, tipo, mensaje string) *alerta.Alerta {
	return &alerta.Alerta{
		LoteID:    &loteID,
		HornoID:   "horno-01",
		Tipo:      tipo,
		Severidad: alerta.SeveridadWarning,
		Estado:    alerta.EstadoActiva,
		Mensaje:   mensaje,
	}
}

// TestAlertaRepo_SaveDedupeActiva verifica que dos alertas ACTIVA con el mismo
// lote+tipo no generan una segunda fila y que Save completa ID/CreadaEn.
func TestAlertaRepo_SaveDedupeActiva(t *testing.T) {
	ctx := context.Background()
	repo, _, loteID := setupAlertaRepo(t)

	a1 := alertaDePrueba(loteID, alerta.TipoTemperaturaHorno, "primera")
	require.NoError(t, repo.Save(a1))
	require.NotEmpty(t, a1.ID, "Save debe completar el ID")
	require.False(t, a1.CreadaEn.IsZero(), "Save debe completar CreadaEn")

	// Mismo lote+tipo ACTIVA: el índice parcial la descarta sin error.
	dedupe := alertaDePrueba(loteID, alerta.TipoTemperaturaHorno, "duplicada")
	require.NoError(t, repo.Save(dedupe), "un conflicto de dedupe no es error")
	require.Empty(t, dedupe.ID, "la fila duplicada no debe insertarse")

	lista, err := repo.Listar(ctx, alerta.EstadoActiva)
	require.NoError(t, err)
	require.Len(t, lista, 1)
	require.Equal(t, "primera", lista[0].Mensaje)

	// GetByHornoID devuelve la alerta con el lote resuelto.
	porHorno, err := repo.GetByHornoID("horno-01")
	require.NoError(t, err)
	require.Len(t, porHorno, 1)
	require.NotNil(t, porHorno[0].LoteID)
	require.Equal(t, loteID, *porHorno[0].LoteID)
}

// TestAlertaRepo_ListarFiltraPorEstadoYOrdenaDesc verifica el filtro opcional
// por estado y el orden created_at DESC.
func TestAlertaRepo_ListarFiltraPorEstadoYOrdenaDesc(t *testing.T) {
	ctx := context.Background()
	repo, _, loteID := setupAlertaRepo(t)

	a1 := alertaDePrueba(loteID, alerta.TipoTemperaturaHorno, "primera")
	require.NoError(t, repo.Save(a1))

	time.Sleep(2 * time.Millisecond)
	a2 := alertaDePrueba(loteID, alerta.TipoConsignaEnlace, "segunda")
	require.NoError(t, repo.Save(a2))

	time.Sleep(2 * time.Millisecond)
	ack := alertaDePrueba(loteID, alerta.TipoTemperaturaHorno, "reconocida")
	ack.Estado = alerta.EstadoAck
	require.NoError(t, repo.Save(ack))

	activas, err := repo.Listar(ctx, alerta.EstadoActiva)
	require.NoError(t, err)
	require.Len(t, activas, 2)
	require.Equal(t, a2.ID, activas[0].ID, "la más reciente primero")
	require.Equal(t, a1.ID, activas[1].ID)

	acks, err := repo.Listar(ctx, alerta.EstadoAck)
	require.NoError(t, err)
	require.Len(t, acks, 1)
	require.Equal(t, ack.ID, acks[0].ID)

	todas, err := repo.Listar(ctx, "")
	require.NoError(t, err)
	require.Len(t, todas, 3)
	require.Equal(t, ack.ID, todas[0].ID)
}

// TestAlertaRepo_Ack verifica estado/acked_por/acked_at, el error de
// idempotencia al segundo intento y ErrNotFound para un id desconocido.
func TestAlertaRepo_Ack(t *testing.T) {
	ctx := context.Background()
	repo, _, loteID := setupAlertaRepo(t)

	a := alertaDePrueba(loteID, alerta.TipoTemperaturaHorno, "reconocer")
	require.NoError(t, repo.Save(a))

	actualizada, err := repo.Ack(ctx, a.ID, alerta.Usuario{Email: alertaTestEmail, Nombre: "ignorado"})
	require.NoError(t, err)
	require.Equal(t, alerta.EstadoAck, actualizada.Estado)
	require.NotNil(t, actualizada.AckedPor)
	require.Equal(t, alertaTestEmail, actualizada.AckedPor.Email)
	require.Equal(t, alertaTestNombre, actualizada.AckedPor.Nombre, "el nombre real se resuelve desde usuarios")
	require.NotNil(t, actualizada.AckedAt)

	_, err = repo.Ack(ctx, a.ID, alerta.Usuario{Email: alertaTestEmail})
	require.ErrorIs(t, err, alerta.ErrYaReconocida)

	_, err = repo.Ack(ctx, "00000000-0000-0000-0000-000000000000", alerta.Usuario{Email: alertaTestEmail})
	require.ErrorIs(t, err, alerta.ErrNotFound)
}
