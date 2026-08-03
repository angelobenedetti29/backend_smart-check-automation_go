package database

import (
	"sort"
	"sync"
	"time"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
)

// MemoryDispositivoStateStore implementa dispositivo.StateStore con un map
// protegido por RWMutex. Almacena el estado actual (última métrica + last_seen)
// de cada dispositivo sin tocar la base de datos.
type MemoryDispositivoStateStore struct {
	mu   sync.RWMutex
	data map[string]*dispositivo.EstadoDispositivo
}

// NewMemoryDispositivoStateStore crea un store vacío de estados de dispositivos.
func NewMemoryDispositivoStateStore() *MemoryDispositivoStateStore {
	return &MemoryDispositivoStateStore{
		data: make(map[string]*dispositivo.EstadoDispositivo),
	}
}

// Hydrate precarga el catálogo de dispositivos junto con su última métrica
// registrada. El estado se calcula según la antigüedad de last_seen: si existe
// una métrica más reciente que el umbral, el dispositivo queda online; de lo
// contrario offline conservando su última métrica y last_seen.
func (s *MemoryDispositivoStateStore) Hydrate(items []dispositivo.DispositivoConUltimaMetrica, umbral time.Duration, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, item := range items {
		d := item.Dispositivo
		if cur, ok := s.data[d.ID]; ok {
			cur.Nombre = d.Nombre
			cur.Ubicacion = d.Ubicacion
			continue
		}

		estado := dispositivo.EstadoOffline
		if m := item.UltimaMetrica; m != nil && now.Sub(m.ReceivedAt) < umbral {
			estado = dispositivo.EstadoOnline
		}

		entry := &dispositivo.EstadoDispositivo{
			DispositivoID: d.ID,
			Nombre:        d.Nombre,
			Ubicacion:     d.Ubicacion,
			Estado:        estado,
		}
		if m := item.UltimaMetrica; m != nil {
			lastSeen := m.ReceivedAt
			entry.UltimaMetrica = m
			entry.LastSeen = &lastSeen
		}

		s.data[d.ID] = entry
	}
}

// Update registra una nueva métrica recibida y marca el dispositivo como online.
// Devuelve el estado previo y el nuevo para detectar transiciones offline→online.
func (s *MemoryDispositivoStateStore) Update(d dispositivo.Dispositivo, m dispositivo.MetricaDispositivo) (prevState, currState string, estado *dispositivo.EstadoDispositivo) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prevState = dispositivo.EstadoOffline
	if cur, ok := s.data[d.ID]; ok && cur.Estado != "" {
		prevState = cur.Estado
	}

	lastSeen := m.ReceivedAt
	estado = &dispositivo.EstadoDispositivo{
		DispositivoID: d.ID,
		Nombre:        d.Nombre,
		Ubicacion:     d.Ubicacion,
		Estado:        dispositivo.EstadoOnline,
		UltimaMetrica: &m,
		LastSeen:      &lastSeen,
	}

	s.data[d.ID] = estado
	return prevState, dispositivo.EstadoOnline, estado
}

// Get devuelve una copia del estado actual de un dispositivo.
func (s *MemoryDispositivoStateStore) Get(dispositivoID string) (*dispositivo.EstadoDispositivo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cur, ok := s.data[dispositivoID]
	if !ok {
		return nil, false
	}
	cp := *cur
	return &cp, true
}

// GetAll devuelve el estado de todos los dispositivos, ordenados por nombre.
func (s *MemoryDispositivoStateStore) GetAll() []dispositivo.EstadoDispositivo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]dispositivo.EstadoDispositivo, 0, len(s.data))
	for _, v := range s.data {
		items = append(items, *v)
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Nombre < items[j].Nombre })
	return items
}

// MarkOfflineIfStale recorre los dispositivos online y marca como offline a
// aquellos cuyo last_seen sea igual o anterior al umbral. Devuelve los IDs que
// transicionaron a offline para que el service pueda emitir el evento SSE.
func (s *MemoryDispositivoStateStore) MarkOfflineIfStale(umbral time.Duration, now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var offlineIDs []string
	for id, cur := range s.data {
		if cur.Estado != dispositivo.EstadoOnline || cur.LastSeen == nil {
			continue
		}
		if now.Sub(*cur.LastSeen) >= umbral {
			cur.Estado = dispositivo.EstadoOffline
			offlineIDs = append(offlineIDs, id)
		}
	}
	return offlineIDs
}
