package reporte

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/reporte"
)

// Errores de validación de filtros
var (
	ErrRangoFechasInvalido = errors.New("reporte: la fecha de fin no puede ser anterior a la fecha de inicio")
	ErrTurnoInvalido       = errors.New("reporte: turno inválido, debe ser 'mañana', 'tarde' o 'noche'")
)

var turnosValidos = map[string]bool{
	"mañana": true,
	"tarde":  true,
	"noche":  true,
}

// Service implementa la lógica de negocio para el cálculo de KPIs y reportes.
type Service struct {
	repo reporte.Repository
}

// NewService instancia un nuevo servicio de reportes.
func NewService(repo reporte.Repository) *Service {
	return &Service{repo: repo}
}

// CalcularKPIFinanciero valida los parámetros del filtro y obtiene el cálculo consolidado
// del impacto económico de mermas en ARS.
func (s *Service) CalcularKPIFinanciero(ctx context.Context, filtro reporte.FiltroKPI) (*reporte.KPIFinanciero, error) {
	// Validar consistencia de fechas
	if filtro.FechaInicio != nil && filtro.FechaFin != nil {
		if filtro.FechaFin.Before(*filtro.FechaInicio) {
			return nil, ErrRangoFechasInvalido
		}
	}

	// Validar turno si fue provisto
	if strings.TrimSpace(filtro.Turno) != "" {
		turnoNorm := strings.ToLower(strings.TrimSpace(filtro.Turno))
		if !turnosValidos[turnoNorm] {
			return nil, fmt.Errorf("%w: '%s'", ErrTurnoInvalido, filtro.Turno)
		}
		filtro.Turno = turnoNorm
	}

	kpi, err := s.repo.GetKPIFinanciero(ctx, filtro)
	if err != nil {
		return nil, fmt.Errorf("error al calcular kpi financiero: %w", err)
	}

	return kpi, nil
}
