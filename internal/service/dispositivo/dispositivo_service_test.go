package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

type fakeDispositivoRepo struct {
	mu           sync.Mutex
	dispositivos map[string]dispositivo.Dispositivo
	inserted     []dispositivo.MetricaDispositivo
	createErr    error
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

	cur, ok := f.dispositivos[d.ID]
	if !ok {
		return dispositivo.ErrDispositivoNotFound
	}
	cur.Nombre = d.Nombre
	cur.Ubicacion = d.Ubicacion
	cur.WhepURL = d.WhepURL
	f.dispositivos[d.ID] = cur
	return nil
}

// Delete borra un dispositivo del map, devolviendo ErrDispositivoNotFound si el
// id no existe.
func (f *fakeDispositivoRepo) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.dispositivos[id]; !ok {
		return dispositivo.ErrDispositivoNotFound
	}
	delete(f.dispositivos, id)
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
		"d1": {ID: "d1", Nombre: "Pi 1", Ubicacion: "Línea A"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	svc := NewDispositivoService(repo, store, broker)

	estado, err := svc.ProcessPing(context.Background(), dispositivo.PingRequest{
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
	estado, err := svc.ProcessPing(context.Background(), dispositivo.PingRequest{
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
	if _, err := svc.ProcessPing(context.Background(), ping); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	drainEvents(client, 100*time.Millisecond)

	if _, err := svc.ProcessPing(context.Background(), ping); err != nil {
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

	_, err := svc.ProcessPing(context.Background(), dispositivo.PingRequest{
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
	if _, err := svc.ProcessPing(context.Background(), dispositivo.PingRequest{
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

	estado, err := svc.Create(context.Background(), dispositivo.CreateDispositivoRequest{
		Nombre:    "  Raspberry Pi Horno 2  ",
		Ubicacion: "  Línea B  ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Devuelve el estado recién registrado: offline, sin métrica ni last_seen.
	if estado.Estado != dispositivo.EstadoOffline {
		t.Fatalf("expected offline, got %q", estado.Estado)
	}
	if estado.Nombre != "Raspberry Pi Horno 2" || estado.Ubicacion != "Línea B" {
		t.Fatalf("expected trimmed metadata, got %+v", estado)
	}
	if estado.UltimaMetrica != nil || estado.LastSeen != nil {
		t.Fatalf("expected no metric/lastSeen for new device, got %+v", estado)
	}

	// Persistió en el repositorio.
	persisted, err := repo.GetDispositivoByID(context.Background(), estado.DispositivoID)
	if err != nil {
		t.Fatalf("expected device persisted, got %v", err)
	}
	if persisted.Nombre != "Raspberry Pi Horno 2" || persisted.Ubicacion != "Línea B" {
		t.Fatalf("unexpected persisted device: %+v", persisted)
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

	_, err := svc.Create(context.Background(), dispositivo.CreateDispositivoRequest{Nombre: "Pi X"})
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

func TestUpdate_ActualizaCacheYEmiteSSE(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1", Ubicacion: "Línea A"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	// Registra d1 como online con métrica para verificar que se preserva.
	store.Update(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", Ubicacion: "Línea A"}, dispositivo.MetricaDispositivo{
		DispositivoID: "d1", CpuPct: 10, MemRamDisponibleMb: 500, TempChip: 50, AiProcessorPct: 10, ReceivedAt: time.Now().UTC(),
	})
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	svc := NewDispositivoService(repo, store, broker)

	estado, err := svc.Update(context.Background(), dispositivo.UpdateDispositivoRequest{
		DispositivoID: "d1",
		Nombre:        "  Pi 1 Renombrado  ",
		Ubicacion:     "  Línea B  ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if estado.Nombre != "Pi 1 Renombrado" || estado.Ubicacion != "Línea B" {
		t.Fatalf("expected trimmed updated metadata, got %+v", estado)
	}
	// El update preserva estado de salud y última métrica del caché.
	if estado.Estado != dispositivo.EstadoOnline {
		t.Fatalf("expected preserved online state, got %q", estado.Estado)
	}
	if estado.UltimaMetrica == nil {
		t.Fatal("expected preserved ultima metrica after update")
	}

	// El caché devuelve el nombre/ubicación nuevos.
	cached, ok := store.Get("d1")
	if !ok {
		t.Fatal("expected device in store")
	}
	if cached.Nombre != "Pi 1 Renombrado" || cached.Ubicacion != "Línea B" {
		t.Fatalf("expected updated cache, got %+v", cached)
	}

	// Emite el evento SSE dispositivo.state.
	types := drainEvents(client, 500*time.Millisecond)
	if !containsAll(types, "dispositivo.state") {
		t.Fatalf("expected dispositivo.state event, got %v", types)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	_, err := svc.Update(context.Background(), dispositivo.UpdateDispositivoRequest{
		DispositivoID: "missing",
		Nombre:        "Pi X",
	})
	if !errors.Is(err, dispositivo.ErrDispositivoNotFound) {
		t.Fatalf("expected ErrDispositivoNotFound, got %v", err)
	}
}

func TestDelete_RemueveDelCache(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{
		"d1": {ID: "d1", Nombre: "Pi 1"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"})
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	if err := svc.Delete(context.Background(), "d1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Quedó fuera del caché y del repositorio.
	if _, ok := store.Get("d1"); ok {
		t.Fatal("expected device removed from cache")
	}
	if _, err := repo.GetDispositivoByID(context.Background(), "d1"); !errors.Is(err, dispositivo.ErrDispositivoNotFound) {
		t.Fatalf("expected device removed from repo, got %v", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	repo := &fakeDispositivoRepo{dispositivos: map[string]dispositivo.Dispositivo{}}
	store := database.NewMemoryDispositivoStateStore()
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)

	err := svc.Delete(context.Background(), "missing")
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
		"d1": {ID: "d1", Nombre: "Pi 1", Ubicacion: "Línea A", WhepURL: "https://camaras.example.com/whep/viejo"},
	}}
	store := database.NewMemoryDispositivoStateStore()
	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", Ubicacion: "Línea A", WhepURL: "https://camaras.example.com/whep/viejo"})
	broker := sse.NewBroker()

	svc := NewDispositivoService(repo, store, broker)
	whep := "https://camaras.example.com/whep/horno-1"

	estado, err := svc.Update(context.Background(), dispositivo.UpdateDispositivoRequest{
		DispositivoID: "d1",
		Nombre:        "Pi 1",
		Ubicacion:     "Línea A",
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
