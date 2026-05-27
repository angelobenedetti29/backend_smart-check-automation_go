package service

import (
	"fmt"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/horno"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/yolo_client"
)

// HornoService implements horno.Service contract.
// It acts as the application's service layer containing the core transactional business logic.
type HornoService struct {
	hornoRepo  horno.Repository
	alertaRepo alerta.Repository
	yoloClient *yolo_client.YOLOClient
}

// NewHornoService instantiates a new HornoService injecting needed adapters/repositories.
func NewHornoService(hRepo horno.Repository, aRepo alerta.Repository, yolo *yolo_client.YOLOClient) *HornoService {
	return &HornoService{
		hornoRepo:  hRepo,
		alertaRepo: aRepo,
		yoloClient: yolo,
	}
}

// CheckStatus retrieves the status of a specific oven, orchestrating a computer vision check via YOLO.
func (s *HornoService) CheckStatus(id string) (*horno.Horno, error) {
	// 1. Fetch from repository
	h, err := s.hornoRepo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// 2. Perform real-time machine vision inspection of pieces via YOLO adapter
	detection := s.yoloClient.InspectConveyorLine()
	if detection.DefectFound {
		// Log and raise transactional alert
		alt := &alerta.Alerta{
			ID:       fmt.Sprintf("alt-yolo-%d", time.Now().UnixNano()),
			HornoID:  h.ID,
			Nivel:    "CRITICAL",
			Mensaje:  fmt.Sprintf("Defecto visual detectado en cinta transportadora por YOLO (Confianza: %.2f%%)", detection.Confidence*100),
			CreadaEn: time.Now(),
		}
		_ = s.alertaRepo.Save(alt)

		// Transition Horno to maintenance status due to failure detection
		h.Estado = "MANTENIMIENTO"
		_ = s.hornoRepo.Update(h)
	}

	return h, nil
}

// UpdateTemperature updates the oven's temperature and handles threshold logic, generating database alerts.
func (s *HornoService) UpdateTemperature(id string, temp float64) (*horno.Horno, error) {
	// 1. Retrieve the domain entity
	h, err := s.hornoRepo.GetByID(id)
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
			ID:       fmt.Sprintf("alt-temp-%d", time.Now().UnixNano()),
			HornoID:  h.ID,
			Nivel:    "CRITICAL",
			Mensaje:  fmt.Sprintf("Temperatura crítica excedida: %.1f°C. Límite seguro: 200.0°C (Previa: %.1f°C)", temp, oldTemp),
			CreadaEn: time.Now(),
		}
		err = s.alertaRepo.Save(alt)
		if err != nil {
			return nil, fmt.Errorf("error al guardar alerta: %v", err)
		}
	} else if temp > 180.0 {
		h.Estado = "ATENCION"

		// Create and store a WARNING alert
		alt := &alerta.Alerta{
			ID:       fmt.Sprintf("alt-temp-%d", time.Now().UnixNano()),
			HornoID:  h.ID,
			Nivel:    "WARNING",
			Mensaje:  fmt.Sprintf("Temperatura elevada en zona activa: %.1f°C. (Previa: %.1f°C)", temp, oldTemp),
			CreadaEn: time.Now(),
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
