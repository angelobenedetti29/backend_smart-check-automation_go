package repository

import (
	"context"
	"errors"
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
	a2 := alertaDePrueba(loteID, alerta.TipoPorcentajeQuemadosAlto, "segunda")
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

type fakeEncolador struct {
	calls    int
	alertaID string
	err      error
}

func (f *fakeEncolador) EncolarParaAlerta(ctx context.Context, a *alerta.Alerta) error {
	f.calls++
	f.alertaID = a.ID
	return f.err
}

// TestAlertaRepo_EncoladorSoloEnInsertReal verifica que el encolador se invoque
// únicamente cuando el INSERT creó una fila (no en una alerta deduplicada) y
// que un fallo del encolador no haga fallar Save.
func TestAlertaRepo_EncoladorSoloEnInsertReal(t *testing.T) {
	repo, _, loteID := setupAlertaRepo(t)
	enc := &fakeEncolador{}
	repo.SetEncolador(enc)

	a := alertaDePrueba(loteID, alerta.TipoTemperaturaHorno, "primera")
	require.NoError(t, repo.Save(a))
	require.Equal(t, 1, enc.calls)
	require.Equal(t, a.ID, enc.alertaID)

	dedupe := alertaDePrueba(loteID, alerta.TipoTemperaturaHorno, "duplicada")
	require.NoError(t, repo.Save(dedupe))
	require.Equal(t, 1, enc.calls, "una alerta deduplicada no debe encolar")

	enc.err = errors.New("proveedor caído")
	a2 := alertaDePrueba(loteID, alerta.TipoPorcentajeQuemadosAlto, "segunda")
	require.NoError(t, repo.Save(a2), "un fallo del encolador no debe romper Save")
	require.Equal(t, 2, enc.calls)
}

// TestAlertaRepo_DispositivoID verifica que dispositivo_id se persiste, se
// lee en Listar y deduplica por (dispositivo_id, tipo) mientras está ACTIVA.
func TestAlertaRepo_DispositivoID(t *testing.T) {
	ctx := context.Background()
	repo, pool, _ := setupAlertaRepo(t)

	var deviceID string
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO dispositivos (nombre, tipo) VALUES ('Pi salida', 'SALIDA_HORNO') RETURNING id::text`,
	).Scan(&deviceID))

	a := &alerta.Alerta{
		DispositivoID: &deviceID,
		Tipo:          alerta.TipoDispositivoOffline,
		Severidad:     alerta.SeveridadWarning,
		Estado:        alerta.EstadoActiva,
		Mensaje:       "dispositivo sin reportar",
	}
	require.NoError(t, repo.Save(a))
	require.NotEmpty(t, a.ID)

	dup := &alerta.Alerta{
		DispositivoID: &deviceID,
		Tipo:          alerta.TipoDispositivoOffline,
		Severidad:     alerta.SeveridadWarning,
		Estado:        alerta.EstadoActiva,
		Mensaje:       "duplicada",
	}
	require.NoError(t, repo.Save(dup), "un conflicto de dedupe no es error")
	require.Empty(t, dup.ID, "la fila duplicada no debe insertarse")

	lista, err := repo.Listar(ctx, alerta.EstadoActiva)
	require.NoError(t, err)
	require.Len(t, lista, 1)
	require.NotNil(t, lista[0].DispositivoID)
	require.Equal(t, deviceID, *lista[0].DispositivoID)
}
