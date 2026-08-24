package database

import (
	"testing"
	"time"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
)

func TestMemoryDispositivoStateStore_Hydrate_SetsOffline(t *testing.T) {
	store := NewMemoryDispositivoStateStore()
	store.Hydrate([]dispositivo.DispositivoConUltimaMetrica{
		{Dispositivo: dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", Ubicacion: "Línea A"}},
	}, 25*time.Second, time.Now().UTC())

	estado, ok := store.Get("d1")
	if !ok {
		t.Fatal("expected estado for hydrated device")
	}
	if estado.Estado != dispositivo.EstadoOffline {
		t.Fatalf("expected offline after hydrate, got %q", estado.Estado)
	}
	if estado.LastSeen != nil {
		t.Fatalf("expected nil lastSeen after hydrate, got %v", estado.LastSeen)
	}
	if estado.Nombre != "Pi 1" || estado.Ubicacion != "Línea A" {
		t.Fatalf("expected hydrated metadata, got %+v", estado)
	}
}

func TestMemoryDispositivoStateStore_Hydrate_RecoversOnlineWithMetric(t *testing.T) {
	store := NewMemoryDispositivoStateStore()
	now := time.Now().UTC()
	metrica := dispositivo.MetricaDispositivo{
		ID: "m1", DispositivoID: "d1", CpuPct: 30, MemRamDisponibleMb: 500, TempChip: 55, AiProcessorPct: 20,
		ReceivedAt: now.Add(-5 * time.Second),
	}

	store.Hydrate([]dispositivo.DispositivoConUltimaMetrica{
		{Dispositivo: dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"}, UltimaMetrica: &metrica},
	}, 25*time.Second, now)

	estado, ok := store.Get("d1")
	if !ok {
		t.Fatal("expected estado for hydrated device")
	}
	if estado.Estado != dispositivo.EstadoOnline {
		t.Fatalf("expected online (last metric fresh), got %q", estado.Estado)
	}
	if estado.UltimaMetrica == nil || estado.UltimaMetrica.CpuPct != 30 {
		t.Fatalf("expected last metric recovered, got %+v", estado.UltimaMetrica)
	}
	if estado.LastSeen == nil || !estado.LastSeen.Equal(metrica.ReceivedAt) {
		t.Fatalf("expected lastSeen recovered, got %v", estado.LastSeen)
	}
}

func TestMemoryDispositivoStateStore_Hydrate_KeepsMetricWhenStaleOffline(t *testing.T) {
	store := NewMemoryDispositivoStateStore()
	now := time.Now().UTC()
	metrica := dispositivo.MetricaDispositivo{
		ID: "m1", DispositivoID: "d1", CpuPct: 30, MemRamDisponibleMb: 500, TempChip: 55, AiProcessorPct: 20,
		ReceivedAt: now.Add(-40 * time.Second),
	}

	store.Hydrate([]dispositivo.DispositivoConUltimaMetrica{
		{Dispositivo: dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"}, UltimaMetrica: &metrica},
	}, 25*time.Second, now)

	estado, _ := store.Get("d1")
	if estado.Estado != dispositivo.EstadoOffline {
		t.Fatalf("expected offline (last metric stale), got %q", estado.Estado)
	}
	// Aunque esté offline, debe conservar la última métrica para "última conexión: hace X".
	if estado.UltimaMetrica == nil || estado.LastSeen == nil {
		t.Fatalf("expected last metric and lastSeen preserved when offline, got %+v", estado)
	}
}

func TestMemoryDispositivoStateStore_Register_AppearsOffline(t *testing.T) {
	store := NewMemoryDispositivoStateStore()

	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", Ubicacion: "Línea A"})

	estado, ok := store.Get("d1")
	if !ok {
		t.Fatal("expected estado for registered device")
	}
	if estado.Estado != dispositivo.EstadoOffline {
		t.Fatalf("expected offline after register, got %q", estado.Estado)
	}
	if estado.UltimaMetrica != nil || estado.LastSeen != nil {
		t.Fatalf("expected no metric/lastSeen after register, got %+v", estado)
	}
	if estado.Nombre != "Pi 1" || estado.Ubicacion != "Línea A" {
		t.Fatalf("expected registered metadata, got %+v", estado)
	}

	// Aparece en GetAllEstados sin esperar el primer ping.
	all := store.GetAll()
	if len(all) != 1 || all[0].DispositivoID != "d1" {
		t.Fatalf("expected device in GetAll, got %+v", all)
	}
}

func TestMemoryDispositivoStateStore_Register_UpdatesWithoutDuplicating(t *testing.T) {
	store := NewMemoryDispositivoStateStore()

	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1", Ubicacion: "Línea A"})
	store.Register(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1 Renombrada", Ubicacion: "Línea B"})

	estado, ok := store.Get("d1")
	if !ok {
		t.Fatal("expected estado for registered device")
	}
	if estado.Nombre != "Pi 1 Renombrada" || estado.Ubicacion != "Línea B" {
		t.Fatalf("expected updated metadata, got %+v", estado)
	}
	if estado.Estado != dispositivo.EstadoOffline {
		t.Fatalf("expected still offline after re-register, got %q", estado.Estado)
	}

	// El re-registro no duplica la entrada.
	all := store.GetAll()
	if len(all) != 1 {
		t.Fatalf("expected single entry after re-register, got %d", len(all))
	}
}

func TestMemoryDispositivoStateStore_Update_TransitionsToOnline(t *testing.T) {
	store := NewMemoryDispositivoStateStore()
	store.Hydrate([]dispositivo.DispositivoConUltimaMetrica{
		{Dispositivo: dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"}},
	}, 25*time.Second, time.Now().UTC())

	now := time.Now().UTC()
	m := dispositivo.MetricaDispositivo{
		DispositivoID:      "d1",
		CpuPct:             30,
		MemRamDisponibleMb: 500,
		TempChip:           55,
		AiProcessorPct:     20,
		ReceivedAt:         now,
	}

	prev, curr, estado := store.Update(dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"}, m)
	if prev != dispositivo.EstadoOffline || curr != dispositivo.EstadoOnline {
		t.Fatalf("expected offline->online transition, got %s->%s", prev, curr)
	}
	if estado == nil || estado.Estado != dispositivo.EstadoOnline {
		t.Fatalf("expected online estado, got %+v", estado)
	}
	if estado.UltimaMetrica == nil || estado.UltimaMetrica.CpuPct != 30 {
		t.Fatalf("expected last metric set, got %+v", estado.UltimaMetrica)
	}
	if estado.LastSeen == nil || !estado.LastSeen.Equal(now) {
		t.Fatalf("expected lastSeen = now, got %v", estado.LastSeen)
	}
}

func TestMemoryDispositivoStateStore_Update_OnlineStaysOnline(t *testing.T) {
	store := NewMemoryDispositivoStateStore()
	d := dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"}
	store.Hydrate([]dispositivo.DispositivoConUltimaMetrica{{Dispositivo: d}}, 25*time.Second, time.Now().UTC())

	store.Update(d, dispositivo.MetricaDispositivo{DispositivoID: "d1", ReceivedAt: time.Now().UTC()})
	prev, curr, _ := store.Update(d, dispositivo.MetricaDispositivo{DispositivoID: "d1", ReceivedAt: time.Now().UTC()})

	if prev != dispositivo.EstadoOnline || curr != dispositivo.EstadoOnline {
		t.Fatalf("expected online->online, got %s->%s", prev, curr)
	}
}

func TestMemoryDispositivoStateStore_MarkOfflineIfStale(t *testing.T) {
	store := NewMemoryDispositivoStateStore()
	d := dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"}
	store.Hydrate([]dispositivo.DispositivoConUltimaMetrica{{Dispositivo: d}}, 25*time.Second, time.Now().UTC())

	now := time.Now().UTC()
	store.Update(d, dispositivo.MetricaDispositivo{DispositivoID: "d1", ReceivedAt: now.Add(-40 * time.Second)})

	offline := store.MarkOfflineIfStale(25*time.Second, now)
	if len(offline) != 1 || offline[0] != "d1" {
		t.Fatalf("expected d1 to be marked offline, got %v", offline)
	}

	estado, _ := store.Get("d1")
	if estado.Estado != dispositivo.EstadoOffline {
		t.Fatalf("expected d1 offline, got %q", estado.Estado)
	}
}

func TestMemoryDispositivoStateStore_MarkOfflineIfStale_FreshStaysOnline(t *testing.T) {
	store := NewMemoryDispositivoStateStore()
	d := dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"}
	store.Hydrate([]dispositivo.DispositivoConUltimaMetrica{{Dispositivo: d}}, 25*time.Second, time.Now().UTC())

	now := time.Now().UTC()
	store.Update(d, dispositivo.MetricaDispositivo{DispositivoID: "d1", ReceivedAt: now.Add(-5 * time.Second)})

	offline := store.MarkOfflineIfStale(25*time.Second, now)
	if len(offline) != 0 {
		t.Fatalf("expected no offline transitions, got %v", offline)
	}
}

func TestMemoryDispositivoStateStore_MarkOfflineIfStale_Boundary25s(t *testing.T) {
	store := NewMemoryDispositivoStateStore()
	d := dispositivo.Dispositivo{ID: "d1", Nombre: "Pi 1"}
	store.Hydrate([]dispositivo.DispositivoConUltimaMetrica{{Dispositivo: d}}, 25*time.Second, time.Now().UTC())
	now := time.Now().UTC()

	store.Update(d, dispositivo.MetricaDispositivo{DispositivoID: "d1", ReceivedAt: now.Add(-25 * time.Second)})
	if offline := store.MarkOfflineIfStale(25*time.Second, now); len(offline) != 1 {
		t.Fatalf("at exactly 25s the device should go offline, got %v", offline)
	}

	store.Update(d, dispositivo.MetricaDispositivo{DispositivoID: "d1", ReceivedAt: now.Add(-24 * time.Second)})
	if offline := store.MarkOfflineIfStale(25*time.Second, now); len(offline) != 0 {
		t.Fatalf("under 25s the device should stay online, got %v", offline)
	}
}
