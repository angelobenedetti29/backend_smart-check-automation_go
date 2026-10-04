package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
)

// setupDecisionesControlRepo provisiona una base aislada con un lote ABIERTO y
// devuelve el repo de decisiones junto con el id del lote.
func setupDecisionesControlRepo(t *testing.T) (*DecisionesControlPostgresRepository, string) {
	t.Helper()
	loteRepo, pool := setupLoteSectorRepo(t)
	lote := abrirLote(t, context.Background(), loteRepo)
	return NewDecisionesControlPostgresRepository(pool), lote.ID
}

// decisionDePrueba arma una decisión válida (cumple los NOT NULL/CHECK de la
// tabla) pendiente de aplicación.
func decisionDePrueba(loteID string) *decisiones_control.DecisionControl {
	vel := 0.2
	ahora := time.Now().UTC()
	return &decisiones_control.DecisionControl{
		LoteID:         loteID,
		VentanaInicio:  ahora.Add(-time.Minute),
		VentanaFin:     ahora,
		Muestras:       10,
		TempActual:     200,
		VelActual:      &vel,
		PctCrudosObs:   10,
		PctOkObs:       80,
		PctQuemadosObs: 10,
		ConfPromedio:   0.9,
		TempPropuesto:  205,
		VelPropuesto:   0.22,
		Prediccion:     []byte(`{"pctOk":80}`),
		AplicadoPor:    decisiones_control.AplicadoPorNadie,
	}
}

// TestDecisionesControlRepo_Save verifica que Save completa ID y CreatedAt.
func TestDecisionesControlRepo_Save(t *testing.T) {
	ctx := context.Background()
	repo, loteID := setupDecisionesControlRepo(t)

	d := decisionDePrueba(loteID)
	require.NoError(t, repo.Save(ctx, d))
	require.NotEmpty(t, d.ID, "Save debe completar el ID")
	require.False(t, d.CreatedAt.IsZero(), "Save debe completar CreatedAt")
}

// TestDecisionesControlRepo_GetUltimaPendiente verifica los filtros: sólo la
// NADIE sin razon_skipped es una propuesta pendiente.
func TestDecisionesControlRepo_GetUltimaPendiente(t *testing.T) {
	ctx := context.Background()
	repo, loteID := setupDecisionesControlRepo(t)

	// 1. NADIE sin skip → es la última pendiente.
	d1 := decisionDePrueba(loteID)
	require.NoError(t, repo.Save(ctx, d1))
	got, err := repo.GetUltimaPendiente(ctx, loteID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, d1.ID, got.ID)

	// 2. Una NADIE con razon_skipped más nueva se filtra.
	time.Sleep(5 * time.Millisecond)
	skipped := "ventana_sin_muestras"
	d2 := decisionDePrueba(loteID)
	d2.RazonSkipped = &skipped
	require.NoError(t, repo.Save(ctx, d2))
	got, err = repo.GetUltimaPendiente(ctx, loteID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, d1.ID, got.ID, "no debe devolver una decisión con razon_skipped")

	// 3. Una OPERARIO más nueva se filtra.
	time.Sleep(5 * time.Millisecond)
	d3 := decisionDePrueba(loteID)
	d3.AplicadoPor = decisiones_control.AplicadoPorOperario
	require.NoError(t, repo.Save(ctx, d3))
	got, err = repo.GetUltimaPendiente(ctx, loteID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, d1.ID, got.ID, "no debe devolver una decisión aplicada")
}

// TestDecisionesControlRepo_MarcarAplicada verifica la promoción de una fila:
// deja de ser pendiente, queda con aplicado_por y self-ref en decision_origen_id.
func TestDecisionesControlRepo_MarcarAplicada(t *testing.T) {
	ctx := context.Background()
	repo, loteID := setupDecisionesControlRepo(t)

	d := decisionDePrueba(loteID)
	require.NoError(t, repo.Save(ctx, d))

	require.NoError(t, repo.MarcarAplicada(ctx, d.ID, 210, 0.3, decisiones_control.AplicadoPorOperario))

	got, err := repo.GetUltimaPendiente(ctx, loteID)
	require.NoError(t, err)
	require.Nil(t, got, "tras aplicar no debe quedar pendiente")

	lista, err := repo.ListByLote(ctx, loteID, 10)
	require.NoError(t, err)
	require.Len(t, lista, 1)
	require.Equal(t, decisiones_control.AplicadoPorOperario, lista[0].AplicadoPor)
	require.NotNil(t, lista[0].TempAplicado)
	require.Equal(t, 210.0, *lista[0].TempAplicado)
	require.NotNil(t, lista[0].VelAplicado)
	require.Equal(t, 0.3, *lista[0].VelAplicado)
	require.NotNil(t, lista[0].DecisionOrigenID)
	require.Equal(t, d.ID, *lista[0].DecisionOrigenID)
}

// TestDecisionesControlRepo_MarcarAplicadaIDInvalido verifica que marcar una
// decisión inexistente devuelve error explícito (RowsAffected == 0).
func TestDecisionesControlRepo_MarcarAplicadaIDInvalido(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupDecisionesControlRepo(t)

	err := repo.MarcarAplicada(ctx, "00000000-0000-0000-0000-000000000000", 200, 0.2, decisiones_control.AplicadoPorOperario)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no encontrada")
}

// TestDecisionesControlRepo_DecisionOrigenIDPersiste verifica que Save persiste
// decision_origen_id cuando viene seteado y NULL cuando no.
func TestDecisionesControlRepo_DecisionOrigenIDPersiste(t *testing.T) {
	ctx := context.Background()
	repo, loteID := setupDecisionesControlRepo(t)

	origen := decisionDePrueba(loteID)
	require.NoError(t, repo.Save(ctx, origen))

	promovida := decisionDePrueba(loteID)
	promovida.DecisionOrigenID = &origen.ID
	require.NoError(t, repo.Save(ctx, promovida))

	sinOrigen := decisionDePrueba(loteID)
	require.NoError(t, repo.Save(ctx, sinOrigen))

	lista, err := repo.ListByLote(ctx, loteID, 10)
	require.NoError(t, err)
	require.Len(t, lista, 3)

	porID := map[string]decisiones_control.DecisionControl{}
	for _, item := range lista {
		porID[item.ID] = item
	}
	require.NotNil(t, porID[promovida.ID].DecisionOrigenID)
	require.Equal(t, origen.ID, *porID[promovida.ID].DecisionOrigenID)
	require.Nil(t, porID[sinOrigen.ID].DecisionOrigenID)
}
