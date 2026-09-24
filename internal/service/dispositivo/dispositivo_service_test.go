package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

func testDeviceContextFor(id string) context.Context {
	return deviceauth.WithPrincipal(context.Background(), deviceauth.Principal{DeviceID: id})
}

func testDeviceContext() context.Context {
	return testDeviceContextFor("d1")
}

type fakeDispositivoRepo struct {
	mu           sync.Mutex
	dispositivos map[string]dispositivo.Dispositivo
	revoked      map[string]bool
	inserted     []dispositivo.MetricaDispositivo
	createErr    error
	updateErr    error
	revokeErr    error
	revokeActor  string
	revokeID     string
}

func (f *fakeDispositivoRepo) Create(ctx context.Context, d *dispositivo.Dispositivo) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.createErr != nil {
		return f.createErr
	}

	// Simula la generación de UUID por PostgreSQL y el created_at de la DB.
	d.ID = "d-nuevo"
	d.CreatedAt = time.Now().UTC()
	f.dispositivos[d.ID] = *d
	return nil
}

// Update actualiza nombre/ubicación de un dispositivo existente en el map,
// devolviendo ErrDispositivoNotFound si el id no existe.
func (f *fakeDispositivoRepo) Update(ctx context.Context, d *dispositivo.Dispositivo) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.updateErr != nil {
		return f.updateErr
	}
	cur, ok := f.dispositivos[d.ID]
	if !ok {
		return dispositivo.ErrDispositivoNotFound
	}
	cur.Nombre = d.Nombre
	cur.SectorID = d.SectorID
	cur.WhepURL = d.WhepURL
	f.dispositivos[d.ID] = cur
	return nil
}

// Revoke simula la baja lógica: registra el actor, quita el dispositivo del map
// y recuerda que fue revocado para ser idempotente. Devuelve
// ErrDispositivoNotFound si nunca existió.
func (f *fakeDispositivoRepo) Revoke(ctx context.Context, actorEmail, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.revokeActor = actorEmail
	f.revokeID = id
	if _, ok := f.dispositivos[id]; !ok {
		if f.revoked[id] {
			return nil // ya revocado: idempotente
		}
		return dispositivo.ErrDispositivoNotFound
	}
	delete(f.dispositivos, id)
	if f.revoked == nil {
		f.revoked = map[string]bool{}
	}
	f.revoked[id] = true
	return nil
}

func (f *fakeDispositivoRepo) InsertMetrica(ctx context.Context, m *dispositivo.MetricaDispositivo) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	m.ID = "m-1"
	f.inserted = append(f.inserted, *m)
	return nil
}

// insertedCount devuelve la cantidad de métricas insertadas de forma segura.
func (f *fakeDispositivoRepo) insertedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.inserted)
}

// insertedAt devuelve una copia de la métrica insertada en la posición i.
func (f *fakeDispositivoRepo) insertedAt(i int) dispositivo.MetricaDispositivo {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.inserted[i]
}

func (f *fakeDispositivoRepo) GetDispositivosConUltimaMetrica(ctx context.Context) ([]dispositivo.DispositivoConUltimaMetrica, error) {
	var out []dispositivo.DispositivoConUltimaMetrica
	for _, d := range f.dispositivos {
		out = append(out, dispositivo.DispositivoConUltimaMetrica{Dispositivo: d})
	}
	return out, nil
}

func (f *fakeDispositivoRepo) GetDispositivoByID(ctx context.Context, id string) (*dispositivo.Dispositivo, error) {
	if d, ok := f.dispositivos[id]; ok {
		return &d, nil
	}
	return nil, dispositivo.ErrDispositivoNotFound
}

func (f *fakeDispositivoRepo) GetMetricasByDispositivo(ctx context.Context, id string, page, pageSize int) (*dispositivo.PaginatedResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	items := append([]dispositivo.MetricaDispositivo(nil), f.inserted...)
	return &dispositivo.PaginatedResult{
		Items:    items,
		Total:    len(items),
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// drainEvents consume los eventos del canal de un cliente SSE hasta el timeout.
func drainEvents(client *sse.Client, timeout time.Duration) []string {
	var types []string
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-client.Events:
			types = append(types, ev.EventType)
		case <-deadline:
			return types
		}
	}
}

func TestProcessPing_UpdatesStateAndBroadcasts(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	svc := NewDispositivoService(repo, store, broker)

	estado, err := svc.ProcessPing(testDeviceContext(), dispositivo.PingRequest{
		DispositivoID:      "d1",
		CpuPct:             20,
		MemRamDisponibleMb: 400,
		TempChip:           50,
		AiProcessorPct:     35,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.Estado != dispositivo.EstadoOnline {
		t.Fatalf("expected online, got %q", estado.Estado)
	}
	if estado.UltimaMetrica == nil || estado.UltimaMetrica.CpuPct != 20 {
		t.Fatalf("expected last metric set, got %+v", estado.UltimaMetrica)
	}
	if estado.UltimaMetrica == nil || estado.UltimaMetrica.AiProcessorPct != 35 {
		t.Fatalf("expected aiProcessorPct in last metric, got %+v", estado.UltimaMetrica)
	}
	if estado.LastSeen == nil {
		t.Fatal("expected lastSeen set")
	}

	// Primer ping: transición offline->online → emite metric + state.
	types := drainEvents(client, 500*time.Millisecond)
	if !containsAll(types, "dispositivo.metric", "dispositivo.state") {
		t.Fatalf("expected metric and state events on first ping, got %v", types)
	}
}

func TestProcessPing_RequiresOperationalPrincipal(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{"d1": {ID: "d1", Nombre: "Pi 1"}}}
	svc := NewDispositivoService(repo, database.NewMemoryDispositivoStateStore(), sse.NewBroker())
	_, err := svc.ProcessPing(context.Background(), dispositivo.PingRequest{DispositivoID: "d1"})
	if !errors.Is(err, deviceauth.ErrInvalidToken) {
		t.Fatalf("expected invalid device token, got %v", err)
	}
	if len(repo.inserted) != 0 {
		t.Fatalf("metric persisted without principal: %d", len(repo.inserted))
	}
}

func TestProcessPing_PropagaTelemetriaExtendidaAlEstadoHistorialYSSE(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	memTotal := 1024.0
	almacenamientoDisponible := 20000.0
	almacenamientoTotal := 64000.0
	svc := NewDispositivoService(repo, store, broker)
	estado, err := svc.ProcessPing(testDeviceContext(), dispositivo.PingRequest{
		DispositivoID:              "d1",
		MemRamDisponibleMb:         512,
		MemRamTotalMb:              &memTotal,
		AlmacenamientoDisponibleMb: &almacenamientoDisponible,
		AlmacenamientoTotalMb:      &almacenamientoTotal,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.UltimaMetrica.MemRamTotalMb == nil || *estado.UltimaMetrica.MemRamTotalMb != memTotal {
		t.Fatalf("expected memRamTotalMb in latest state, got %+v", estado.UltimaMetrica)
	}

	deadline := time.Now().Add(2 * time.Second)
	for repo.insertedCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if repo.insertedCount() != 1 {
		t.Fatalf("expected extended metric in history, got %d records", repo.insertedCount())
	}
	inserted := repo.insertedAt(0)
	if inserted.AlmacenamientoTotalMb == nil || *inserted.AlmacenamientoTotalMb != almacenamientoTotal {
		t.Fatalf("expected almacenamientoTotalMb in persisted metric, got %+v", inserted)
	}

	select {
	case event := <-client.Events:
		if event.EventType != "dispositivo.metric" {
			t.Fatalf("expected dispositivo.metric SSE event, got %s", event.EventType)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			t.Fatalf("invalid SSE payload: %v", err)
		}
		data := payload["data"].(map[string]interface{})
		metric := data["ultimaMetrica"].(map[string]interface{})
		if metric["almacenamientoDisponibleMb"] != almacenamientoDisponible || metric["memRamTotalMb"] != memTotal {
			t.Fatalf("expected extended telemetry in SSE payload, got %+v", metric)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dispositivo.metric SSE event")
	}
}

func TestProcessPing_NoStateEventWhenAlreadyOnline(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	svc := NewDispositivoService(repo, store, broker)

	ping := dispositivo.PingRequest{DispositivoID: "d1", CpuPct: 20, MemRamDisponibleMb: 400, TempChip: 50, AiProcessorPct: 35}
	if _, err := svc.ProcessPing(testDeviceContext(), ping); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	drainEvents(client, 100*time.Millisecond)

	if _, err := svc.ProcessPing(testDeviceContext(), ping); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	types := drainEvents(client, 500*time.Millisecond)
	if len(types) != 1 || types[0] != "dispositivo.metric" {
		t.Fatalf("expected only dispositivo.metric on consecutive ping, got %v", types)
	}
}

func TestProcessPing_DispositivoNotFound(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	_, err := svc.ProcessPing(testDeviceContextFor("missing"), dispositivo.PingRequest{
		DispositivoID:      "missing",
		CpuPct:             10,
		MemRamDisponibleMb: 100,
		TempChip:           40,
		AiProcessorPct:     20,
	})
	if !errors.Is(err, dispositivo.ErrDispositivoNotFound) {
		t.Fatalf("expected ErrDispositivoNotFound, got %v", err)
	}
}

func TestProcessPing_InsertsMetricaHistory(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)
	if _, err := svc.ProcessPing(testDeviceContext(), dispositivo.PingRequest{
		DispositivoID: "d1", CpuPct: 30, MemRamDisponibleMb: 500, TempChip: 55, AiProcessorPct: 40,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for repo.insertedCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if repo.insertedCount() != 1 {
		t.Fatalf("expected 1 inserted metric in history, got %d", repo.insertedCount())
	}
	inserted := repo.insertedAt(0)
	if inserted.CpuPct != 30 || inserted.DispositivoID != "d1" {
		t.Fatalf("unexpected inserted metric: %+v", inserted)
	}
}

func TestCreate_PersistsAndRegistersOffline(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	svc := NewDispositivoService(repo, store, broker)
	sectorID := "horno-1"

	estado, err := svc.Create(context.Background(), dispositivo.CreateDispositivoRequest{
		Nombre:   "  Raspberry Pi Horno 2  ",
		SectorID: &sectorID,
		Tipo:     "ENTRADA_HORNO",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Devuelve el estado recién registrado: offline, sin métrica ni last_seen.
	if estado.Estado != dispositivo.EstadoOffline {
		t.Fatalf("expected offline, got %q", estado.Estado)
	}
	if estado.Nombre != "Raspberry Pi Horno 2" || estado.SectorID == nil || *estado.SectorID != sectorID {
		t.Fatalf("expected trimmed metadata, got %+v", estado)
	}
	if estado.Tipo == nil || *estado.Tipo != "ENTRADA_HORNO" {
		t.Fatalf("expected tipo propagated, got %+v", estado.Tipo)
	}
	if estado.UltimaMetrica != nil || estado.LastSeen != nil {
		t.Fatalf("expected no metric/lastSeen for new device, got %+v", estado)
	}

	// Persistió en el repositorio.
	persisted, err := repo.GetDispositivoByID(context.Background(), estado.DispositivoID)
	if err != nil {
		t.Fatalf("expected device persisted, got %v", err)
	}
	if persisted.Nombre != "Raspberry Pi Horno 2" || persisted.SectorID == nil || *persisted.SectorID != sectorID {
		t.Fatalf("unexpected persisted device: %+v", persisted)
	}
	if persisted.Tipo == nil || *persisted.Tipo != "ENTRADA_HORNO" {
		t.Fatalf("expected persisted tipo, got %+v", persisted.Tipo)
	}

	// Quedó registrado en el store: aparece de inmediato en GetAllEstados.
	estados := svc.GetAllEstados()
	if len(estados) != 1 || estados[0].DispositivoID != estado.DispositivoID {
		t.Fatalf("expected device in GetAllEstados, got %+v", estados)
	}

	// Emite el evento SSE dispositivo.state.
	types := drainEvents(client, 500*time.Millisecond)
	if !containsAll(types, "dispositivo.state") {
		t.Fatalf("expected dispositivo.state event, got %v", types)
	}
}

func TestCreate_PropagatesRepoError(t *testing.T) {
	repo := &fakeDispositivoRepo{
		dispositivos: map[string]dispositivo.Dispositivo{},
		createErr:    errors.New("db caída"),
	}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	_, err := svc.Create(context.Background(), dispositivo.CreateDispositivoRequest{Nombre: "Pi X", Tipo: "ENTRADA_HORNO"})
	if err == nil {
		t.Fatal("expected error propagated from repo")
	}
	if err.Error() != "db caída" {
		t.Fatalf("expected original repo error, got %v", err)
	}

	// Sin persistencia exitosa no debe registrarse nada en el store.
	if len(svc.GetAllEstados()) != 0 {
		t.Fatalf("expected no states registered after failed create, got %+v", svc.GetAllEstados())
	}
}

func TestGetMetricas_AppliesDefaults(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	res, err := svc.GetMetricas(context.Background(), "d1", 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Page != 1 || res.PageSize != 10 {
		t.Fatalf("expected page=1 pageSize=10 defaults, got %d/%d", res.Page, res.PageSize)
	}
}

func TestGetMetricas_CapsPageSize(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	res, err := svc.GetMetricas(context.Background(), "d1", 1, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PageSize != maxPageSize {
		t.Fatalf("expected pageSize capped at %d, got %d", maxPageSize, res.PageSize)
	}
}

func TestGetMetricas_DispositivoNotFound(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	_, err := svc.GetMetricas(context.Background(), "missing", 1, 10)
	if !errors.Is(err, dispositivo.ErrDispositivoNotFound) {
		t.Fatalf("expected ErrDispositivoNotFound, got %v", err)
	}
}

func TestStartReaper_EmitsOfflineState(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	store.Hydrate([]dispositivo.DispositivoConUltimaMetrica{
		{Dispositivo: dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"}},
	}, 25*time.Second, time.Now().UTC())
	broker := sse.NewBroker()
	client := broker.Subscribe()

	// El dispositivo queda con last_seen muy antiguo (1 min atrás) y online.
	store.Update(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"}, dispositivo.MetricaDispositivo{
		DispositivoID: "d1", AiProcessorPct: 0, ReceivedAt: time.Now().UTC().Add(-1 * time.Minute),
	})

	svc := NewDispositivoService(repo, store, broker)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.StartReaper(ctx, 20*time.Millisecond, 25*time.Second)
	}()

	types := drainEvents(client, 2*time.Second)
	if !containsAll(types, "dispositivo.state") {
		t.Fatalf("expected dispositivo.state offline event, got %v", types)
	}

	estado, _ := store.Get("d1")
	if estado.Estado != dispositivo.EstadoOffline {
		t.Fatalf("expected d1 offline after reaper, got %q", estado.Estado)
	}

	// Detener el reaper y esperar a que termine antes de cerrar el canal del cliente,
	// para garantizar que ningún Broadcast ocurra después del Unsubscribe.
	cancel()
	<-done
	broker.Unsubscribe(client)
}

func containsAll(types []string, wanted ...string) bool {
	for _, w := range wanted {
		found := false
		for _, ty := range types {
			if ty == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestUpdate_PreservaNombreYActualizaSector verifica que el update del panel
// cambie el sector pero PRESERVE el nombre (inmutable desde el panel), conservando
// salud/métrica del caché y emitiendo el evento SSE dispositivo.state.
func TestUpdate_PreservaNombreYActualizaSector(t *testing.T) {
	sectorID := "horno-1"
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1", SectorID: &sectorID},
	}}
	store := database.NewMemoryDispositivoStateStore()
	// Registra d1 como online con métrica para verificar que se preserva.
	store.Update(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", SectorID: &sectorID}, dispositivo.MetricaDispositivo{
		DispositivoID: "d1", CpuPct: 10, MemRamDisponibleMb: 500, TempChip: 50, AiProcessorPct: 10, ReceivedAt: time.Now().UTC(),
	})
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	svc := NewDispositivoService(repo, store, broker)

	nuevoSector := "horno-2"
	estado, err := svc.Update(context.Background(), dispositivo.UpdateDispositivoRequest{
		DispositivoID: "d1",
		SectorID:      &nuevoSector,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.Nombre != "Pi 1" {
		t.Fatalf("expected nombre preserved, got %q", estado.Nombre)
	}
	if estado.SectorID == nil || *estado.SectorID != nuevoSector {
		t.Fatalf("expected updated sector, got %+v", estado.SectorID)
	}
	// El update preserva estado de salud y última métrica del caché.
	if estado.Estado != dispositivo.EstadoOnline {
		t.Fatalf("expected preserved online state, got %q", estado.Estado)
	}
	if estado.UltimaMetrica == nil {
		t.Fatal("expected preserved ultima metrica after update")
	}

	// El caché devuelve el nombre preservado y el sector nuevo.
	cached, ok := store.Get("d1")
	if !ok {
		t.Fatal("expected device in store")
	}
	if cached.Nombre != "Pi 1" || cached.SectorID == nil || *cached.SectorID != nuevoSector {
		t.Fatalf("expected preserved nombre and updated sector in cache, got %+v", cached)
	}

	// Emite el evento SSE dispositivo.state.
	types := drainEvents(client, 500*time.Millisecond)
	if !containsAll(types, "dispositivo.state") {
		t.Fatalf("expected dispositivo.state event, got %v", types)
	}
}

// TestRename_PreservaSectorYWhepURL verifica que el rename de la propia
// Raspberry cambia solo el nombre, preservando sector/whepUrl, persiste en el
// repositorio, actualiza el caché y emite el evento SSE dispositivo.state.
func TestRename_PreservaSectorYWhepURL(t *testing.T) {
	const whep = "https://camaras.example.com/whep/horno-1"
	sectorID := "horno-1"
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1", SectorID: &sectorID, WhepURL: whep},
	}}
	store := database.NewMemoryDispositivoStateStore()
	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", SectorID: &sectorID, WhepURL: whep})
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	svc := NewDispositivoService(repo, store, broker)

	estado, err := svc.Rename(context.Background(), "d1", "  Pi 1 Renombrada  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.Nombre != "Pi 1 Renombrada" {
		t.Fatalf("expected trimmed renamed name, got %q", estado.Nombre)
	}
	if estado.SectorID == nil || *estado.SectorID != sectorID || estado.WhepURL != whep {
		t.Fatalf("expected sector/whepUrl preserved, got %+v", estado)
	}

	// Persistió el nombre nuevo sin tocar sector/whepUrl.
	persisted, err := repo.GetDispositivoByID(context.Background(), "d1")
	if err != nil {
		t.Fatalf("expected device persisted, got %v", err)
	}
	if persisted.Nombre != "Pi 1 Renombrada" || persisted.SectorID == nil || *persisted.SectorID != sectorID || persisted.WhepURL != whep {
		t.Fatalf("unexpected persisted device: %+v", persisted)
	}

	// El caché devuelve el nombre nuevo preservando sector/whepUrl.
	cached, ok := store.Get("d1")
	if !ok {
		t.Fatal("expected device in store")
	}
	if cached.Nombre != "Pi 1 Renombrada" || cached.SectorID == nil || *cached.SectorID != sectorID || cached.WhepURL != whep {
		t.Fatalf("expected updated cache, got %+v", cached)
	}

	// Emite el evento SSE dispositivo.state.
	types := drainEvents(client, 500*time.Millisecond)
	if !containsAll(types, "dispositivo.state") {
		t.Fatalf("expected dispositivo.state event, got %v", types)
	}
}

func TestRename_NotFound(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	_, err := svc.Rename(context.Background(), "missing", "Pi X")
	if !errors.Is(err, dispositivo.ErrDispositivoNotFound) {
		t.Fatalf("expected ErrDispositivoNotFound, got %v", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	_, err := svc.Update(context.Background(), dispositivo.UpdateDispositivoRequest{
		DispositivoID: "missing",
	})
	if !errors.Is(err, dispositivo.ErrDispositivoNotFound) {
		t.Fatalf("expected ErrDispositivoNotFound, got %v", err)
	}
}

func TestRevoke_RemueveDelCacheYPasaActor(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"})
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	if err := svc.Revoke(context.Background(), "supervisor@fermar.com.ar", "d1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// El actor viaja al repositorio para la auditoría.
	if repo.revokeActor != "supervisor@fermar.com.ar" || repo.revokeID != "d1" {
		t.Fatalf("expected actor/id propagated to repo, got actor=%q id=%q", repo.revokeActor, repo.revokeID)
	}

	// Quedó fuera del caché y del repositorio (baja lógica en el fake).
	if _, ok := store.Get("d1"); ok {
		t.Fatal("expected device removed from cache")
	}
	if _, err := repo.GetDispositivoByID(context.Background(), "d1"); !errors.Is(err, dispositivo.ErrDispositivoNotFound) {
		t.Fatalf("expected device removed from repo, got %v", err)
	}
}

// TestRevoke_Idempotente verifica que repetir la baja no falle.
func TestRevoke_Idempotente(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"})
	svc := NewDispositivoService(repo, store, sse.NewBroker())

	if err := svc.Revoke(context.Background(), "supervisor@fermar.com.ar", "d1"); err != nil {
		t.Fatalf("unexpected error on first revoke: %v", err)
	}
	if err := svc.Revoke(context.Background(), "supervisor@fermar.com.ar", "d1"); err != nil {
		t.Fatalf("expected idempotent revoke, got %v", err)
	}
}

func TestRevoke_NotFound(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	err := svc.Revoke(context.Background(), "supervisor@fermar.com.ar", "missing")
	if !errors.Is(err, dispositivo.ErrDispositivoNotFound) {
		t.Fatalf("expected ErrDispositivoNotFound, got %v", err)
	}
}

func TestCreate_PropagaWhepURL(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)
	whep := "https://camaras.example.com/whep/horno-2"

	estado, err := svc.Create(context.Background(), dispositivo.CreateDispositivoRequest{
		Nombre:  "Raspberry Pi Horno 2",
		WhepURL: whep,
		Tipo:    "SALIDA_HORNO",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.WhepURL != whep {
		t.Fatalf("expected whepUrl in returned estado, got %q", estado.WhepURL)
	}

	persisted, err := repo.GetDispositivoByID(context.Background(), estado.DispositivoID)
	if err != nil {
		t.Fatalf("expected device persisted, got %v", err)
	}
	if persisted.WhepURL != whep {
		t.Fatalf("expected whepUrl persisted, got %q", persisted.WhepURL)
	}
}

func TestUpdate_PropagaWhepURL(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1", WhepURL: "https://camaras.example.com/whep/viejo"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", WhepURL: "https://camaras.example.com/whep/viejo"})
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)
	whep := "https://camaras.example.com/whep/horno-1"

	estado, err := svc.Update(context.Background(), dispositivo.UpdateDispositivoRequest{
		DispositivoID: "d1",
		WhepURL:       whep,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.WhepURL != whep {
		t.Fatalf("expected whepUrl in returned estado, got %q", estado.WhepURL)
	}

	persisted, err := repo.GetDispositivoByID(context.Background(), "d1")
	if err != nil {
		t.Fatalf("expected device persisted, got %v", err)
	}
	if persisted.WhepURL != whep {
		t.Fatalf("expected whepUrl persisted on update, got %q", persisted.WhepURL)
	}

	cached, ok := store.Get("d1")
	if !ok || cached.WhepURL != whep {
		t.Fatalf("expected whepUrl propagated to cache, got %+v", cached)
	}
}

// TestCreate_TipoInvalido verifica que el alta exija un tipo canónico y que un
// tipo inválido no llegue a persistirse.
func TestCreate_TipoInvalido(t *testing.T) {
	for _, tipo := range []string{"", "   ", "HORNO", "entrada-horno"} {
		t.Run(tipo, func(t *testing.T) {
			repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
			svc := NewDispositivoService(repo, database.NewMemoryDispositivoStateStore(), sse.NewBroker())

			_, err := svc.Create(context.Background(), dispositivo.CreateDispositivoRequest{Nombre: "Pi", Tipo: tipo})
			if err == nil {
				t.Fatal("expected validation error for invalid type")
			}
			if !dispositivo.IsValidationError(err) {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
			if len(repo.dispositivos) != 0 {
				t.Fatalf("invalid create must not persist, got %d", len(repo.dispositivos))
			}
		})
	}
}

// TestCreate_NormalizaTipo verifica que el tipo se normalice a mayúsculas antes
// de persistirlo.
func TestCreate_NormalizaTipo(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
	svc := NewDispositivoService(repo, database.NewMemoryDispositivoStateStore(), sse.NewBroker())

	estado, err := svc.Create(context.Background(), dispositivo.CreateDispositivoRequest{Nombre: "Pi", Tipo: "  salida_horno "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.Tipo == nil || *estado.Tipo != "SALIDA_HORNO" {
		t.Fatalf("expected normalized tipo SALIDA_HORNO, got %+v", estado.Tipo)
	}
}

// TestUpdate_PreservaTipo verifica que la modificación no pise el tipo.
func TestUpdate_PreservaTipo(t *testing.T) {
	tipo := "SALIDA_HORNO"
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1", Tipo: &tipo},
	}}
	store := database.NewMemoryDispositivoStateStore()
	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", Tipo: &tipo})
	svc := NewDispositivoService(repo, store, sse.NewBroker())

	estado, err := svc.Update(context.Background(), dispositivo.UpdateDispositivoRequest{
		DispositivoID: "d1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.Tipo == nil || *estado.Tipo != tipo {
		t.Fatalf("expected tipo preserved in estado, got %+v", estado.Tipo)
	}
	cached, _ := store.Get("d1")
	if cached.Tipo == nil || *cached.Tipo != tipo {
		t.Fatalf("expected tipo preserved in cache, got %+v", cached.Tipo)
	}
	persisted, _ := repo.GetDispositivoByID(context.Background(), "d1")
	if persisted.Tipo == nil || *persisted.Tipo != tipo {
		t.Fatalf("expected tipo preserved in repo, got %+v", persisted.Tipo)
	}
}

// TestRename_PreservaTipo verifica que el rename de la Raspberry no pise el tipo.
func TestRename_PreservaTipo(t *testing.T) {
	tipo := "ENTRADA_HORNO"
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1", Tipo: &tipo},
	}}
	store := database.NewMemoryDispositivoStateStore()
	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", Tipo: &tipo})
	svc := NewDispositivoService(repo, store, sse.NewBroker())

	estado, err := svc.Rename(context.Background(), "d1", "Pi 1 Renombrada")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.Tipo == nil || *estado.Tipo != tipo {
		t.Fatalf("expected tipo preserved in estado, got %+v", estado.Tipo)
	}
	cached, _ := store.Get("d1")
	if cached.Tipo == nil || *cached.Tipo != tipo {
		t.Fatalf("expected tipo preserved in cache, got %+v", cached.Tipo)
	}
	persisted, _ := repo.GetDispositivoByID(context.Background(), "d1")
	if persisted.Tipo == nil || *persisted.Tipo != tipo {
		t.Fatalf("expected tipo preserved in repo, got %+v", persisted.Tipo)
	}
}

// TestUpdate_DesasignaSectorConNil verifica que un sectorId nulo en el body
// desasigne el dispositivo del sector tanto en el repo como en el caché.
func TestUpdate_DesasignaSectorConNil(t *testing.T) {
	sectorID := "horno-1"
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1", SectorID: &sectorID},
	}}
	store := database.NewMemoryDispositivoStateStore()
	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", SectorID: &sectorID})
	svc := NewDispositivoService(repo, store, sse.NewBroker())

	estado, err := svc.Update(context.Background(), dispositivo.UpdateDispositivoRequest{
		DispositivoID: "d1",
		SectorID:      nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.SectorID != nil {
		t.Fatalf("expected sector unassigned in estado, got %+v", estado.SectorID)
	}
	cached, _ := store.Get("d1")
	if cached.SectorID != nil {
		t.Fatalf("expected sector unassigned in cache, got %+v", cached.SectorID)
	}
	persisted, _ := repo.GetDispositivoByID(context.Background(), "d1")
	if persisted.SectorID != nil {
		t.Fatalf("expected sector unassigned in repo, got %+v", persisted.SectorID)
	}
}

// TestUpdate_PropagaErroresDeSector verifica que los errores tipados de
// asignación de sector del repositorio se propaguen sin transformarse.
func TestUpdate_PropagaErroresDeSector(t *testing.T) {
	for _, repoErr := range []error{dispositivo.ErrSectorNotFound, dispositivo.ErrSectorTipoDuplicado} {
		repo := &fakeDispositivoRepo{
			dispositivos: map[string]dispositivo.Dispositivo{"d1": {ID: "d1", Nombre: "Pi 1"}},
			updateErr:    repoErr,
		}
		svc := NewDispositivoService(repo, database.NewMemoryDispositivoStateStore(), sse.NewBroker())

		sectorID := "horno-1"
		_, err := svc.Update(context.Background(), dispositivo.UpdateDispositivoRequest{
			DispositivoID: "d1",
			SectorID:      &sectorID,
		})
		if !errors.Is(err, repoErr) {
			t.Fatalf("expected %v propagated, got %v", repoErr, err)
		}
	}
}
