package oee

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	domain "github.com/angelobenedetti29/smart-check-automation/internal/domain/oee"
)

type fakeRepo struct {
	q       domain.Query
	datos   *domain.Datos
	err     error
	llamado bool
}

func (f *fakeRepo) Datos(_ context.Context, q domain.Query) (*domain.Datos, error) {
	f.q = q
	f.llamado = true
	if f.err != nil {
		return nil, f.err
	}
	if f.datos != nil {
		return f.datos, nil
	}
	return &domain.Datos{Capacidades: map[string]*float64{}, Sectores: map[string]string{}}, nil
}

func servicioDePrueba(repo domain.Repository) *Service {
	svc := NewService(repo)
	svc.now = func() time.Time {
		return time.Date(2026, time.November, 4, 18, 0, 0, 0, time.UTC)
	}
	return svc
}

var art = time.FixedZone("ART", -3*60*60)

func fecha(dia, hora int) time.Time {
	return time.Date(2026, time.November, dia, hora, 0, 0, 0, art)
}

func ptr(v float64) *float64 { return &v }

func datosBase() *domain.Datos {
	return &domain.Datos{
		Sectores:    map[string]string{"s1": "Sector 1", "s2": "Sector 2"},
		Capacidades: map[string]*float64{"p1": ptr(87.5)},
		Plan: []domain.PlanOEE{{
			SectorID: "s1", ProductoID: "p1",
			InicioAt: fecha(2, 6), FinAt: fecha(2, 14), KgPlan: 1000,
		}},
		Paradas: []domain.ParadaOEE{{
			SectorID: "s1", InicioAt: fecha(2, 10), FinAt: fecha(2, 11), Tipo: "NO_PLANIFICADA",
		}},
		Lotes: []domain.LoteOEE{{SectorID: "s1", ProductoID: "p1", KgTotal: 600, KgOK: 570}},
	}
}

func TestOEE_CasoCompleto(t *testing.T) {
	repo := &fakeRepo{datos: datosBase()}
	svc := servicioDePrueba(repo)

	res, err := svc.OEE(context.Background(), Query{Desde: "2026-11-01", Hasta: "2026-11-04"})
	require.NoError(t, err)
	require.True(t, repo.llamado)

	require.Equal(t, "2026-11-01", res.Periodo.Desde)
	require.Equal(t, "2026-11-04", res.Periodo.Hasta)
	require.Nil(t, res.Filtros.SectorID)

	g := res.Global
	require.Equal(t, "", g.SectorID)
	require.Equal(t, "Todos", g.SectorNombre)
	require.True(t, g.Calculable)
	require.Empty(t, g.Motivos)
	require.InDelta(t, 87.5, *g.AvailabilityPct, 1e-9)
	require.InDelta(t, 85.71, *g.PerformancePct, 1e-9)
	require.InDelta(t, 95.0, *g.QualityPct, 1e-9)
	require.InDelta(t, 71.25, *g.OEEPct, 1e-9)
	require.InDelta(t, 480.0, g.PlannedTimeMin, 1e-9)
	require.InDelta(t, 0.0, g.PlannedDowntimeMin, 1e-9)
	require.InDelta(t, 60.0, g.UnplannedDowntimeMin, 1e-9)
	require.InDelta(t, 420.0, g.OperatingTimeMin, 1e-9)
	require.InDelta(t, 1000.0, g.KgPlan, 1e-9)
	require.InDelta(t, 600.0, g.KgProducidos, 1e-9)
	require.InDelta(t, 570.0, g.KgOK, 1e-9)
	require.InDelta(t, 700.0, g.CapacidadTeoricaKg, 1e-9)

	require.Len(t, res.PorSector, 1)
	require.Equal(t, "s1", res.PorSector[0].SectorID)
	require.Equal(t, "Sector 1", res.PorSector[0].SectorNombre)
	require.True(t, res.PorSector[0].Calculable)
	require.InDelta(t, 71.25, *res.PorSector[0].OEEPct, 1e-9)
}

func TestOEE_KgPlanProrrateadoPorSolape(t *testing.T) {
	datos := &domain.Datos{
		Sectores:    map[string]string{"s1": "Sector 1"},
		Capacidades: map[string]*float64{"p1": ptr(100)},
		Plan: []domain.PlanOEE{{
			SectorID: "s1", ProductoID: "p1",
			InicioAt: fecha(4, 20), FinAt: fecha(5, 4), KgPlan: 1000,
		}},
	}

	res, err := servicioDePrueba(&fakeRepo{datos: datos}).OEE(context.Background(),
		Query{Desde: "2026-11-01", Hasta: "2026-11-04"})
	require.NoError(t, err)

	g := res.Global
	// 4 h de las 8 h del plan caen en el período.
	require.InDelta(t, 240.0, g.PlannedTimeMin, 1e-9)
	require.InDelta(t, 500.0, g.KgPlan, 1e-9)
	require.InDelta(t, 400.0, g.CapacidadTeoricaKg, 1e-9)
}

func TestOEE_SinPlan(t *testing.T) {
	datos := &domain.Datos{
		Sectores:    map[string]string{"s2": "Sector 2"},
		Capacidades: map[string]*float64{},
		Lotes:       []domain.LoteOEE{{SectorID: "s2", ProductoID: "p1", KgTotal: 100, KgOK: 90}},
	}
	svc := servicioDePrueba(&fakeRepo{datos: datos})

	res, err := svc.OEE(context.Background(), Query{Desde: "2026-11-01", Hasta: "2026-11-04"})
	require.NoError(t, err)

	g := res.Global
	require.False(t, g.Calculable)
	require.Nil(t, g.AvailabilityPct)
	require.Nil(t, g.PerformancePct)
	require.InDelta(t, 90.0, *g.QualityPct, 1e-9)
	require.Nil(t, g.OEEPct)
	require.Equal(t, []string{"sin_plan_produccion"}, g.Motivos)
	require.InDelta(t, 0.0, g.PlannedTimeMin, 1e-9)

	require.Len(t, res.PorSector, 1)
	require.Equal(t, "s2", res.PorSector[0].SectorID)
	require.False(t, res.PorSector[0].Calculable)
	require.Equal(t, []string{"sin_plan_produccion"}, res.PorSector[0].Motivos)
}

func TestOEE_SinCapacidad(t *testing.T) {
	datos := datosBase()
	datos.Capacidades = map[string]*float64{"p1": nil}

	res, err := servicioDePrueba(&fakeRepo{datos: datos}).OEE(context.Background(),
		Query{Desde: "2026-11-01", Hasta: "2026-11-04"})
	require.NoError(t, err)

	g := res.Global
	require.False(t, g.Calculable)
	require.NotNil(t, g.AvailabilityPct)
	require.Nil(t, g.PerformancePct)
	require.NotNil(t, g.QualityPct)
	require.Nil(t, g.OEEPct)
	require.InDelta(t, 0.0, g.CapacidadTeoricaKg, 1e-9)
	require.Equal(t, []string{"sin_capacidad_producto:p1"}, g.Motivos)
}

func TestOEE_SinProduccion(t *testing.T) {
	datos := datosBase()
	datos.Capacidades = map[string]*float64{"p1": ptr(100)}
	datos.Lotes = nil

	res, err := servicioDePrueba(&fakeRepo{datos: datos}).OEE(context.Background(),
		Query{Desde: "2026-11-01", Hasta: "2026-11-04"})
	require.NoError(t, err)

	g := res.Global
	require.False(t, g.Calculable)
	require.InDelta(t, 87.5, *g.AvailabilityPct, 1e-9)
	require.InDelta(t, 0.0, *g.PerformancePct, 1e-9)
	require.Nil(t, g.QualityPct)
	require.Nil(t, g.OEEPct)
	require.Equal(t, []string{"sin_produccion"}, g.Motivos)
}

func TestOEE_DosSectores(t *testing.T) {
	datos := datosBase()
	datos.Capacidades["p1"] = ptr(100)
	datos.Lotes = append(datos.Lotes,
		domain.LoteOEE{SectorID: "s2", ProductoID: "p1", KgTotal: 100, KgOK: 90})

	res, err := servicioDePrueba(&fakeRepo{datos: datos}).OEE(context.Background(),
		Query{Desde: "2026-11-01", Hasta: "2026-11-04"})
	require.NoError(t, err)

	g := res.Global
	require.True(t, g.Calculable)
	require.InDelta(t, 87.5, *g.AvailabilityPct, 1e-9)
	require.InDelta(t, 87.5, *g.PerformancePct, 1e-9)
	require.InDelta(t, 94.29, *g.QualityPct, 1e-9)
	require.InDelta(t, 72.19, *g.OEEPct, 1e-9)
	require.InDelta(t, 700.0, g.KgProducidos, 1e-9)
	require.InDelta(t, 660.0, g.KgOK, 1e-9)
	require.Empty(t, g.Motivos)

	require.Len(t, res.PorSector, 2)
	s1, s2 := res.PorSector[0], res.PorSector[1]
	require.Equal(t, "s1", s1.SectorID)
	require.True(t, s1.Calculable)
	require.InDelta(t, 75.0, *s1.PerformancePct, 1e-9)
	require.InDelta(t, 62.34, *s1.OEEPct, 1e-9)

	require.Equal(t, "s2", s2.SectorID)
	require.False(t, s2.Calculable)
	require.Nil(t, s2.AvailabilityPct)
	require.InDelta(t, 90.0, *s2.QualityPct, 1e-9)
	require.Equal(t, []string{"sin_plan_produccion"}, s2.Motivos)
}

func TestOEE_Redondeos(t *testing.T) {
	datos := &domain.Datos{
		Sectores:    map[string]string{"s1": "Sector 1"},
		Capacidades: map[string]*float64{"p1": ptr(100)},
		Plan: []domain.PlanOEE{{
			SectorID: "s1", ProductoID: "p1",
			InicioAt: time.Date(2026, time.November, 2, 6, 0, 0, 0, art),
			FinAt:    time.Date(2026, time.November, 2, 7, 1, 1, 0, art),
			KgPlan:   10,
		}},
		Lotes: []domain.LoteOEE{{SectorID: "s1", ProductoID: "p1", KgTotal: 100.0004, KgOK: 90.0004}},
	}

	res, err := servicioDePrueba(&fakeRepo{datos: datos}).OEE(context.Background(),
		Query{Desde: "2026-11-01", Hasta: "2026-11-04"})
	require.NoError(t, err)

	g := res.Global
	require.InDelta(t, 61.0, g.PlannedTimeMin, 1e-9)
	require.InDelta(t, 61.0, g.OperatingTimeMin, 1e-9)
	require.InDelta(t, 101.694, g.CapacidadTeoricaKg, 1e-9)
	require.InDelta(t, 100.0, g.KgProducidos, 1e-9)
	require.InDelta(t, 90.0, g.KgOK, 1e-9)
	require.InDelta(t, 98.33, *g.PerformancePct, 1e-9)
	require.InDelta(t, 90.0, *g.QualityPct, 1e-9)
}

func TestOEE_FiltroSectorSinDatos(t *testing.T) {
	datos := &domain.Datos{
		Sectores:    map[string]string{"s1": "Sector 1"},
		Capacidades: map[string]*float64{},
	}
	res, err := servicioDePrueba(&fakeRepo{datos: datos}).OEE(context.Background(),
		Query{Desde: "2026-11-01", Hasta: "2026-11-04", SectorID: "s1"})
	require.NoError(t, err)

	require.NotNil(t, res.Filtros.SectorID)
	require.Equal(t, "s1", *res.Filtros.SectorID)
	require.False(t, res.Global.Calculable)
	require.Equal(t, []string{"sin_plan_produccion", "sin_produccion"}, res.Global.Motivos)
	require.Len(t, res.PorSector, 1)
	require.Equal(t, "Sector 1", res.PorSector[0].SectorNombre)
	require.False(t, res.PorSector[0].Calculable)
}

func TestOEE_PeriodoDefault(t *testing.T) {
	repo := &fakeRepo{}
	res, err := servicioDePrueba(repo).OEE(context.Background(), Query{})
	require.NoError(t, err)

	require.Equal(t, "2026-10-06", res.Periodo.Desde)
	require.Equal(t, "2026-11-04", res.Periodo.Hasta)
	require.Equal(t, domain.Timezone, res.Periodo.Timezone)
	require.Equal(t, 0, repo.q.Periodo.Desde.Hour())
}

func TestOEE_ValidacionesPeriodo(t *testing.T) {
	svc := servicioDePrueba(&fakeRepo{})

	_, err := svc.OEE(context.Background(), Query{Desde: "2026-13-01"})
	require.ErrorIs(t, err, domain.ErrFechaInvalida)

	_, err = svc.OEE(context.Background(), Query{Desde: "2026-11-05", Hasta: "2026-11-01"})
	require.ErrorIs(t, err, domain.ErrDesdeMayorHasta)

	_, err = svc.OEE(context.Background(), Query{Desde: "2025-01-01", Hasta: "2026-01-02"})
	require.ErrorIs(t, err, domain.ErrRangoExcedido)
}
