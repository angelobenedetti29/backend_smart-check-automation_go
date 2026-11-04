package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/planificacion"
)

// fakePlanificacionRepo es un doble configurable de planificacion.Repository.
type fakePlanificacionRepo struct {
	sectores  map[string]bool
	productos map[string]bool
	lotes     map[string]bool
	existsErr error

	planRows    []planificacion.PlanRow
	paradaRows  []planificacion.ParadaRow
	energiaRows []planificacion.EnergiaRow

	planesExistentes []planificacion.PlanRow
	solapadosErr     error

	planIns, planUpd       int
	paradaIns, paradaUpd   int
	energiaIns, energiaUpd int
	upsertErr              error
	upsertCalls            int

	listadoFiltro planificacion.FiltroListado
	listadoErr    error
	deleteOK      bool
	deleteErr     error
	deleteLlamado bool
}

func (f *fakePlanificacionRepo) SectoresExistentes(context.Context, []string) (map[string]bool, error) {
	return f.sectores, f.existsErr
}

func (f *fakePlanificacionRepo) ProductosExistentes(context.Context, []string) (map[string]bool, error) {
	return f.productos, f.existsErr
}

func (f *fakePlanificacionRepo) LotesExistentes(context.Context, []string) (map[string]bool, error) {
	return f.lotes, f.existsErr
}

func (f *fakePlanificacionRepo) UpsertPlan(_ context.Context, rows []planificacion.PlanRow) (int, int, error) {
	f.upsertCalls++
	f.planRows = rows
	return f.planIns, f.planUpd, f.upsertErr
}

func (f *fakePlanificacionRepo) UpsertParadas(_ context.Context, rows []planificacion.ParadaRow) (int, int, error) {
	f.upsertCalls++
	f.paradaRows = rows
	return f.paradaIns, f.paradaUpd, f.upsertErr
}

func (f *fakePlanificacionRepo) UpsertEnergia(_ context.Context, rows []planificacion.EnergiaRow) (int, int, error) {
	f.upsertCalls++
	f.energiaRows = rows
	return f.energiaIns, f.energiaUpd, f.upsertErr
}

func (f *fakePlanificacionRepo) ListPlan(context.Context, string, time.Time, time.Time) ([]planificacion.PlanRow, error) {
	return nil, nil
}

func (f *fakePlanificacionRepo) ListParadas(context.Context, string, time.Time, time.Time) ([]planificacion.ParadaRow, error) {
	return nil, nil
}

func (f *fakePlanificacionRepo) ListEnergia(context.Context, string, time.Time, time.Time) ([]planificacion.EnergiaRow, error) {
	return nil, nil
}

func (f *fakePlanificacionRepo) PlanSolapados(_ context.Context, sectorID string, desde, hasta time.Time) ([]planificacion.PlanRow, error) {
	if f.solapadosErr != nil {
		return nil, f.solapadosErr
	}
	out := []planificacion.PlanRow{}
	for _, p := range f.planesExistentes {
		if p.SectorID == sectorID && p.InicioAt.Before(hasta) && p.FinAt.After(desde) {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakePlanificacionRepo) ListPlanItems(_ context.Context, filtro planificacion.FiltroListado) (planificacion.Listado[planificacion.PlanItem], error) {
	f.listadoFiltro = filtro
	return planificacion.Listado[planificacion.PlanItem]{}, f.listadoErr
}

func (f *fakePlanificacionRepo) ListParadaItems(_ context.Context, filtro planificacion.FiltroListado) (planificacion.Listado[planificacion.ParadaItem], error) {
	f.listadoFiltro = filtro
	return planificacion.Listado[planificacion.ParadaItem]{}, f.listadoErr
}

func (f *fakePlanificacionRepo) ListEnergiaItems(_ context.Context, filtro planificacion.FiltroListado) (planificacion.Listado[planificacion.EnergiaItem], error) {
	f.listadoFiltro = filtro
	return planificacion.Listado[planificacion.EnergiaItem]{}, f.listadoErr
}

func (f *fakePlanificacionRepo) DeletePlan(context.Context, string) (bool, error) {
	f.deleteLlamado = true
	return f.deleteOK, f.deleteErr
}

func (f *fakePlanificacionRepo) DeleteParada(context.Context, string) (bool, error) {
	f.deleteLlamado = true
	return f.deleteOK, f.deleteErr
}

func (f *fakePlanificacionRepo) DeleteEnergia(context.Context, string) (bool, error) {
	f.deleteLlamado = true
	return f.deleteOK, f.deleteErr
}

const planHeader = "sector_id,fecha,turno,producto_id,inicio,fin,kg_plan\n"

func fakePlanRepoValido() *fakePlanificacionRepo {
	return &fakePlanificacionRepo{
		sectores:  map[string]bool{"s1": true},
		productos: map[string]bool{"prod-1": true},
		lotes:     map[string]bool{"lote-1": true},
		planIns:   1,
	}
}

func TestImportarPlan_CSVValido(t *testing.T) {
	repo := fakePlanRepoValido()
	svc := NewService(repo)

	csv := planHeader + "s1,2026-11-04,mañana,prod-1,2026-11-04 06:00,2026-11-04 14:00,500.500\n"
	res, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Procesadas)
	assert.Equal(t, 1, res.Insertadas)
	assert.Equal(t, 0, res.Actualizadas)
	assert.Empty(t, res.Errores)
	require.Len(t, repo.planRows, 1)
	assert.Equal(t, "s1", repo.planRows[0].SectorID)
	assert.Equal(t, "mañana", repo.planRows[0].Turno)
	assert.Equal(t, 500.5, repo.planRows[0].KgPlan)
}

func TestImportarPlan_AceptaBOM(t *testing.T) {
	repo := fakePlanRepoValido()
	svc := NewService(repo)

	csv := "\ufeff" + planHeader + "s1,2026-11-04,tarde,prod-1,2026-11-04 14:00,2026-11-04 22:00,10\n"
	res, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Insertadas)
	assert.Empty(t, res.Errores)
}

func TestImportarPlan_HeaderInvalido(t *testing.T) {
	repo := fakePlanRepoValido()
	svc := NewService(repo)

	csv := "sector,fecha,turno,producto_id,inicio,fin,kg_plan\n" +
		"s1,2026-11-04,tarde,prod-1,2026-11-04 14:00,2026-11-04 22:00,10\n"
	_, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.ErrorIs(t, err, planificacion.ErrFormatoCSV)
	assert.Zero(t, repo.upsertCalls, "no debe insertar con cabecera inválida")
}

func TestImportarPlan_FilaInvalidaNoSeInserta(t *testing.T) {
	repo := fakePlanRepoValido()
	svc := NewService(repo)

	csv := planHeader +
		"s1,2026-11-04,tarde,prod-1,2026-11-04 14:00,2026-11-04 22:00,10\n" +
		"s1,2026-11-04,noche,prod-1,2026-11-04 22:00,2026-11-04 23:00,-5\n"
	res, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Equal(t, 2, res.Procesadas)
	assert.Equal(t, 1, res.Insertadas)
	require.Len(t, res.Errores, 1)
	assert.Equal(t, 3, res.Errores[0].Fila)
	require.Len(t, repo.planRows, 1)
}

func TestImportarPlan_ReferenciasInexistentes(t *testing.T) {
	repo := fakePlanRepoValido()
	svc := NewService(repo)

	csv := planHeader +
		"s1,2026-11-04,tarde,prod-x,2026-11-04 14:00,2026-11-04 22:00,10\n" +
		"sx,2026-11-04,tarde,prod-1,2026-11-04 14:00,2026-11-04 22:00,10\n"
	res, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Equal(t, 2, res.Procesadas)
	assert.Zero(t, res.Insertadas)
	require.Len(t, res.Errores, 2)
	assert.Equal(t, "producto_id inexistente", res.Errores[0].Mensaje)
	assert.Equal(t, "sector_id inexistente", res.Errores[1].Mensaje)
	assert.Zero(t, repo.upsertCalls, "todas inválidas: no debe abrir transacción")
}

func TestImportarPlan_FechasARTyRFC3339(t *testing.T) {
	repo := fakePlanRepoValido()
	svc := NewService(repo)

	csv := planHeader +
		"s1,2026-11-04,tarde,prod-1,2026-11-04 14:00,2026-11-04 22:00,10\n" +
		"s1,2026-11-05,tarde,prod-1,2026-11-05T14:00:00-03:00,2026-11-05T22:00:00-03:00,20\n"
	_, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, repo.planRows, 2)

	esperadoART := time.Date(2026, 11, 4, 14, 0, 0, 0, zonaART())
	assert.True(t, repo.planRows[0].InicioAt.Equal(esperadoART))
	esperadoRFC := time.Date(2026, 11, 5, 14, 0, 0, 0, time.FixedZone("ART", -3*60*60))
	assert.True(t, repo.planRows[1].InicioAt.Equal(esperadoRFC))
}

func TestImportarPlan_ErrorDeRepo(t *testing.T) {
	repo := fakePlanRepoValido()
	repo.upsertErr = errors.New("db caída")
	svc := NewService(repo)

	csv := planHeader + "s1,2026-11-04,tarde,prod-1,2026-11-04 14:00,2026-11-04 22:00,10\n"
	_, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.ErrorContains(t, err, "db caída")
}

func TestImportarPlan_SolapeConExistenteRechazado(t *testing.T) {
	repo := fakePlanRepoValido()
	inicio := time.Date(2026, 11, 4, 6, 0, 0, 0, zonaART())
	repo.planesExistentes = []planificacion.PlanRow{{
		SectorID: "s1", ProductoID: "prod-1",
		InicioAt: inicio, FinAt: inicio.Add(8 * time.Hour), KgPlan: 500,
	}}
	svc := NewService(repo)

	csv := planHeader + "s1,2026-11-04,mañana,prod-1,2026-11-04 10:00,2026-11-04 18:00,100\n"
	res, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Equal(t, 1, res.Procesadas)
	assert.Zero(t, res.Insertadas)
	assert.Zero(t, repo.upsertCalls, "la fila solapada no debe llegar al upsert")
	require.Len(t, res.Errores, 1)
	assert.Equal(t, 2, res.Errores[0].Fila)
	assert.Equal(t, "solape con otro plan del sector", res.Errores[0].Mensaje)
}

func TestImportarPlan_AdyacenteAceptado(t *testing.T) {
	repo := fakePlanRepoValido()
	inicio := time.Date(2026, 11, 4, 6, 0, 0, 0, zonaART())
	repo.planesExistentes = []planificacion.PlanRow{{
		SectorID: "s1", ProductoID: "prod-1",
		InicioAt: inicio, FinAt: inicio.Add(8 * time.Hour), KgPlan: 500,
	}}
	svc := NewService(repo)

	csv := planHeader + "s1,2026-11-04,tarde,prod-1,2026-11-04 14:00,2026-11-04 22:00,100\n"
	res, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Empty(t, res.Errores)
	assert.Equal(t, 1, res.Insertadas)
	require.Len(t, repo.planRows, 1)
}

func TestImportarPlan_MismaClaveReimportadaActualiza(t *testing.T) {
	repo := fakePlanRepoValido()
	inicio := time.Date(2026, 11, 4, 6, 0, 0, 0, zonaART())
	repo.planesExistentes = []planificacion.PlanRow{{
		SectorID: "s1", ProductoID: "prod-1",
		InicioAt: inicio, FinAt: inicio.Add(8 * time.Hour), KgPlan: 500,
	}}
	repo.planIns, repo.planUpd = 0, 1
	svc := NewService(repo)

	csv := planHeader + "s1,2026-11-04,mañana,prod-1,2026-11-04 06:00,2026-11-04 15:00,600\n"
	res, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Empty(t, res.Errores)
	assert.Equal(t, 1, res.Actualizadas)
	require.Len(t, repo.planRows, 1)
	assert.Equal(t, 600.0, repo.planRows[0].KgPlan)
}

func TestImportarPlan_SolapeInternoPrimeraGana(t *testing.T) {
	repo := fakePlanRepoValido()
	svc := NewService(repo)

	csv := planHeader +
		"s1,2026-11-04,mañana,prod-1,2026-11-04 06:00,2026-11-04 14:00,100\n" +
		"s1,2026-11-04,tarde,prod-1,2026-11-04 12:00,2026-11-04 20:00,200\n"
	res, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Equal(t, 2, res.Procesadas)
	require.Len(t, res.Errores, 1)
	assert.Equal(t, 3, res.Errores[0].Fila)
	assert.Equal(t, "solape con otro plan del sector", res.Errores[0].Mensaje)
	require.Len(t, repo.planRows, 1, "sólo la primera fila debe insertarse")
	assert.Equal(t, 100.0, repo.planRows[0].KgPlan)
}

func TestImportarPlan_SinSolapeInsertanTodas(t *testing.T) {
	repo := fakePlanRepoValido()
	repo.planIns = 2
	svc := NewService(repo)

	csv := planHeader +
		"s1,2026-11-04,mañana,prod-1,2026-11-04 06:00,2026-11-04 14:00,100\n" +
		"s1,2026-11-04,tarde,prod-1,2026-11-04 14:00,2026-11-04 22:00,200\n"
	res, err := svc.ImportarPlan(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Empty(t, res.Errores)
	assert.Equal(t, 2, res.Insertadas)
	require.Len(t, repo.planRows, 2)
}

func TestImportarParadas_CSVValidoConLoteOpcional(t *testing.T) {
	repo := fakePlanRepoValido()
	repo.paradaIns = 2
	svc := NewService(repo)

	csv := "sector_id,lote_id,causa_codigo,tipo,inicio,fin,notas\n" +
		"s1,lote-1,FALLA_MOTOR,NO_PLANIFICADA,2026-11-04 10:00,2026-11-04 10:30, motor\n" +
		"s1,,LIMPIEZA,PLANIFICADA,2026-11-04 12:00,2026-11-04 12:15,\n"
	res, err := svc.ImportarParadas(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Equal(t, 2, res.Procesadas)
	assert.Equal(t, 2, res.Insertadas)
	require.Len(t, repo.paradaRows, 2)
	assert.Equal(t, "lote-1", repo.paradaRows[0].LoteID)
	assert.Equal(t, "motor", repo.paradaRows[0].Notas)
	assert.Empty(t, repo.paradaRows[1].LoteID)
}

func TestImportarEnergia_CSVValidoConGasOpcional(t *testing.T) {
	repo := fakePlanRepoValido()
	repo.energiaIns = 2
	svc := NewService(repo)

	csv := "sector_id,lote_id,periodo_inicio,periodo_fin,kwh,gas_m3\n" +
		"s1,lote-1,2026-11-04 06:00,2026-11-04 14:00,120.5,30.25\n" +
		"s1,,2026-11-04 14:00,2026-11-04 22:00,0,\n"
	res, err := svc.ImportarEnergia(context.Background(), strings.NewReader(csv))
	require.NoError(t, err)
	assert.Equal(t, 2, res.Procesadas)
	assert.Equal(t, 2, res.Insertadas)
	require.Len(t, repo.energiaRows, 2)
	require.NotNil(t, repo.energiaRows[0].GasM3)
	assert.Equal(t, 30.25, *repo.energiaRows[0].GasM3)
	assert.Nil(t, repo.energiaRows[1].GasM3)
	assert.Equal(t, "lote-1", repo.energiaRows[0].LoteID)
}

func servicioListado() (*Service, *fakePlanificacionRepo) {
	repo := &fakePlanificacionRepo{}
	svc := NewService(repo)
	svc.now = func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, zonaART()) }
	return svc, repo
}

func TestListarPlan_PeriodoDefaultYLimite(t *testing.T) {
	svc, repo := servicioListado()

	_, err := svc.ListarPlan(context.Background(), Query{})
	require.NoError(t, err)
	assert.Equal(t, "2026-10-06", repo.listadoFiltro.Desde.Format(fechaLayout))
	// Hasta exclusivo: medianoche ART del día siguiente al último día inclusive.
	assert.Equal(t, "2026-11-05", repo.listadoFiltro.Hasta.Format(fechaLayout))
	assert.Equal(t, 500, repo.listadoFiltro.Limite)
	assert.Empty(t, repo.listadoFiltro.SectorID)
}

func TestListarParadas_FiltroSectorYLimiteTope(t *testing.T) {
	svc, repo := servicioListado()

	_, err := svc.ListarParadas(context.Background(), Query{
		Desde: "2026-11-01", Hasta: "2026-11-03", SectorID: "s1", Limite: "9999",
	})
	require.NoError(t, err)
	assert.Equal(t, "2026-11-01", repo.listadoFiltro.Desde.Format(fechaLayout))
	assert.Equal(t, "2026-11-04", repo.listadoFiltro.Hasta.Format(fechaLayout))
	assert.Equal(t, "s1", repo.listadoFiltro.SectorID)
	assert.Equal(t, limiteMaximo, repo.listadoFiltro.Limite)

	// Limite no positivo cae al default.
	_, err = svc.ListarEnergia(context.Background(), Query{Limite: "0"})
	require.NoError(t, err)
	assert.Equal(t, limiteDefault, repo.listadoFiltro.Limite)
}

func TestListarEnergia_Validaciones(t *testing.T) {
	svc := NewService(&fakePlanificacionRepo{})

	casos := []struct {
		nombre string
		q      Query
		err    error
	}{
		{"fecha inválida", Query{Desde: "2026-13-01"}, planificacion.ErrFechaInvalida},
		{"desde mayor", Query{Desde: "2026-11-05", Hasta: "2026-11-04"}, planificacion.ErrDesdeMayorHasta},
		{"rango excedido", Query{Desde: "2025-01-01", Hasta: "2026-11-04"}, planificacion.ErrRangoExcedido},
		{"limite no numérico", Query{Limite: "muchos"}, planificacion.ErrLimiteInvalido},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			_, err := svc.ListarEnergia(context.Background(), c.q)
			assert.ErrorIs(t, err, c.err)
		})
	}
}

func TestEliminar_ValidacionUUIDYNoEncontrado(t *testing.T) {
	ctx := context.Background()
	repo := &fakePlanificacionRepo{}
	svc := NewService(repo)

	err := svc.EliminarPlan(ctx, "no-es-uuid")
	assert.ErrorIs(t, err, planificacion.ErrIDInvalido)
	assert.False(t, repo.deleteLlamado, "un id inválido no debe llegar al repositorio")

	err = svc.EliminarParada(ctx, "11111111-1111-1111-1111-111111111111")
	assert.ErrorIs(t, err, planificacion.ErrNoEncontrado)

	repo.deleteOK = true
	assert.NoError(t, svc.EliminarEnergia(ctx, "11111111-1111-1111-1111-111111111111"))
}
