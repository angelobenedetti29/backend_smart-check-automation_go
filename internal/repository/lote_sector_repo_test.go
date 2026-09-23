package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
)

const (
	testProductoID   = "aaaaaaaa-0000-0000-0000-000000000001"
	testSectorID     = "sector-test-1"
	testEntradaDevID = "bbbbbbbb-0000-0000-0000-000000000001"
	testSalidaDevID  = "bbbbbbbb-0000-0000-0000-000000000002"
)

// ptr devuelve un puntero a una copia del valor dado; facilita construir
// campos opcionales (*string, *float64, ...) en los fixtures de prueba.
func ptr[T any](v T) *T { return &v }

// setupLoteSectorRepo provisiona una base aislada con un producto, un sector y
// dos dispositivos (ENTRADA_HORNO/SALIDA_HORNO) del mismo sector. Cada sentencia
// se ejecuta en su propio Exec: pgx usa el protocolo extendido, que no admite
// múltiples comandos en un único statement preparado.
func setupLoteSectorRepo(t *testing.T) (*LoteSectorPostgresRepository, *pgxpool.Pool) {
	t.Helper()
	pool := repositoryTestPool(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`INSERT INTO productos (id, nombre, activo) VALUES ($1, 'Tostada Test', true)`, testProductoID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO sectores (id, nombre) VALUES ($1, 'Sector Test')`, testSectorID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO dispositivos (id, nombre, tipo, sector_id) VALUES
			($1, 'pi-entrada', 'ENTRADA_HORNO', $3),
			($2, 'pi-salida', 'SALIDA_HORNO', $3)`,
		testEntradaDevID, testSalidaDevID, testSectorID)
	require.NoError(t, err)
	return NewLoteSectorPostgresRepository(pool), pool
}

// abrirLote abre el lote del sector de prueba y exige que sea una creación.
func abrirLote(t *testing.T, ctx context.Context, repo *LoteSectorPostgresRepository) *lotesector.Lote {
	t.Helper()
	l, creado, err := repo.Abrir(ctx, lotesector.AbrirParams{
		SectorID:   testSectorID,
		ProductoID: testProductoID,
		AbiertoPor: testEntradaDevID,
	})
	require.NoError(t, err)
	require.True(t, creado, "la primera apertura debe crear el lote")
	return l
}

// TestBuildConteosLote valida el mapeo puro de buckets nulos sin tocar la DB.
func TestBuildConteosLote(t *testing.T) {
	ok, crudo := 3, 2
	c := buildConteosLote(&ok, nil, &crudo)
	require.NotNil(t, c.OK)
	require.Nil(t, c.Crudo)
	require.NotNil(t, c.Quemado)
	require.Equal(t, 5, c.Total)

	empty := buildConteosLote(nil, nil, nil)
	require.Nil(t, empty.OK)
	require.Nil(t, empty.Crudo)
	require.Nil(t, empty.Quemado)
	require.Equal(t, 0, empty.Total)
}

// TestLoteSectorRepository_AbrirConcurrente verifica que dos aperturas
// concurrentes del mismo sector resuelvan un único lote ABIERTO.
func TestLoteSectorRepository_AbrirConcurrente(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupLoteSectorRepo(t)

	const workers = 2
	type result struct {
		lote   *lotesector.Lote
		creado bool
		err    error
	}
	results := make(chan result, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			l, creado, err := repo.Abrir(ctx, lotesector.AbrirParams{
				SectorID:   testSectorID,
				ProductoID: testProductoID,
				AbiertoPor: testEntradaDevID,
			})
			results <- result{lote: l, creado: creado, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	creados := 0
	ids := map[string]bool{}
	for r := range results {
		require.NoError(t, r.err)
		require.NotNil(t, r.lote)
		if r.creado {
			creados++
		}
		ids[r.lote.ID] = true
	}
	require.Equal(t, 1, creados, "sólo una apertura debe crear el lote")
	require.Len(t, ids, 1, "ambas llamadas deben resolver el mismo lote")

	var abiertos int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM lotes_productivos WHERE sector_id=$1 AND estado='ABIERTO'`, testSectorID).Scan(&abiertos))
	require.Equal(t, 1, abiertos)
}

// TestLoteSectorRepository_AbrirIdempotente verifica que la misma clave de
// apertura devuelva el mismo lote, incluso ya cerrado, sin crear uno nuevo.
func TestLoteSectorRepository_AbrirIdempotente(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupLoteSectorRepo(t)

	params := lotesector.AbrirParams{
		SectorID:       testSectorID,
		ProductoID:     testProductoID,
		AbiertoPor:     testEntradaDevID,
		IdempotencyKey: "key-abrir-1",
	}
	first, creado, err := repo.Abrir(ctx, params)
	require.NoError(t, err)
	require.True(t, creado)

	// Segunda llamada con el lote todavía abierto: attach, no error.
	second, creado, err := repo.Abrir(ctx, params)
	require.NoError(t, err)
	require.False(t, creado)
	require.Equal(t, first.ID, second.ID)

	// Cerrar y reintentar con la misma clave: devuelve el mismo lote cerrado.
	_, _, err = repo.Cerrar(ctx, lotesector.CerrarParams{
		LoteID:   first.ID,
		SectorID: testSectorID,
		Conteos:  lotesector.Conteos{Total: 0},
		Motivo:   "manual",
	})
	require.NoError(t, err)

	again, creado, err := repo.Abrir(ctx, params)
	require.NoError(t, err)
	require.False(t, creado)
	require.Equal(t, first.ID, again.ID)
	require.Equal(t, lotesector.EstadoCerrado, again.Estado)
}

// TestLoteSectorRepository_RegistrarEventosDuplicados verifica que la
// deduplicación por evento_id no duplique los conteos.
func TestLoteSectorRepository_RegistrarEventosDuplicados(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupLoteSectorRepo(t)
	lote := abrirLote(t, ctx, repo)

	eventos := []lotesector.Evento{
		{EventoID: "cccccccc-0000-0000-0000-000000000001", ProductoID: testProductoID, Estado: ptr("ok")},
		{EventoID: "cccccccc-0000-0000-0000-000000000002", ProductoID: testProductoID, Estado: ptr("quemado")},
	}

	aceptados, duplicados, updated, err := repo.RegistrarEventos(ctx, lote.ID, testSectorID, testSalidaDevID, eventos)
	require.NoError(t, err)
	require.Equal(t, 2, aceptados)
	require.Equal(t, 0, duplicados)
	require.NotNil(t, updated.Conteos.OK)
	require.Equal(t, 1, *updated.Conteos.OK)
	require.NotNil(t, updated.Conteos.Quemado)
	require.Equal(t, 1, *updated.Conteos.Quemado)
	require.Equal(t, 2, updated.Conteos.Total)

	// Reintento del mismo batch: todo duplicado, sin doble conteo.
	aceptados, duplicados, updated, err = repo.RegistrarEventos(ctx, lote.ID, testSectorID, testSalidaDevID, eventos)
	require.NoError(t, err)
	require.Equal(t, 0, aceptados)
	require.Equal(t, 2, duplicados)
	require.NotNil(t, updated.Conteos.OK)
	require.Equal(t, 1, *updated.Conteos.OK)
	require.Equal(t, 2, updated.Conteos.Total)
}

// TestLoteSectorRepository_RegistrarEventosDuplicadoNoExtiendeVentana verifica
// que un batch que sólo trae duplicados no mueva ultimo_evento_en: la ventana de
// inactividad del sector no debe extenderse por un reintento sin eventos nuevos.
func TestLoteSectorRepository_RegistrarEventosDuplicadoNoExtiendeVentana(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupLoteSectorRepo(t)
	lote := abrirLote(t, ctx, repo)

	eventos := []lotesector.Evento{
		{EventoID: "13131313-0000-0000-0000-000000000001", ProductoID: testProductoID, Estado: ptr("ok")},
	}

	aceptados, duplicados, first, err := repo.RegistrarEventos(ctx, lote.ID, testSectorID, testSalidaDevID, eventos)
	require.NoError(t, err)
	require.Equal(t, 1, aceptados)
	require.Equal(t, 0, duplicados)
	require.NotNil(t, first.UltimoEventoEn)

	// Espaciar para que un now() nuevo sería distinguible del persistido.
	time.Sleep(20 * time.Millisecond)

	aceptados, duplicados, again, err := repo.RegistrarEventos(ctx, lote.ID, testSectorID, testSalidaDevID, eventos)
	require.NoError(t, err)
	require.Equal(t, 0, aceptados)
	require.Equal(t, 1, duplicados)
	require.NotNil(t, again.UltimoEventoEn)
	require.True(t, first.UltimoEventoEn.Equal(*again.UltimoEventoEn),
		"un batch sólo-duplicado no debe extender ultimo_evento_en")
}

// TestLoteSectorRepository_RegistrarEventosSinEstadoExtiendeVentana verifica
// que un batch aceptado cuyos eventos tienen estado NULL (no suman a ningún
// bucket) igualmente extienda ultimo_evento_en, mientras que un reintento
// sólo-duplicado no la mueva.
func TestLoteSectorRepository_RegistrarEventosSinEstadoExtiendeVentana(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupLoteSectorRepo(t)
	lote := abrirLote(t, ctx, repo)

	eventos := []lotesector.Evento{
		{EventoID: "15151515-0000-0000-0000-000000000001", ProductoID: testProductoID, Estado: nil},
	}

	aceptados, duplicados, first, err := repo.RegistrarEventos(ctx, lote.ID, testSectorID, testSalidaDevID, eventos)
	require.NoError(t, err)
	require.Equal(t, 1, aceptados)
	require.Equal(t, 0, duplicados)
	require.NotNil(t, first.UltimoEventoEn, "un evento aceptado sin estado debe extender la ventana")
	require.Nil(t, first.Conteos.OK)
	require.Nil(t, first.Conteos.Crudo)
	require.Nil(t, first.Conteos.Quemado)
	require.Equal(t, 0, first.Conteos.Total)

	time.Sleep(20 * time.Millisecond)

	aceptados, duplicados, again, err := repo.RegistrarEventos(ctx, lote.ID, testSectorID, testSalidaDevID, eventos)
	require.NoError(t, err)
	require.Equal(t, 0, aceptados)
	require.Equal(t, 1, duplicados)
	require.NotNil(t, again.UltimoEventoEn)
	require.True(t, first.UltimoEventoEn.Equal(*again.UltimoEventoEn),
		"un batch sólo-duplicado no debe extender ultimo_evento_en")
}

// TestLoteSectorRepository_AbrirClaveDeOtroSector verifica que reutilizar una
// clave de idempotencia de apertura en otro sector devuelva el centinela de
// conflicto en vez de un error opaco (o del lote ajeno).
func TestLoteSectorRepository_AbrirClaveDeOtroSector(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupLoteSectorRepo(t)

	const otroSectorID = "sector-test-2"
	_, err := pool.Exec(ctx, `INSERT INTO sectores (id, nombre) VALUES ($1, 'Sector Test 2')`, otroSectorID)
	require.NoError(t, err)

	const key = "key-compartida"
	_, creado, err := repo.Abrir(ctx, lotesector.AbrirParams{
		SectorID:       testSectorID,
		ProductoID:     testProductoID,
		AbiertoPor:     testEntradaDevID,
		IdempotencyKey: key,
	})
	require.NoError(t, err)
	require.True(t, creado)

	_, _, err = repo.Abrir(ctx, lotesector.AbrirParams{
		SectorID:       otroSectorID,
		ProductoID:     testProductoID,
		AbiertoPor:     testEntradaDevID,
		IdempotencyKey: key,
	})
	require.ErrorIs(t, err, lotesector.ErrIdempotencyKeyConflicto)
}

// TestLoteSectorRepository_RegistrarEventosPersisteDispositivo verifica que la
// procedencia del reporte quede en eventos_lote.dispositivo_id (el dispositivo
// que reporta), y NULL cuando el deviceID llega vacío.
func TestLoteSectorRepository_RegistrarEventosPersisteDispositivo(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupLoteSectorRepo(t)
	lote := abrirLote(t, ctx, repo)

	conDevice := "14141414-0000-0000-0000-000000000001"
	_, _, _, err := repo.RegistrarEventos(ctx, lote.ID, testSectorID, testSalidaDevID, []lotesector.Evento{
		{EventoID: conDevice, ProductoID: testProductoID, Estado: ptr("ok")},
	})
	require.NoError(t, err)

	var deviceID *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT dispositivo_id FROM eventos_lote WHERE evento_id=$1`, conDevice).Scan(&deviceID))
	require.NotNil(t, deviceID)
	require.Equal(t, testSalidaDevID, *deviceID)

	sinDevice := "14141414-0000-0000-0000-000000000002"
	_, _, _, err = repo.RegistrarEventos(ctx, lote.ID, testSectorID, "", []lotesector.Evento{
		{EventoID: sinDevice, ProductoID: testProductoID, Estado: ptr("ok")},
	})
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT dispositivo_id FROM eventos_lote WHERE evento_id=$1`, sinDevice).Scan(&deviceID))
	require.Nil(t, deviceID, "deviceID vacío debe persistir NULL")
}

// TestLoteSectorRepository_RegistrarEventosProductoInconsistente verifica que
// un batch con producto ajeno se rechace completo sin aplicar eventos.
func TestLoteSectorRepository_RegistrarEventosProductoInconsistente(t *testing.T) {
	ctx := context.Background()
	repo, pool := setupLoteSectorRepo(t)
	lote := abrirLote(t, ctx, repo)

	_, _, _, err := repo.RegistrarEventos(ctx, lote.ID, testSectorID, testSalidaDevID, []lotesector.Evento{
		{EventoID: "dddddddd-0000-0000-0000-000000000001", ProductoID: testProductoID, Estado: ptr("ok")},
		{EventoID: "dddddddd-0000-0000-0000-000000000002", ProductoID: "aaaaaaaa-0000-0000-0000-0000000000ff", Estado: ptr("ok")},
	})
	require.ErrorIs(t, err, lotesector.ErrProductoInconsistente)

	var count int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM eventos_lote WHERE lote_id=$1`, lote.ID).Scan(&count))
	require.Equal(t, 0, count, "no debe haber aplicación parcial")
}

// TestLoteSectorRepository_RegistrarEventosCerrado verifica el rechazo de
// eventos tardíos sobre un lote ya cerrado.
func TestLoteSectorRepository_RegistrarEventosCerrado(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupLoteSectorRepo(t)
	lote := abrirLote(t, ctx, repo)

	_, _, err := repo.Cerrar(ctx, lotesector.CerrarParams{
		LoteID:   lote.ID,
		SectorID: testSectorID,
		Conteos:  lotesector.Conteos{Total: 0},
		Motivo:   "manual",
	})
	require.NoError(t, err)

	_, _, _, err = repo.RegistrarEventos(ctx, lote.ID, testSectorID, testSalidaDevID, []lotesector.Evento{
		{EventoID: "eeeeeeee-0000-0000-0000-000000000001", ProductoID: testProductoID, Estado: ptr("ok")},
	})
	require.ErrorIs(t, err, lotesector.ErrCerrado)
}

// TestLoteSectorRepository_CerrarIdempotente verifica que el segundo cierre no
// pise los conteos finales autoritativos.
func TestLoteSectorRepository_CerrarIdempotente(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupLoteSectorRepo(t)
	lote := abrirLote(t, ctx, repo)

	ok := 5
	closed, yaCerrado, err := repo.Cerrar(ctx, lotesector.CerrarParams{
		LoteID:   lote.ID,
		SectorID: testSectorID,
		Conteos:  lotesector.Conteos{OK: &ok, Total: 5},
		Motivo:   "sin_detecciones",
	})
	require.NoError(t, err)
	require.False(t, yaCerrado)
	require.Equal(t, lotesector.EstadoCerrado, closed.Estado)
	require.NotNil(t, closed.Conteos.OK)
	require.Equal(t, 5, *closed.Conteos.OK)
	require.NotNil(t, closed.CerradoEn)
	require.NotNil(t, closed.MotivoCierre)
	require.Equal(t, "sin_detecciones", *closed.MotivoCierre)

	other := 99
	again, yaCerrado, err := repo.Cerrar(ctx, lotesector.CerrarParams{
		LoteID:   lote.ID,
		SectorID: testSectorID,
		Conteos:  lotesector.Conteos{OK: &other, Total: 99},
		Motivo:   "manual",
	})
	require.NoError(t, err)
	require.True(t, yaCerrado)
	require.NotNil(t, again.Conteos.OK)
	require.Equal(t, 5, *again.Conteos.OK, "los conteos finales no deben cambiar")
	require.Equal(t, 5, again.Conteos.Total)
}

// TestLoteSectorRepository_BucketSinEventosQuedaNull verifica que un estado sin
// eventos permanezca NULL, nunca 0, y que los recibidos sean no nulos.
func TestLoteSectorRepository_BucketSinEventosQuedaNull(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupLoteSectorRepo(t)
	lote := abrirLote(t, ctx, repo)

	_, _, updated, err := repo.RegistrarEventos(ctx, lote.ID, testSectorID, testSalidaDevID, []lotesector.Evento{
		{EventoID: "ffffffff-0000-0000-0000-000000000001", ProductoID: testProductoID, Estado: ptr("ok")},
	})
	require.NoError(t, err)
	require.NotNil(t, updated.Conteos.OK)
	require.Nil(t, updated.Conteos.Crudo)
	require.Nil(t, updated.Conteos.Quemado)
	require.Equal(t, 1, updated.Conteos.Total)

	got, err := repo.GetByID(ctx, lote.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Conteos.OK)
	require.Nil(t, got.Conteos.Crudo)
	require.Nil(t, got.Conteos.Quemado)
}

// TestLoteSectorRepository_Historial verifica el orden más-nuevos-primero, el
// filtro por producto y el cursor opaco.
func TestLoteSectorRepository_Historial(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupLoteSectorRepo(t)

	first := abrirLote(t, ctx, repo)
	_, _, err := repo.Cerrar(ctx, lotesector.CerrarParams{
		LoteID:   first.ID,
		SectorID: testSectorID,
		Conteos:  lotesector.Conteos{Total: 0},
		Motivo:   "manual",
	})
	require.NoError(t, err)
	second := abrirLote(t, ctx, repo)

	lotes, total, err := repo.Historial(ctx, lotesector.HistorialParams{SectorID: testSectorID, Limite: 10})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, lotes, 2)
	require.Equal(t, second.ID, lotes[0].ID, "el más nuevo debe ir primero")

	page2, total2, err := repo.Historial(ctx, lotesector.HistorialParams{
		SectorID:  testSectorID,
		Limite:    10,
		AntesDe:   &lotes[0].AbiertoEn,
		AntesDeID: lotes[0].ID,
	})
	require.NoError(t, err)
	require.Equal(t, 2, total2, "el total ignora el cursor")
	require.Len(t, page2, 1)
	require.Equal(t, first.ID, page2[0].ID)

	_, filtered, err := repo.Historial(ctx, lotesector.HistorialParams{
		SectorID:   testSectorID,
		ProductoID: "aaaaaaaa-0000-0000-0000-0000000000ff",
		Limite:     10,
	})
	require.NoError(t, err)
	require.Equal(t, 0, filtered)
}

// TestLoteSectorRepository_22P02Mapping verifica que los ids no-UUID no
// escapen como 500: los lookups/updates de lote se traducen a ErrNotFound y el
// filtro producto_id del historial a ErrPayloadInvalido.
func TestLoteSectorRepository_22P02Mapping(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupLoteSectorRepo(t)

	t.Run("GetByID no-UUID → ErrNotFound", func(t *testing.T) {
		_, err := repo.GetByID(ctx, "no-es-uuid")
		require.ErrorIs(t, err, lotesector.ErrNotFound)
	})

	t.Run("RegistrarEventos no-UUID → ErrNotFound", func(t *testing.T) {
		_, _, _, err := repo.RegistrarEventos(ctx, "no-es-uuid", testSectorID, testSalidaDevID, nil)
		require.ErrorIs(t, err, lotesector.ErrNotFound)
	})

	t.Run("Cerrar no-UUID → ErrNotFound", func(t *testing.T) {
		_, _, err := repo.Cerrar(ctx, lotesector.CerrarParams{
			LoteID:   "no-es-uuid",
			SectorID: testSectorID,
			Conteos:  lotesector.Conteos{Total: 0},
			Motivo:   "manual",
		})
		require.ErrorIs(t, err, lotesector.ErrNotFound)
	})

	t.Run("Historial producto_id no-UUID → ErrPayloadInvalido", func(t *testing.T) {
		_, _, err := repo.Historial(ctx, lotesector.HistorialParams{
			SectorID:   testSectorID,
			ProductoID: "no-es-uuid",
			Limite:     10,
		})
		require.ErrorIs(t, err, lotesector.ErrPayloadInvalido)
	})
}
