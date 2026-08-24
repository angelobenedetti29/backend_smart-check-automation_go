package service

import (
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/yolo_client"
)

func TestHornoService_UpdateTemperature_Normal(t *testing.T) {
	hRepo := database.NewPostgresRepository()
	aRepo := hRepo // Simulates dual interface implementation
	yolo := yolo_client.NewYOLOClient("http://localhost:8500")

	svc := NewHornoService(hRepo, aRepo, yolo)

	// Update to safe temperature
	h, err := svc.UpdateTemperature("horno-01", 175.0)
	if err != nil {
		t.Fatalf("unexpected error updating temperature: %v", err)
	}

	if h.Estado != "ACTIVO" {
		t.Errorf("expected state 'ACTIVO', got '%s'", h.Estado)
	}

	// Verify no alerts were stored
	alerts, _ := aRepo.GetByHornoID("horno-01")
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(alerts))
	}
}

func TestHornoService_UpdateTemperature_Warning(t *testing.T) {
	hRepo := database.NewPostgresRepository()
	aRepo := hRepo
	yolo := yolo_client.NewYOLOClient("http://localhost:8500")

	svc := NewHornoService(hRepo, aRepo, yolo)

	// Update to warning temperature
	h, err := svc.UpdateTemperature("horno-01", 188.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if h.Estado != "ATENCION" {
		t.Errorf("expected state 'ATENCION', got '%s'", h.Estado)
	}

	// Verify alert was stored
	alerts, _ := aRepo.GetByHornoID("horno-01")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 warning alert, got %d", len(alerts))
	}

	if alerts[0].Nivel != "WARNING" {
		t.Errorf("expected level 'WARNING', got '%s'", alerts[0].Nivel)
	}
}

func TestHornoService_UpdateTemperature_Critical(t *testing.T) {
	hRepo := database.NewPostgresRepository()
	aRepo := hRepo
	yolo := yolo_client.NewYOLOClient("http://localhost:8500")

	svc := NewHornoService(hRepo, aRepo, yolo)

	// Update to critical temperature
	h, err := svc.UpdateTemperature("horno-01", 215.2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if h.Estado != "MANTENIMIENTO" {
		t.Errorf("expected state 'MANTENIMIENTO', got '%s'", h.Estado)
	}

	// Verify alert was stored
	alerts, _ := aRepo.GetByHornoID("horno-01")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 critical alert, got %d", len(alerts))
	}

	if alerts[0].Nivel != "CRITICAL" {
		t.Errorf("expected level 'CRITICAL', got '%s'", alerts[0].Nivel)
	}
}
