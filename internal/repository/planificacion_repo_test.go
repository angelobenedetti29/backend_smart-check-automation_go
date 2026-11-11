package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/planificacion"
)

const (
	planTestSectorID   = "sector-plan-1"
	planTestProductoID = "cccccccc-0000-0000-0000-000000000001"
	planTestLoteID     = "dddddddd-0000-0000-0000-000000000001"
)

// setupPlanificacionRepo crea un sector, un producto y un lote para las FK.
func setupPlanificacionRepo(t *testing.T) *PlanificacionPostgresRepository {
	t.Helper()
	pool := repositoryTestPool(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `INSERT INTO sectores (id, nombre) VALUES ($1, 'Sector Plan')`, planTestSectorID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO productos (
			id, nombre, activo,
			peso_referencia_kg,
			temp_min, temp_max, velocidad_cinta_min, velocidad_cinta_max
		) VALUES ($1, 'Producto Plan', true, 0.25, 180, 220, 0.1, 0.3)`, planTestProductoID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO lotes_productivos (id, producto_id, inicio_at, estado, sector_id)
		VALUES ($1, $2, now(), 'CERRADO', $3)`, planTestLoteID, planTestProductoID, planTestSectorID)
	require.NoError(t, err)

	return NewPlanificacionPostgresRepository(pool)
}

func TestPlanificacionRepository_UpsertEIdempotencia(t *testing.T) {
	ctx := context.Background()
	repo := setupPlanificacionRepo(t)
	inicio := time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)
	fin := inicio.Add(8 * time.Hour)

	// Plan: primer upsert inserta y el segundo (misma clave) actualiza.
	plan := planificacion.PlanRow{
		SectorID: planTestSectorID, ProductoID: planTestProductoID,
		Fecha: inicio, Turno: "mañana", InicioAt: inicio, FinAt: fin, KgPlan: 500,
	}
	ins, upd, err := repo.UpsertPlan(ctx, []planificacion.PlanRow{plan})
	require.NoError(t, err)
	assert.Equal(t, 1, ins)
	assert.Equal(t, 0, upd)

	plan.KgPlan = 750
	ins, upd, err = repo.UpsertPlan(ctx, []planificacion.PlanRow{plan})
	require.NoError(t, err)
	assert.Equal(t, 0, ins)
	assert.Equal(t, 1, upd)

	var kg float64
	require.NoError(t, repo.db.QueryRow(ctx,
		`SELECT kg_plan FROM plan_produccion WHERE sector_id=$1 AND inicio_at=$2 AND producto_id=$3`,
		planTestSectorID, inicio, planTestProductoID).Scan(&kg))
	assert.Equal(t, 750.0, kg)

	// Paradas: idempotencia por (sector_id, inicio_at, causa_codigo).
	parada := planificacion.ParadaRow{
		SectorID: planTestSectorID, LoteID: planTestLoteID,
		InicioAt: inicio, FinAt: inicio.Add(time.Hour),
		CausaCodigo: "FALLA", Tipo: "NO_PLANIFICADA", Notas: "primera",
	}
	ins, upd, err = repo.UpsertParadas(ctx, []planificacion.ParadaRow{parada})
	require.NoError(t, err)
	assert.Equal(t, 1, ins)
	assert.Equal(t, 0, upd)

	parada.Notas = "actualizada"
	ins, upd, err = repo.UpsertParadas(ctx, []planificacion.ParadaRow{parada})
	require.NoError(t, err)
	assert.Equal(t, 0, ins)
	assert.Equal(t, 1, upd)

	var notas string
	require.NoError(t, repo.db.QueryRow(ctx,
		`SELECT notas FROM eventos_parada WHERE sector_id=$1 AND inicio_at=$2 AND causa_codigo=$3`,
		planTestSectorID, inicio, "FALLA").Scan(&notas))
	assert.Equal(t, "actualizada", notas)
}

func TestPlanificacionRepository_FKsInvalidas(t *testing.T) {
	ctx := context.Background()
	repo := setupPlanificacionRepo(t)
	inicio := time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)

	_, _, err := repo.UpsertPlan(ctx, []planificacion.PlanRow{{
		SectorID: "sector-inexistente", ProductoID: planTestProductoID,
		Fecha: inicio, Turno: "mañana", InicioAt: inicio, FinAt: inicio.Add(time.Hour), KgPlan: 10,
	}})
	require.Error(t, err, "sector_id inexistente debe violar la FK")

	_, _, err = repo.UpsertPlan(ctx, []planificacion.PlanRow{{
		SectorID: planTestSectorID, ProductoID: "00000000-0000-0000-0000-000000000000",
		Fecha: inicio, Turno: "mañana", InicioAt: inicio, FinAt: inicio.Add(time.Hour), KgPlan: 10,
	}})
	require.Error(t, err, "producto_id inexistente debe violar la FK")

	_, _, err = repo.UpsertParadas(ctx, []planificacion.ParadaRow{{
		SectorID: planTestSectorID, LoteID: "00000000-0000-0000-0000-000000000000",
		InicioAt: inicio, FinAt: inicio.Add(time.Hour), CausaCodigo: "X", Tipo: "PLANIFICADA",
	}})
	require.Error(t, err, "lote_id inexistente debe violar la FK")
}

func TestPlanificacionRepository_Listados(t *testing.T) {
	ctx := context.Background()
	repo := setupPlanificacionRepo(t)
	inicio := time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)

	_, _, err := repo.UpsertPlan(ctx, []planificacion.PlanRow{{
		SectorID: planTestSectorID, ProductoID: planTestProductoID,
		Fecha: inicio, Turno: "mañana", InicioAt: inicio, FinAt: inicio.Add(time.Hour), KgPlan: 10,
	}})
	require.NoError(t, err)
	_, _, err = repo.UpsertParadas(ctx, []planificacion.ParadaRow{{
		SectorID: planTestSectorID, InicioAt: inicio, FinAt: inicio.Add(time.Hour),
		CausaCodigo: "X", Tipo: "PLANIFICADA",
	}})
	require.NoError(t, err)

	planes, err := repo.ListPlan(ctx, planTestSectorID, inicio, inicio.Add(24*time.Hour))
	require.NoError(t, err)
	require.Len(t, planes, 1)

	paradas, err := repo.ListParadas(ctx, planTestSectorID, inicio, inicio.Add(24*time.Hour))
	require.NoError(t, err)
	require.Len(t, paradas, 1)
}

func TestPlanificacionRepository_ListadoItemsYDelete(t *testing.T) {
	ctx := context.Background()
	repo := setupPlanificacionRepo(t)
	inicio := time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)

	_, err := repo.db.Exec(ctx, `INSERT INTO sectores (id, nombre) VALUES ('sector-plan-2', 'Sector Plan 2')`)
	require.NoError(t, err)

	// Dos filas en la ventana y una fuera (+24h) en el sector 1; una en el 2.
	sectores := []string{planTestSectorID, planTestSectorID, planTestSectorID, "sector-plan-2"}
	offsets := []int{0, 1, 24, 2}
	for i, sector := range sectores {
		ini := inicio.Add(time.Duration(offsets[i]) * time.Hour)
		_, _, err := repo.UpsertPlan(ctx, []planificacion.PlanRow{{
			SectorID: sector, ProductoID: planTestProductoID, Fecha: ini,
			Turno: "mañana", InicioAt: ini, FinAt: ini.Add(30 * time.Minute), KgPlan: float64(100 + i),
		}})
		require.NoError(t, err)
		causa := "CAUSA-" + string(rune('A'+i))
		_, _, err = repo.UpsertParadas(ctx, []planificacion.ParadaRow{{
			SectorID: sector, LoteID: planTestLoteID, InicioAt: ini, FinAt: ini.Add(10 * time.Minute),
			CausaCodigo: causa, Tipo: "PLANIFICADA",
		}})
		require.NoError(t, err)
	}

	filtro := planificacion.FiltroListado{
		SectorID: planTestSectorID,
		Desde:    inicio,
		Hasta:    inicio.Add(24 * time.Hour),
		Limite:   1,
	}

	planes, err := repo.ListPlanItems(ctx, filtro)
	require.NoError(t, err)
	assert.Equal(t, 2, planes.Total)
	assert.True(t, planes.Truncada)
	require.Len(t, planes.Items, 1)
	assert.True(t, inicio.Add(time.Hour).Equal(planes.Items[0].InicioAt))
	assert.Equal(t, "2026-11-04", planes.Items[0].Fecha)
	assert.Equal(t, "Sector Plan", planes.Items[0].SectorNombre)
	assert.Equal(t, "Producto Plan", planes.Items[0].ProductoNombre)
	assert.Equal(t, "-03:00", planes.Items[0].InicioAt.Format("-07:00"))

	// Sin filtro de sector y sin truncar entran los tres de la ventana.
	filtro.SectorID = ""
	filtro.Limite = 500
	planes, err = repo.ListPlanItems(ctx, filtro)
	require.NoError(t, err)
	assert.Equal(t, 3, planes.Total)
	assert.False(t, planes.Truncada)
	assert.Len(t, planes.Items, 3)
	assert.True(t, planes.Items[0].InicioAt.After(planes.Items[1].InicioAt), "orden DESC por inicio_at")

	paradas, err := repo.ListParadaItems(ctx, filtro)
	require.NoError(t, err)
	assert.Equal(t, 3, paradas.Total)
	assert.False(t, paradas.Truncada)
	require.NotEmpty(t, paradas.Items[0].LoteID)
	assert.Equal(t, planTestLoteID, *paradas.Items[0].LoteID)

	// Delete: existente true, repetido/inexistente false.
	borrado, err := repo.DeletePlan(ctx, planes.Items[0].ID)
	require.NoError(t, err)
	assert.True(t, borrado)
	borrado, err = repo.DeletePlan(ctx, planes.Items[0].ID)
	require.NoError(t, err)
	assert.False(t, borrado)

	borrado, err = repo.DeleteParada(ctx, paradas.Items[0].ID)
	require.NoError(t, err)
	assert.True(t, borrado)
}

func TestPlanificacionRepository_PlanSolapados(t *testing.T) {
	ctx := context.Background()
	repo := setupPlanificacionRepo(t)
	base := time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)

	_, _, err := repo.UpsertPlan(ctx, []planificacion.PlanRow{{
		SectorID: planTestSectorID, ProductoID: planTestProductoID,
		Fecha: base, Turno: "mañana", InicioAt: base, FinAt: base.Add(8 * time.Hour), KgPlan: 500,
	}})
	require.NoError(t, err)

	// Solape que empieza antes del rango consultado: 06-14 contra 07-09.
	got, err := repo.PlanSolapados(ctx, planTestSectorID, base.Add(time.Hour), base.Add(3*time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, base.Equal(got[0].InicioAt))

	// Adyacente (fin == inicio del rango) no solapa.
	got, err = repo.PlanSolapados(ctx, planTestSectorID, base.Add(8*time.Hour), base.Add(9*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, got)

	// Filtra por sector.
	_, err = repo.db.Exec(ctx, `INSERT INTO sectores (id, nombre) VALUES ('sector-plan-2', 'Sector Plan 2')`)
	require.NoError(t, err)
	_, _, err = repo.UpsertPlan(ctx, []planificacion.PlanRow{{
		SectorID: "sector-plan-2", ProductoID: planTestProductoID,
		Fecha: base, Turno: "mañana", InicioAt: base.Add(2 * time.Hour), FinAt: base.Add(6 * time.Hour), KgPlan: 100,
	}})
	require.NoError(t, err)

	got, err = repo.PlanSolapados(ctx, planTestSectorID, base, base.Add(24*time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, planTestSectorID, got[0].SectorID)
}
