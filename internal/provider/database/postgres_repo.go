package database

import (
	"errors"
	"sync"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/horno"
)

// PostgresRepository implements horno.Repository.
// Es un registro en memoria de comandos/auditoría: la última consigna
// despachada y el flag CONTROL_MANUAL. El estado real del horno se proyecta
// desde metricas_horno (telemetría de la Pi) + el lote abierto.
type PostgresRepository struct {
	mu     sync.RWMutex
	hornos map[string]horno.Horno
}

// NewPostgresRepository initializes the PostgreSQL repository simulator with seed data.
func NewPostgresRepository() *PostgresRepository {
	repo := &PostgresRepository{
		hornos: make(map[string]horno.Horno),
	}

	// Seed data representing a real industrial kiln (Horno) in factory line
	repo.hornos["horno-01"] = horno.Horno{
		ID:             "horno-01",
		Nombre:         "Horno Rotativo de Clinkerización A-1",
		Temperatura:    185.3,
		VelocidadCinta: 0.20,
		Estado:         "ACTIVO",
		UltimoCheck:    time.Now(),
	}

	return repo
}

// GetByID retrieves a Horno record from the simulated PostgreSQL database.
func (r *PostgresRepository) GetByID(id string) (*horno.Horno, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	h, ok := r.hornos[id]
	if !ok {
		return nil, errors.New("horno no encontrado")
	}
	return &h, nil
}

// Ensure devuelve el horno en memoria del sector. Si no existe, lo crea con
// valores por defecto (sin persistencia real). Es idempotente y se usa para
// despachar/auditar consignas y para el endpoint legacy de temperatura; no
// representa el estado real, que vive en metricas_horno.
func (r *PostgresRepository) Ensure(id string) (*horno.Horno, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if h, ok := r.hornos[id]; ok {
		return &h, nil
	}

	h := horno.Horno{
		ID:             id,
		Nombre:         "Horno " + id,
		Temperatura:    185.3,
		VelocidadCinta: 0.20,
		Estado:         "ACTIVO",
		UltimoCheck:    time.Now(),
	}
	r.hornos[id] = h
	return &h, nil
}

// Update updates a Horno record inside the database.
func (r *PostgresRepository) Update(h *horno.Horno) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.hornos[h.ID]; !ok {
		return errors.New("horno no encontrado para actualizar")
	}

	h.UltimoCheck = time.Now()
	r.hornos[h.ID] = *h
	return nil
}
