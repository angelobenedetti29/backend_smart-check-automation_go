package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// --- Fakes -----------------------------------------------------------------

type fakeLoteRepo struct {
	abrir     func(context.Context, lotesector.AbrirParams) (*lotesector.Lote, bool, error)
	registrar func(context.Context, string, string, string, []lotesector.Evento) (int, int, *lotesector.Lote, error)
	cerrar    func(context.Context, lotesector.CerrarParams) (*lotesector.Lote, bool, error)
	abierto   func(context.Context, string) (*lotesector.Lote, error)
	getByID   func(context.Context, string) (*lotesector.Lote, error)
	historial func(context.Context, lotesector.HistorialParams) ([]lotesector.Lote, int, error)

	historialParams lotesector.HistorialParams
}

func (f *fakeLoteRepo) GetAbiertoBySector(ctx context.Context, sectorID string) (*lotesector.Lote, error) {
	if f.abierto == nil {
		return nil, nil
	}
	return f.abierto(ctx, sectorID)
}

func (f *fakeLoteRepo) GetByID(ctx context.Context, loteID string) (*lotesector.Lote, error) {
	if f.getByID == nil {
		return nil, lotesector.ErrNotFound
	}
	return f.getByID(ctx, loteID)
}

func (f *fakeLoteRepo) Abrir(ctx context.Context, params lotesector.AbrirParams) (*lotesector.Lote, bool, error) {
	if f.abrir == nil {
		return nil, false, nil
	}
	return f.abrir(ctx, params)
}

func (f *fakeLoteRepo) RegistrarEventos(ctx context.Context, loteID, sectorID, deviceID string, eventos []lotesector.Evento) (int, int, *lotesector.Lote, error) {
	if f.registrar == nil {
		return 0, 0, nil, nil
	}
	return f.registrar(ctx, loteID, sectorID, deviceID, eventos)
}

func (f *fakeLoteRepo) Cerrar(ctx context.Context, params lotesector.CerrarParams) (*lotesector.Lote, bool, error) {
	if f.cerrar == nil {
		return nil, false, nil
	}
	return f.cerrar(ctx, params)
}

func (f *fakeLoteRepo) Historial(ctx context.Context, params lotesector.HistorialParams) ([]lotesector.Lote, int, error) {
	f.historialParams = params
	if f.historial == nil {
		return []lotesector.Lote{}, 0, nil
	}
	return f.historial(ctx, params)
}

type fakeSectorRepo struct {
	deviceInfo    *sector.DeviceInfo
	deviceErr     error
	sectorByID    *sector.Sector
	sectorErr     error
	companeros    []sector.Companero
	companerosErr error
	sectores      []sector.Sector
	sectoresErr   error
}

func (f *fakeSectorRepo) GetDeviceInfo(context.Context, string) (*sector.DeviceInfo, error) {
	return f.deviceInfo, f.deviceErr
}

func (f *fakeSectorRepo) GetByID(context.Context, string) (*sector.Sector, error) {
	return f.sectorByID, f.sectorErr
}

func (f *fakeSectorRepo) ListCompaneros(context.Context, string, string) ([]sector.Companero, error) {
	return f.companeros, f.companerosErr
}

func (f *fakeSectorRepo) List(context.Context) ([]sector.Sector, error) {
	return f.sectores, f.sectoresErr
}

func (f *fakeSectorRepo) Create(context.Context, *sector.Sector) error { return nil }

func (f *fakeSectorRepo) Update(context.Context, *sector.Sector) error { return nil }

func (f *fakeSectorRepo) Delete(context.Context, string) error { return nil }

type fakeProductoRepo struct {
	getByID func(context.Context, string) (*producto.Producto, error)
	list    []producto.Producto
	listErr error
}

func (f *fakeProductoRepo) List(context.Context) ([]producto.Producto, error) {
	return f.list, f.listErr
}

func (f *fakeProductoRepo) GetByID(ctx context.Context, id string) (*producto.Producto, error) {
	if f.getByID == nil {
		return nil, producto.ErrDesconocido
	}
	return f.getByID(ctx, id)
}

// --- Helpers ---------------------------------------------------------------

func sectorConDispositivo() *fakeSectorRepo {
	sid := "sector-1"
	return &fakeSectorRepo{
		deviceInfo: &sector.DeviceInfo{DeviceID: "dev-1", Hostname: "pi-entrada", Tipo: "ENTRADA_HORNO", SectorID: &sid},
		sectorByID: &sector.Sector{ID: "sector-1", Nombre: "Horno 1"},
		companeros: []sector.Companero{{DeviceID: "dev-2", Hostname: "pi-salida", Tipo: "SALIDA_HORNO"}},
	}
}

func productoExistente() *fakeProductoRepo {
	return &fakeProductoRepo{
		getByID: func(context.Context, string) (*producto.Producto, error) {
			return &producto.Producto{ID: "prod-1", Nombre: "Tostada", Activo: true}, nil
		},
	}
}

func newTestService(loteRepo *fakeLoteRepo, sectorRepo *fakeSectorRepo, productoRepo *fakeProductoRepo, minInact time.Duration) (*Service, *sse.Client) {
	broker := sse.NewBroker()
	client := broker.Subscribe()
	svc := NewService(loteRepo, sectorRepo, productoRepo, broker, minInact)
	return svc, client
}

func receiveEvent(t *testing.T, client *sse.Client) sse.SSEEvent {
	t.Helper()
	select {
	case ev := <-client.Events:
		return ev
	case <-time.After(time.Second):
		t.Fatal("no se recibió ningún evento SSE")
		return sse.SSEEvent{}
	}
}

func assertNoEvent(t *testing.T, client *sse.Client) {
	t.Helper()
	select {
	case ev := <-client.Events:
		t.Fatalf("evento SSE inesperado: %s", ev.EventType)
	case <-time.After(50 * time.Millisecond):
	}
}

func eventoValido(id string) lotesector.Evento {
	return lotesector.Evento{EventoID: id, ProductoID: testProductoUUID}
}

const testUUID = "c1a2b3c4-d5e6-4789-8abc-def012345678"
const testProductoUUID = "a1b2c3d4-5678-490a-8def-1234567890ab"

// --- Abrir -----------------------------------------------------------------

func TestAbrirEmiteLoteCreadoCuandoCrea(t *testing.T) {
	now := time.Now().UTC()
	lote := &lotesector.Lote{ID: "lote-1", SectorID: "sector-1", Estado: lotesector.EstadoAbierto, AbiertoEn: now}
	repo := &fakeLoteRepo{abrir: func(_ context.Context, p lotesector.AbrirParams) (*lotesector.Lote, bool, error) {
		require.Equal(t, "sector-1", p.SectorID)
		require.Equal(t, "prod-1", p.ProductoID)
		require.Equal(t, "dev-1", p.AbiertoPor)
		require.Equal(t, "key-1", p.IdempotencyKey)
		require.NotEmpty(t, p.Turno)
		return lote, true, nil
	}}
	svc, client := newTestService(repo, sectorConDispositivo(), productoExistente(), 0)

	got, creado, err := svc.Abrir(context.Background(), "dev-1", "prod-1", "key-1")
	require.NoError(t, err)
	require.True(t, creado)
	require.Same(t, lote, got)

	ev := receiveEvent(t, client)
	assert.Equal(t, "lote.creado", ev.EventType)
	var payload lotesector.Lote
	require.NoError(t, json.Unmarshal(ev.Data, &payload))
	assert.Equal(t, "lote-1", payload.ID)
}

func TestAbrirNoEmiteCuandoAdjuntaAExistente(t *testing.T) {
	repo := &fakeLoteRepo{abrir: func(context.Context, lotesector.AbrirParams) (*lotesector.Lote, bool, error) {
		return &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoAbierto}, false, nil
	}}
	svc, client := newTestService(repo, sectorConDispositivo(), productoExistente(), 0)

	_, creado, err := svc.Abrir(context.Background(), "dev-1", "prod-1", "key-1")
	require.NoError(t, err)
	assert.False(t, creado)
	assertNoEvent(t, client)
}

func TestAbrirProductoDesconocido(t *testing.T) {
	repo := &fakeLoteRepo{}
	prodRepo := &fakeProductoRepo{getByID: func(context.Context, string) (*producto.Producto, error) {
		return nil, producto.ErrDesconocido
	}}
	svc, _ := newTestService(repo, sectorConDispositivo(), prodRepo, 0)

	_, _, err := svc.Abrir(context.Background(), "dev-1", "no-existe", "key-1")
	assert.ErrorIs(t, err, producto.ErrDesconocido)
}

func TestAbrirSinSector(t *testing.T) {
	repo := &fakeLoteRepo{}
	secRepo := &fakeSectorRepo{deviceInfo: &sector.DeviceInfo{DeviceID: "dev-1"}}
	svc, _ := newTestService(repo, secRepo, productoExistente(), 0)

	_, _, err := svc.Abrir(context.Background(), "dev-1", "prod-1", "key-1")
	assert.ErrorIs(t, err, sector.ErrSinSector)
}

// --- RegistrarEventos ------------------------------------------------------

func TestRegistrarEventosEmiteActualizado(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoAbierto}
	repo := &fakeLoteRepo{registrar: func(_ context.Context, loteID, sectorID, deviceID string, eventos []lotesector.Evento) (int, int, *lotesector.Lote, error) {
		require.Equal(t, "lote-1", loteID)
		require.Equal(t, "sector-1", sectorID)
		require.Equal(t, "dev-1", deviceID)
		require.Len(t, eventos, 1)
		return 1, 0, lote, nil
	}}
	svc, client := newTestService(repo, sectorConDispositivo(), productoExistente(), 0)

	aceptados, duplicados, got, err := svc.RegistrarEventos(context.Background(), "dev-1", "lote-1", []lotesector.Evento{eventoValido(testUUID)})
	require.NoError(t, err)
	assert.Equal(t, 1, aceptados)
	assert.Equal(t, 0, duplicados)
	require.Same(t, lote, got)

	ev := receiveEvent(t, client)
	assert.Equal(t, "lote.actualizado", ev.EventType)
}

func TestRegistrarEventosBatchVacioEsPayloadInvalido(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)

	_, _, _, err := svc.RegistrarEventos(context.Background(), "dev-1", "lote-1", nil)
	assert.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
}

func TestRegistrarEventosDemasiados(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)

	demasiados := make([]lotesector.Evento, maxEventosPorBatch+1)
	for i := range demasiados {
		demasiados[i] = eventoValido(testUUID)
	}
	_, _, _, err := svc.RegistrarEventos(context.Background(), "dev-1", "lote-1", demasiados)
	assert.ErrorIs(t, err, lotesector.ErrDemasiadosEventos)
}

func TestRegistrarEventosEventoIDInvalido(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)

	_, _, _, err := svc.RegistrarEventos(context.Background(), "dev-1", "lote-1", []lotesector.Evento{eventoValido("no-es-uuid")})
	assert.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
}

// M2: un estado fuera del dominio no debe llegar al CHECK de la base (23514).
func TestRegistrarEventosEstadoInvalido(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)

	estado := "carbonizado"
	ev := eventoValido(testUUID)
	ev.Estado = &estado
	_, _, _, err := svc.RegistrarEventos(context.Background(), "dev-1", "lote-1", []lotesector.Evento{ev})
	assert.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
}

// M2: confianza fuera de [0,1] no debe llegar al CHECK de la base.
func TestRegistrarEventosConfianzaFueraDeRango(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)

	for _, c := range []float64{-0.01, 1.01} {
		confianza := c
		ev := eventoValido(testUUID)
		ev.Confianza = &confianza
		_, _, _, err := svc.RegistrarEventos(context.Background(), "dev-1", "lote-1", []lotesector.Evento{ev})
		assert.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
	}
}

// M2: producto_id no-UUID no debe llegar a la columna uuid (22P02).
func TestRegistrarEventosProductoIDInvalido(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)

	ev := eventoValido(testUUID)
	ev.ProductoID = "prod-1"
	_, _, _, err := svc.RegistrarEventos(context.Background(), "dev-1", "lote-1", []lotesector.Evento{ev})
	assert.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
}

// m6: un batch sólo-duplicados no debe emitir lote.actualizado.
func TestRegistrarEventosSoloDuplicadosNoEmite(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoAbierto}
	repo := &fakeLoteRepo{registrar: func(context.Context, string, string, string, []lotesector.Evento) (int, int, *lotesector.Lote, error) {
		return 0, 1, lote, nil
	}}
	svc, client := newTestService(repo, sectorConDispositivo(), productoExistente(), 0)

	aceptados, duplicados, _, err := svc.RegistrarEventos(context.Background(), "dev-1", "lote-1", []lotesector.Evento{eventoValido(testUUID)})
	require.NoError(t, err)
	assert.Equal(t, 0, aceptados)
	assert.Equal(t, 1, duplicados)
	assertNoEvent(t, client)
}

// --- Cerrar ----------------------------------------------------------------

func conteosValidos() lotesector.Conteos {
	ok := 3
	quemado := 1
	return lotesector.Conteos{OK: &ok, Quemado: &quemado, Total: 4}
}

func TestCerrarEmiteCerradoSoloEnTransicion(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoCerrado}
	repo := &fakeLoteRepo{cerrar: func(context.Context, lotesector.CerrarParams) (*lotesector.Lote, bool, error) {
		return lote, false, nil
	}}
	svc, client := newTestService(repo, sectorConDispositivo(), productoExistente(), 0)

	_, err := svc.Cerrar(context.Background(), "dev-1", "lote-1", "manual", conteosValidos(), "key-1")
	require.NoError(t, err)
	ev := receiveEvent(t, client)
	assert.Equal(t, "lote.cerrado", ev.EventType)
}

func TestCerrarNoEmiteEnReintentoIdempotente(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoCerrado}
	repo := &fakeLoteRepo{cerrar: func(context.Context, lotesector.CerrarParams) (*lotesector.Lote, bool, error) {
		return lote, true, nil
	}}
	svc, client := newTestService(repo, sectorConDispositivo(), productoExistente(), 0)

	_, err := svc.Cerrar(context.Background(), "dev-1", "lote-1", "manual", conteosValidos(), "key-1")
	require.NoError(t, err)
	assertNoEvent(t, client)
}

// M1(a): con el gate activo, un reintento sobre un lote CERRADO debe ser 200
// idempotente, nunca 409 (el gate de inactividad no aplica a CERRADO).
func TestCerrarLoteCerradoConGateActivoNoEs409(t *testing.T) {
	now := time.Now().UTC()
	ultimo := now.Add(-5 * time.Second) // sector "activo" según el umbral
	repo := &fakeLoteRepo{
		getByID: func(context.Context, string) (*lotesector.Lote, error) {
			return &lotesector.Lote{
				ID: "lote-1", SectorID: "sector-1", Estado: lotesector.EstadoCerrado,
				AbiertoEn: now.Add(-time.Minute), UltimoEventoEn: &ultimo,
			}, nil
		},
		cerrar: func(context.Context, lotesector.CerrarParams) (*lotesector.Lote, bool, error) {
			return &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoCerrado}, true, nil
		},
	}
	svc, client := newTestService(repo, sectorConDispositivo(), productoExistente(), 30*time.Second)
	svc.now = func() time.Time { return now }

	_, err := svc.Cerrar(context.Background(), "dev-1", "lote-1", "manual", conteosValidos(), "key-1")
	require.NoError(t, err)
	assertNoEvent(t, client)
}

// M1(b): con el gate activo, un lote de otro sector es 403 antes que 409.
func TestCerrarLoteAjenoConGateActivoEsAjeno(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeLoteRepo{
		getByID: func(context.Context, string) (*lotesector.Lote, error) {
			return &lotesector.Lote{ID: "lote-1", SectorID: "otro-sector", Estado: lotesector.EstadoAbierto, AbiertoEn: now}, nil
		},
	}
	svc, _ := newTestService(repo, sectorConDispositivo(), productoExistente(), 30*time.Second)
	svc.now = func() time.Time { return now }

	_, err := svc.Cerrar(context.Background(), "dev-1", "lote-1", "manual", conteosValidos(), "key-1")
	assert.ErrorIs(t, err, lotesector.ErrAjeno)
}

// M2: un bucket negativo no debe llegar al CHECK de la base (23514 → 500).
func TestCerrarBucketNegativo(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)
	ok := -1
	conteos := lotesector.Conteos{OK: &ok, Total: -1}
	_, err := svc.Cerrar(context.Background(), "dev-1", "lote-1", "manual", conteos, "key-1")
	assert.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
}

func TestCerrarMotivoInvalido(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)
	_, err := svc.Cerrar(context.Background(), "dev-1", "lote-1", "explotó", conteosValidos(), "key-1")
	assert.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
}

func TestCerrarTotalInconsistente(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)
	ok := 3
	conteos := lotesector.Conteos{OK: &ok, Total: 99}
	_, err := svc.Cerrar(context.Background(), "dev-1", "lote-1", "manual", conteos, "key-1")
	assert.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
}

func TestCerrarSectorActivoPorInactividad(t *testing.T) {
	now := time.Now().UTC()
	ultimo := now.Add(-5 * time.Second)
	repo := &fakeLoteRepo{
		getByID: func(context.Context, string) (*lotesector.Lote, error) {
			return &lotesector.Lote{ID: "lote-1", SectorID: "sector-1", Estado: lotesector.EstadoAbierto, AbiertoEn: now.Add(-time.Minute), UltimoEventoEn: &ultimo}, nil
		},
		cerrar: func(context.Context, lotesector.CerrarParams) (*lotesector.Lote, bool, error) {
			return &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoCerrado}, false, nil
		},
	}
	svc, _ := newTestService(repo, sectorConDispositivo(), productoExistente(), 30*time.Second)
	svc.now = func() time.Time { return now }

	_, err := svc.Cerrar(context.Background(), "dev-1", "lote-1", "manual", conteosValidos(), "key-1")
	assert.ErrorIs(t, err, lotesector.ErrSectorActivo)
}

func TestCerrarPermiteCuandoInactividadSuperaUmbral(t *testing.T) {
	now := time.Now().UTC()
	ultimo := now.Add(-60 * time.Second)
	repo := &fakeLoteRepo{
		getByID: func(context.Context, string) (*lotesector.Lote, error) {
			return &lotesector.Lote{ID: "lote-1", SectorID: "sector-1", Estado: lotesector.EstadoAbierto, AbiertoEn: now.Add(-2 * time.Minute), UltimoEventoEn: &ultimo}, nil
		},
		cerrar: func(context.Context, lotesector.CerrarParams) (*lotesector.Lote, bool, error) {
			return &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoCerrado}, false, nil
		},
	}
	svc, client := newTestService(repo, sectorConDispositivo(), productoExistente(), 30*time.Second)
	svc.now = func() time.Time { return now }

	_, err := svc.Cerrar(context.Background(), "dev-1", "lote-1", "manual", conteosValidos(), "key-1")
	require.NoError(t, err)
	ev := receiveEvent(t, client)
	assert.Equal(t, "lote.cerrado", ev.EventType)
}

// --- Inactividad -----------------------------------------------------------

func TestInactividadUsaUltimoEvento(t *testing.T) {
	now := time.Now().UTC()
	ultimo := now.Add(-12500 * time.Millisecond)
	repo := &fakeLoteRepo{abierto: func(context.Context, string) (*lotesector.Lote, error) {
		return &lotesector.Lote{ID: "lote-1", AbiertoEn: now.Add(-time.Hour), UltimoEventoEn: &ultimo}, nil
	}}
	svc, _ := newTestService(repo, sectorConDispositivo(), productoExistente(), 0)
	svc.now = func() time.Time { return now }

	lote, err := svc.Abierto(context.Background(), "sector-1")
	require.NoError(t, err)
	assert.InDelta(t, 12.5, lote.InactividadSegundos, 0.001)
}

func TestInactividadCaeAAbiertoEnYNoEsNegativa(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeLoteRepo{abierto: func(context.Context, string) (*lotesector.Lote, error) {
		// abierto_en en el futuro → clamp a 0.
		return &lotesector.Lote{ID: "lote-1", AbiertoEn: now.Add(time.Hour)}, nil
	}}
	svc, _ := newTestService(repo, sectorConDispositivo(), productoExistente(), 0)
	svc.now = func() time.Time { return now }

	lote, err := svc.Abierto(context.Background(), "sector-1")
	require.NoError(t, err)
	assert.Equal(t, 0.0, lote.InactividadSegundos)
}

func TestAbiertoSinLoteDevuelveNil(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)
	lote, err := svc.Abierto(context.Background(), "sector-1")
	require.NoError(t, err)
	assert.Nil(t, lote)
}

// --- Historial -------------------------------------------------------------

func TestHistorialCursorMalformado(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)
	_, _, err := svc.Historial(context.Background(), "sector-1", "", 20, "%%%no-base64%%%")
	assert.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
}

// M3: un cursor forjado con id no-UUID no debe llegar a compararse contra la
// columna uuid (22P02 → 500).
func TestHistorialCursorIDNoUUID(t *testing.T) {
	forged := EncodeCursor(time.Now().UTC(), "lote-forjado")
	_, _, err := DecodeCursor(forged)
	require.Error(t, err)

	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)
	_, _, err = svc.Historial(context.Background(), "sector-1", "", 20, forged)
	assert.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
}

func TestHistorialCursorValidoDecodifica(t *testing.T) {
	repo := &fakeLoteRepo{}
	svc, _ := newTestService(repo, sectorConDispositivo(), productoExistente(), 0)

	inicio := time.Now().UTC().Truncate(time.Microsecond)
	cursor := EncodeCursor(inicio, testUUID)
	_, _, err := svc.Historial(context.Background(), "sector-1", "prod-1", 20, cursor)
	require.NoError(t, err)

	require.NotNil(t, repo.historialParams.AntesDe)
	assert.True(t, repo.historialParams.AntesDe.Equal(inicio))
	assert.Equal(t, testUUID, repo.historialParams.AntesDeID)
	assert.Equal(t, 20, repo.historialParams.Limite)
	assert.Equal(t, "prod-1", repo.historialParams.ProductoID)
}

func TestHistorialLimiteSeAcota(t *testing.T) {
	repo := &fakeLoteRepo{}
	svc, _ := newTestService(repo, sectorConDispositivo(), productoExistente(), 0)

	_, _, err := svc.Historial(context.Background(), "sector-1", "", 500, "")
	require.NoError(t, err)
	assert.Equal(t, maxHistorialLimite, repo.historialParams.Limite)

	_, _, err = svc.Historial(context.Background(), "sector-1", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, defaultHistorialLimite, repo.historialParams.Limite)
}

// --- SectorDelDispositivo --------------------------------------------------

func TestSectorDelDispositivo(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, sectorConDispositivo(), productoExistente(), 0)
	info, err := svc.SectorDelDispositivo(context.Background(), "dev-1")
	require.NoError(t, err)
	assert.Equal(t, "sector-1", info.SectorID)
	assert.Equal(t, "Horno 1", info.Nombre)
	assert.Equal(t, "ENTRADA_HORNO", info.Tipo)
	require.Len(t, info.Companeros, 1)
}

func TestSectorDelDispositivoSinSector(t *testing.T) {
	svc, _ := newTestService(&fakeLoteRepo{}, &fakeSectorRepo{deviceErr: sector.ErrSinSector}, productoExistente(), 0)
	_, err := svc.SectorDelDispositivo(context.Background(), "dev-1")
	assert.True(t, errors.Is(err, sector.ErrSinSector))
}
