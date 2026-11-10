package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
)

const testLoteConsignaID = "9f8e7d6c-5b4a-4392-8170-6f5e4d3c2b1a"

// TestConsignaRepo_SaveYMarcarAplicada cubre el ciclo de vida nuevo: Save
// persiste decision_id/estado (con default PENDIENTE), el ack la cierra como
// APLICADA y el update es idempotente incluso sin fila.
func TestConsignaRepo_SaveYMarcarAplicada(t *testing.T) {
	ctx := context.Background()
	repo := NewConsignaPostgresRepository(repositoryTestPool(t))

	decisionID := "11111111-1111-1111-1111-111111111111"
	loteID := testLoteConsignaID
	motivo := "fallo previo"

	c := &consigna.Consigna{
		HornoID:                "horno-1",
		LoteID:                 &loteID,
		TemperaturaObjetivo:    180,
		VelocidadCintaObjetivo: 0.2,
		Origen:                 consigna.OrigenManual,
		Exitosa:                true,
		DecisionID:             &decisionID,
		MotivoError:            &motivo,
	}
	require.NoError(t, repo.Save(ctx, c), "Save debe defaultear estado a PENDIENTE")
	require.Equal(t, consigna.EstadoConsignaPendiente, c.Estado)

	historial, err := repo.GetByLoteID(ctx, loteID)
	require.NoError(t, err)
	require.Len(t, historial, 1)
	require.NotNil(t, historial[0].DecisionID)
	require.Equal(t, decisionID, *historial[0].DecisionID)
	require.Equal(t, consigna.EstadoConsignaPendiente, historial[0].Estado)

	require.NoError(t, repo.MarcarAplicadaPorDecision(ctx, decisionID))

	historial, err = repo.GetByLoteID(ctx, loteID)
	require.NoError(t, err)
	require.Equal(t, consigna.EstadoConsignaAplicada, historial[0].Estado)
	require.True(t, historial[0].Exitosa)
	require.Nil(t, historial[0].MotivoError)

	// Idempotente y sin fila: no es error.
	require.NoError(t, repo.MarcarAplicadaPorDecision(ctx, decisionID))
	require.NoError(t, repo.MarcarAplicadaPorDecision(ctx, "22222222-2222-2222-2222-222222222222"))
}
