package database

import (
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
)

func TestMySQLRepository_GetByID(t *testing.T) {
	repo := NewMySQLRepository()

	// Test retrieving existing seeded record
	h, err := repo.GetByID("horno-01")
	if err != nil {
		t.Fatalf("unexpected error fetching seed horno-01: %v", err)
	}

	if h.Nombre != "Horno Rotativo de Clinkerización A-1" {
		t.Errorf("expected name 'Horno Rotativo de Clinkerización A-1', got '%s'", h.Nombre)
	}

	// Test fetching non-existing record
	_, err = repo.GetByID("non-existing")
	if err == nil {
		t.Fatal("expected error fetching non-existing ID, got nil")
	}
}

func TestMySQLRepository_Update(t *testing.T) {
	repo := NewMySQLRepository()

	h, _ := repo.GetByID("horno-01")
	h.Temperatura = 195.0
	h.Estado = "MANTENIMIENTO"

	err := repo.Update(h)
	if err != nil {
		t.Fatalf("failed to update horno: %v", err)
	}

	updated, _ := repo.GetByID("horno-01")
	if updated.Temperatura != 195.0 {
		t.Errorf("expected updated temp 195.0, got %f", updated.Temperatura)
	}
	if updated.Estado != "MANTENIMIENTO" {
		t.Errorf("expected updated state 'MANTENIMIENTO', got '%s'", updated.Estado)
	}
}

func TestMySQLRepository_Alerts(t *testing.T) {
	repo := NewMySQLRepository()

	a := &alerta.Alerta{
		ID:      "alert-01",
		HornoID: "horno-01",
		Nivel:   "WARNING",
		Mensaje: "Test Alert",
	}

	err := repo.Save(a)
	if err != nil {
		t.Fatalf("failed to save alert: %v", err)
	}

	alerts, err := repo.GetByHornoID("horno-01")
	if err != nil {
		t.Fatalf("failed to fetch alerts: %v", err)
	}

	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}

	if alerts[0].Mensaje != "Test Alert" {
		t.Errorf("expected 'Test Alert', got '%s'", alerts[0].Mensaje)
	}
}
