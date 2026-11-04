package service

import (
	"fmt"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/horno"
)

// HornoService implements horno.Service contract.
// It acts as the application's service layer containing the core transactional business logic.
type HornoService struct {
	hornoRepo  horno.Repository
	alertaRepo alerta.Repository
}

// NewHornoService instantiates a new HornoService injecting needed adapters/repositories.
func NewHornoService(hRepo horno.Repository, aRepo alerta.Repository) *HornoService {
	return &HornoService{
		hornoRepo:  hRepo,
		alertaRepo: aRepo,
	}
}

// CheckStatus retrieves the status of a specific oven.
func (s *HornoService) CheckStatus(id string) (*horno.Horno, error) {
	return s.hornoRepo.Ensure(id)
}

// UpdateTemperature updates the oven's temperature and handles threshold logic, generating database alerts.
func (s *HornoService) UpdateTemperature(id string, temp float64) (*horno.Horno, error) {
	// 1. Retrieve (or create on demand) the sector's simulated oven
	h, err := s.hornoRepo.Ensure(id)
	if err != nil {
		return nil, err
	}

	// 2. Perform business logic calculations & state machines
	oldTemp := h.Temperatura
	h.Temperatura = temp

	if temp > 200.0 {
		h.Estado = "MANTENIMIENTO"

		// Create and store a CRITICAL alert
		alt := &alerta.Alerta{
			ID:        fmt.Sprintf("alt-temp-%d", time.Now().UnixNano()),
			HornoID:   h.ID,
			Tipo:      alerta.TipoTemperaturaHorno,
			Severidad: alerta.SeveridadCritical,
			Estado:    alerta.EstadoActiva,
			Mensaje:   fmt.Sprintf("Temperatura crítica excedida: %.1f°C. Límite seguro: 200.0°C (Previa: %.1f°C)", temp, oldTemp),
			CreadaEn:  time.Now(),
		}
		err = s.alertaRepo.Save(alt)
		if err != nil {
			return nil, fmt.Errorf("error al guardar alerta: %v", err)
		}
	} else if temp > 180.0 {
		h.Estado = "ATENCION"

		// Create and store a WARNING alert
		alt := &alerta.Alerta{
			ID:        fmt.Sprintf("alt-temp-%d", time.Now().UnixNano()),
			HornoID:   h.ID,
			Tipo:      alerta.TipoTemperaturaHorno,
			Severidad: alerta.SeveridadWarning,
			Estado:    alerta.EstadoActiva,
			Mensaje:   fmt.Sprintf("Temperatura elevada en zona activa: %.1f°C. (Previa: %.1f°C)", temp, oldTemp),
			CreadaEn:  time.Now(),
		}
		_ = s.alertaRepo.Save(alt)
	} else {
		// Normal thermal range
		h.Estado = "ACTIVO"
	}

	// 3. Commit the domain model updates back to store
	err = s.hornoRepo.Update(h)
	if err != nil {
		return nil, err
	}

	return h, nil
}
