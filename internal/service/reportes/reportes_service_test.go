package reportes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	domain "github.com/angelobenedetti29/smart-check-automation/internal/domain/reportes"
	lotesectorSvc "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
)

// fakeRepo captura el período/filtros y devuelve un resumen vacío.
type fakeRepo struct {
	periodo domain.Periodo
	filtros domain.Filtros
	llamado bool

	lotesParams  domain.LotesParams
	lotes        []domain.LoteReporte
	totalLotes   int
	llamadoLotes bool

	trazabilidad *domain.Trazabilidad
	loteID       string
	errTraz      error
	llamadoTraz  bool

	alertasParams  domain.AlertasParams
	alertas        []domain.AlertaReporte
	totalAlertas   int
	errAlertas     error
	llamadoAlertas bool
}

func (f *fakeRepo) Alertas(_ context.Context, params domain.AlertasParams) ([]domain.AlertaReporte, int, error) {
	f.alertasParams = params
	f.llamadoAlertas = true
	return f.alertas, f.totalAlertas, f.errAlertas
}

func (f *fakeRepo) Trazabilidad(_ context.Context, loteID string) (*domain.Trazabilidad, error) {
	f.loteID = loteID
	f.llamadoTraz = true
	if f.errTraz != nil {
		return nil, f.errTraz
	}
	if f.trazabilidad != nil {
		return f.trazabilidad, nil
	}
	return &domain.Trazabilidad{}, nil
}

func (f *fakeRepo) Resumen(_ context.Context, periodo domain.Periodo, filtros domain.Filtros) (*domain.Resumen, error) {
	f.periodo = periodo
	f.filtros = filtros
	f.llamado = true
	return &domain.Resumen{}, nil
}

func (f *fakeRepo) Lotes(_ context.Context, params domain.LotesParams) ([]domain.LoteReporte, int, error) {
	f.lotesParams = params
	f.llamadoLotes = true
	return f.lotes, f.totalLotes, nil
}

func servicioDePrueba(repo domain.Repository) *Service {
	svc := NewService(repo)
	// 2026-11-04 15:00 ART.
	svc.now = func() time.Time {
		return time.Date(2026, time.November, 4, 18, 0, 0, 0, time.UTC)
	}
	return svc
}

func TestResumen_DefaultUltimos30Dias(t *testing.T) {
	repo := &fakeRepo{}
	svc := servicioDePrueba(repo)

	res, err := svc.Resumen(context.Background(), Query{})
	require.NoError(t, err)
	require.True(t, repo.llamado)

	require.Equal(t, "2026-10-06", res.Periodo.Desde)
	require.Equal(t, "2026-11-04", res.Periodo.Hasta)
	require.Equal(t, domain.Timezone, res.Periodo.Timezone)
	require.Equal(t, 2026, repo.periodo.Desde.Year())
	require.Equal(t, time.October, repo.periodo.Desde.Month())
	require.Equal(t, 6, repo.periodo.Desde.Day())
	require.Equal(t, 0, repo.periodo.Desde.Hour())
	require.Equal(t, time.November, repo.periodo.Hasta.Month())
	require.Equal(t, 4, repo.periodo.Hasta.Day())
}

func TestResumen_FechasExplicitasInclusivas(t *testing.T) {
	repo := &fakeRepo{}
	svc := servicioDePrueba(repo)

	res, err := svc.Resumen(context.Background(), Query{Desde: "2026-10-01", Hasta: "2026-10-31"})
	require.NoError(t, err)

	require.Equal(t, "2026-10-01", res.Periodo.Desde)
	require.Equal(t, "2026-10-31", res.Periodo.Hasta)
	require.Equal(t, time.Date(2026, time.October, 1, 0, 0, 0, 0, repo.periodo.Desde.Location()), repo.periodo.Desde)
	require.Equal(t, time.Date(2026, time.October, 31, 0, 0, 0, 0, repo.periodo.Hasta.Location()), repo.periodo.Hasta)
}

func TestResumen_DesdeMayorHasta(t *testing.T) {
	svc := servicioDePrueba(&fakeRepo{})

	_, err := svc.Resumen(context.Background(), Query{Desde: "2026-10-31", Hasta: "2026-10-01"})
	require.ErrorIs(t, err, domain.ErrDesdeMayorHasta)
}

func TestResumen_RangoExcedido(t *testing.T) {
	svc := servicioDePrueba(&fakeRepo{})

	_, err := svc.Resumen(context.Background(), Query{Desde: "2025-01-01", Hasta: "2026-01-02"})
	require.ErrorIs(t, err, domain.ErrRangoExcedido)

	// 366 días inclusive es el máximo permitido.
	_, err = svc.Resumen(context.Background(), Query{Desde: "2025-01-01", Hasta: "2026-01-01"})
	require.NoError(t, err)
}

func TestResumen_TurnoInvalido(t *testing.T) {
	repo := &fakeRepo{}
	svc := servicioDePrueba(repo)

	_, err := svc.Resumen(context.Background(), Query{Turno: "madrugada"})
	require.ErrorIs(t, err, domain.ErrTurnoInvalido)
	require.False(t, repo.llamado)
}

func TestResumen_ProductoIDInvalido(t *testing.T) {
	svc := servicioDePrueba(&fakeRepo{})

	_, err := svc.Resumen(context.Background(), Query{ProductoID: "no-es-uuid"})
	require.ErrorIs(t, err, domain.ErrProductoIDInvalido)
}

func TestResumen_PasaFiltros(t *testing.T) {
	repo := &fakeRepo{}
	svc := servicioDePrueba(repo)

	_, err := svc.Resumen(context.Background(), Query{
		SectorID:   "horno-1",
		ProductoID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		Turno:      "tarde",
	})
	require.NoError(t, err)
	require.NotNil(t, repo.filtros.SectorID)
	require.Equal(t, "horno-1", *repo.filtros.SectorID)
	require.NotNil(t, repo.filtros.ProductoID)
	require.Equal(t, "tarde", *repo.filtros.Turno)
}

func TestResumen_FechaInvalida(t *testing.T) {
	svc := servicioDePrueba(&fakeRepo{})

	_, err := svc.Resumen(context.Background(), Query{Desde: "01/10/2026"})
	require.ErrorIs(t, err, domain.ErrFechaInvalida)
}

func TestLotes_LimiteDefaultYMaximo(t *testing.T) {
	repo := &fakeRepo{}
	svc := servicioDePrueba(repo)
	ctx := context.Background()

	pagina, err := svc.Lotes(ctx, Query{})
	require.NoError(t, err)
	require.Equal(t, 100, pagina.Limite)
	require.Equal(t, 100, repo.lotesParams.Limite)

	_, err = svc.Lotes(ctx, Query{Limite: "5000"})
	require.NoError(t, err)
	require.Equal(t, 1000, repo.lotesParams.Limite)

	_, err = svc.Lotes(ctx, Query{Limite: "abc"})
	require.ErrorIs(t, err, domain.ErrLimiteInvalido)
}

func TestLotes_CursorInvalido(t *testing.T) {
	repo := &fakeRepo{}
	svc := servicioDePrueba(repo)

	_, err := svc.Lotes(context.Background(), Query{AntesDe: "%%%"})
	require.ErrorIs(t, err, domain.ErrCursorInvalido)
	require.False(t, repo.llamadoLotes)
}

func TestLotes_PasaFiltrosCursorYSiguiente(t *testing.T) {
	repo := &fakeRepo{}
	svc := servicioDePrueba(repo)

	cursorIn := lotesectorSvc.EncodeCursor(
		time.Date(2026, time.October, 31, 10, 0, 0, 0, time.UTC),
		"a1b2c3d4-5678-90ab-cdef-1234567890ab",
	)
	ultimo := domain.LoteReporte{
		ID:        "ffffffff-0000-0000-0000-000000000002",
		AbiertoEn: time.Date(2026, time.October, 30, 9, 0, 0, 0, time.UTC),
	}
	repo.lotes = []domain.LoteReporte{
		{ID: "ffffffff-0000-0000-0000-000000000001", AbiertoEn: time.Date(2026, time.October, 30, 10, 0, 0, 0, time.UTC)},
		ultimo,
	}
	repo.totalLotes = 5

	pagina, err := svc.Lotes(context.Background(), Query{
		SectorID:   "horno-1",
		ProductoID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		Turno:      "tarde",
		Limite:     "2",
		AntesDe:    cursorIn,
	})
	require.NoError(t, err)
	require.True(t, repo.llamadoLotes)
	require.Equal(t, 2, repo.lotesParams.Limite)
	require.NotNil(t, repo.lotesParams.AntesDe)
	require.Equal(t, time.Date(2026, time.October, 31, 10, 0, 0, 0, time.UTC), repo.lotesParams.AntesDe.UTC())
	require.Equal(t, "a1b2c3d4-5678-90ab-cdef-1234567890ab", repo.lotesParams.AntesDeID)
	require.NotNil(t, repo.lotesParams.Filtros.SectorID)
	require.Equal(t, "horno-1", *repo.lotesParams.Filtros.SectorID)
	require.NotNil(t, repo.lotesParams.Filtros.Turno)
	require.Equal(t, "tarde", *repo.lotesParams.Filtros.Turno)
	require.Equal(t, 5, pagina.Total)
	require.Equal(t, lotesectorSvc.EncodeCursor(ultimo.AbiertoEn, ultimo.ID), pagina.SiguienteCursor)
}

func TestTrazabilidad_LoteIDInvalido(t *testing.T) {
	casos := []string{"", "  ", "no-es-uuid", "a1b2c3d4"}
	for _, loteID := range casos {
		t.Run(loteID, func(t *testing.T) {
			repo := &fakeRepo{}
			_, err := servicioDePrueba(repo).Trazabilidad(context.Background(), Query{LoteID: loteID})
			require.ErrorIs(t, err, domain.ErrLoteIDInvalido)
			require.False(t, repo.llamadoTraz)
		})
	}
}

func TestTrazabilidad_ErrorDelRepo(t *testing.T) {
	repo := &fakeRepo{errTraz: domain.ErrLoteNoEncontrado}
	_, err := servicioDePrueba(repo).Trazabilidad(context.Background(),
		Query{LoteID: "a1b2c3d4-5678-90ab-cdef-1234567890ab"})
	require.ErrorIs(t, err, domain.ErrLoteNoEncontrado)
	require.True(t, repo.llamadoTraz)
}

func TestTrazabilidad_GeneradoEnConRelojDelServicio(t *testing.T) {
	repo := &fakeRepo{trazabilidad: &domain.Trazabilidad{}}
	traz, err := servicioDePrueba(repo).Trazabilidad(context.Background(),
		Query{LoteID: " a1b2c3d4-5678-90ab-cdef-1234567890ab "})
	require.NoError(t, err)
	require.True(t, repo.llamadoTraz)
	require.Equal(t, "a1b2c3d4-5678-90ab-cdef-1234567890ab", repo.loteID)
	require.Equal(t, time.Date(2026, time.November, 4, 18, 0, 0, 0, time.UTC), traz.GeneradoEn)
}

func TestAlertas_EstadoInvalido(t *testing.T) {
	repo := &fakeRepo{}
	_, err := servicioDePrueba(repo).Alertas(context.Background(), Query{Estado: "PENDIENTE"})
	require.ErrorIs(t, err, domain.ErrEstadoInvalido)
	require.False(t, repo.llamadoAlertas)
}

func TestAlertas_LimiteDefaultYMaximo(t *testing.T) {
	repo := &fakeRepo{}
	svc := servicioDePrueba(repo)
	ctx := context.Background()

	_, err := svc.Alertas(ctx, Query{})
	require.NoError(t, err)
	require.Equal(t, 1000, repo.alertasParams.Limite)

	_, err = svc.Alertas(ctx, Query{Limite: "9000"})
	require.NoError(t, err)
	require.Equal(t, 5000, repo.alertasParams.Limite)

	_, err = svc.Alertas(ctx, Query{Limite: "abc"})
	require.ErrorIs(t, err, domain.ErrLimiteInvalido)
}

func TestAlertas_FechasYFiltros(t *testing.T) {
	repo := &fakeRepo{}
	svc := servicioDePrueba(repo)

	_, err := svc.Alertas(context.Background(), Query{
		Desde:      "2026-10-01",
		Hasta:      "2026-10-31",
		SectorID:   "horno-1",
		ProductoID: "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		Turno:      "tarde",
		Estado:     "ACK",
	})
	require.NoError(t, err)
	require.True(t, repo.llamadoAlertas)
	require.Equal(t, time.Date(2026, time.October, 1, 0, 0, 0, 0, repo.alertasParams.Periodo.Desde.Location()), repo.alertasParams.Periodo.Desde)
	require.Equal(t, time.Date(2026, time.October, 31, 0, 0, 0, 0, repo.alertasParams.Periodo.Hasta.Location()), repo.alertasParams.Periodo.Hasta)
	require.Equal(t, "horno-1", *repo.alertasParams.Filtros.SectorID)
	require.NotNil(t, repo.alertasParams.Filtros.ProductoID)
	require.Equal(t, "tarde", *repo.alertasParams.Filtros.Turno)
	require.NotNil(t, repo.alertasParams.Estado)
	require.Equal(t, "ACK", *repo.alertasParams.Estado)

	// Default: últimos 30 días hasta hoy ART.
	_, err = svc.Alertas(context.Background(), Query{})
	require.NoError(t, err)
	require.Equal(t, "2026-10-06", repo.alertasParams.Periodo.Desde.Format("2006-01-02"))
	require.Equal(t, "2026-11-04", repo.alertasParams.Periodo.Hasta.Format("2006-01-02"))
	require.Nil(t, repo.alertasParams.Estado)
}

func TestAlertas_TruncadaYFechaInvalida(t *testing.T) {
	repo := &fakeRepo{
		alertas:      []domain.AlertaReporte{{ID: "a"}, {ID: "b"}},
		totalAlertas: 5,
	}
	pagina, err := servicioDePrueba(repo).Alertas(context.Background(), Query{Limite: "2"})
	require.NoError(t, err)
	require.Equal(t, 5, pagina.Total)
	require.True(t, pagina.Truncada)
	require.Len(t, pagina.Alertas, 2)

	_, err = servicioDePrueba(&fakeRepo{}).Alertas(context.Background(), Query{Desde: "01/10/2026"})
	require.ErrorIs(t, err, domain.ErrFechaInvalida)
}
