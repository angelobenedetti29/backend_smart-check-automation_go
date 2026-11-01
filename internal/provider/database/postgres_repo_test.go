package database

import "testing"

func TestPostgresRepository_GetByID(t *testing.T) {
	repo := NewPostgresRepository()

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

func TestPostgresRepository_Update(t *testing.T) {
	repo := NewPostgresRepository()

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
