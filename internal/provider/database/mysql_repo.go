package database

import (
	"errors"
	"sync"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/horno"
)

// MySQLRepository implements horno.Repository and alerta.Repository.
// It simulates a MySQL database adapter for testing and transactional scaffolding.
type MySQLRepository struct {
	mu      sync.RWMutex
	hornos  map[string]horno.Horno
	alertas map[string][]alerta.Alerta
}

// NewMySQLRepository initializes the MySQL repository simulator with seed data.
func NewMySQLRepository() *MySQLRepository {
	repo := &MySQLRepository{
		hornos:  make(map[string]horno.Horno),
		alertas: make(map[string][]alerta.Alerta),
	}

	// Seed data representing a real industrial kiln (Horno) in factory line
	repo.hornos["horno-01"] = horno.Horno{
		ID:          "horno-01",
		Nombre:      "Horno Rotativo de Clinkerización A-1",
		Temperatura: 185.3,
		Estado:      "ACTIVO",
		UltimoCheck: time.Now(),
	}

	return repo
}

// GetByID retrieves a Horno record from the simulated MySQL database.
func (r *MySQLRepository) GetByID(id string) (*horno.Horno, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	h, ok := r.hornos[id]
	if !ok {
		return nil, errors.New("horno no encontrado")
	}
	return &h, nil
}

// Update updates a Horno record inside the database.
func (r *MySQLRepository) Update(h *horno.Horno) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.hornos[h.ID]; !ok {
		return errors.New("horno no encontrado para actualizar")
	}

	h.UltimoCheck = time.Now()
	r.hornos[h.ID] = *h
	return nil
}

// Save saves an industrial alert event to the database.
func (r *MySQLRepository) Save(a *alerta.Alerta) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	a.CreadaEn = time.Now()
	r.alertas[a.HornoID] = append(r.alertas[a.HornoID], *a)
	return nil
}

// GetByHornoID retrieves all alerts generated for a specific kiln.
func (r *MySQLRepository) GetByHornoID(hornoID string) ([]alerta.Alerta, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	alerts, ok := r.alertas[hornoID]
	if !ok {
		return []alerta.Alerta{}, nil
	}
	return alerts, nil
}
