package database

import (
	"errors"
	"sync"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/horno"
)

// PostgresRepository implements horno.Repository.
// It simulates a PostgreSQL database adapter for testing and transactional scaffolding.
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
