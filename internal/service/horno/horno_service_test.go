package service

import (
	"context"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
)

// fakeAlertaRepo aísla el servicio del repositorio PostgreSQL real.
type fakeAlertaRepo struct {
	guardadas []alerta.Alerta
}

func (f *fakeAlertaRepo) Save(a *alerta.Alerta) error {
	a.ID = "alerta-1"
	f.guardadas = append(f.guardadas, *a)
	return nil
}

func (f *fakeAlertaRepo) GetByHornoID(string) ([]alerta.Alerta, error) {
	return f.guardadas, nil
}

func (f *fakeAlertaRepo) Listar(context.Context, string) ([]alerta.Alerta, error) {
	return f.guardadas, nil
}

func (f *fakeAlertaRepo) Ack(context.Context, string, alerta.Usuario) (*alerta.Alerta, error) {
	return nil, alerta.ErrNotFound
}

func TestHornoService_UpdateTemperature_Normal(t *testing.T) {
	hRepo := database.NewPostgresRepository()
	aRepo := &fakeAlertaRepo{}

	svc := NewHornoService(hRepo, aRepo)

	// Update to safe temperature
	h, err := svc.UpdateTemperature("horno-01", 175.0)
	if err != nil {
		t.Fatalf("unexpected error updating temperature: %v", err)
	}

	if h.Estado != "ACTIVO" {
		t.Errorf("expected state 'ACTIVO', got '%s'", h.Estado)
	}

	// Verify no alerts were stored
	if len(aRepo.guardadas) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(aRepo.guardadas))
	}
}

func TestHornoService_UpdateTemperature_Warning(t *testing.T) {
	hRepo := database.NewPostgresRepository()
	aRepo := &fakeAlertaRepo{}

	svc := NewHornoService(hRepo, aRepo)

	// Update to warning temperature
	h, err := svc.UpdateTemperature("horno-01", 188.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if h.Estado != "ATENCION" {
		t.Errorf("expected state 'ATENCION', got '%s'", h.Estado)
	}

	// Verify alert was stored
	if len(aRepo.guardadas) != 1 {
		t.Fatalf("expected 1 warning alert, got %d", len(aRepo.guardadas))
	}

	if aRepo.guardadas[0].Severidad != alerta.SeveridadWarning {
		t.Errorf("expected severidad 'WARNING', got '%s'", aRepo.guardadas[0].Severidad)
	}
	if aRepo.guardadas[0].Tipo != alerta.TipoTemperaturaHorno {
		t.Errorf("expected tipo '%s', got '%s'", alerta.TipoTemperaturaHorno, aRepo.guardadas[0].Tipo)
	}
	if aRepo.guardadas[0].Estado != alerta.EstadoActiva {
		t.Errorf("expected estado 'ACTIVA', got '%s'", aRepo.guardadas[0].Estado)
	}
}

func TestHornoService_UpdateTemperature_Critical(t *testing.T) {
	hRepo := database.NewPostgresRepository()
	aRepo := &fakeAlertaRepo{}

	svc := NewHornoService(hRepo, aRepo)

	// Update to critical temperature
	h, err := svc.UpdateTemperature("horno-01", 215.2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if h.Estado != "MANTENIMIENTO" {
		t.Errorf("expected state 'MANTENIMIENTO', got '%s'", h.Estado)
	}

	// Verify alert was stored
	if len(aRepo.guardadas) != 1 {
		t.Fatalf("expected 1 critical alert, got %d", len(aRepo.guardadas))
	}

	if aRepo.guardadas[0].Severidad != alerta.SeveridadCritical {
		t.Errorf("expected severidad 'CRITICAL', got '%s'", aRepo.guardadas[0].Severidad)
	}
}
