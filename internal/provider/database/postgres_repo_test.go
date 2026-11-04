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

func TestPostgresRepository_Ensure(t *testing.T) {
	repo := NewPostgresRepository()

	// Crea el horno simulado del sector on demand.
	h, err := repo.Ensure("sector-nuevo")
	if err != nil {
		t.Fatalf("unexpected error ensuring horno: %v", err)
	}
	if h.ID != "sector-nuevo" {
		t.Errorf("expected id del sector 'sector-nuevo', got '%s'", h.ID)
	}
	if h.Nombre != "Horno sector-nuevo" {
		t.Errorf("expected nombre 'Horno sector-nuevo', got '%s'", h.Nombre)
	}
	if h.Estado != "ACTIVO" {
		t.Errorf("expected estado ACTIVO, got '%s'", h.Estado)
	}

	// Idempotente: el segundo Ensure devuelve el mismo horno, no lo recrea.
	h.Temperatura = 195.0
	_ = repo.Update(h)

	h2, err := repo.Ensure("sector-nuevo")
	if err != nil {
		t.Fatalf("unexpected error on second Ensure: %v", err)
	}
	if h2.Temperatura != 195.0 {
		t.Errorf("Ensure no debe recrear el horno existente, got temp=%.1f", h2.Temperatura)
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
