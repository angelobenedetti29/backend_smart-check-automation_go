package service

import (
	lote "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_productivo"
)

const (
	defaultPage     = 1
	defaultPageSize = 10
	maxPageSize     = 100
)

// LoteProductivoService implementa lote_productivo.Service.
type LoteProductivoService struct {
	repo lote.Repository
}

// NewLoteProductivoService instancia el servicio inyectando el repositorio.
func NewLoteProductivoService(repo lote.Repository) *LoteProductivoService {
	return &LoteProductivoService{repo: repo}
}

// GetAll recupera una página de lotes productivos aplicando límites de paginación.
func (s *LoteProductivoService) GetAll(page, pageSize int) (*lote.PaginatedResult, error) {
	if page < 1 {
		page = defaultPage
	}
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	return s.repo.GetAll(page, pageSize)
}
