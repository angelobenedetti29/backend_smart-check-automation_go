// Package service expone la lógica de negocio del catálogo de productos.
package service

import (
	"context"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
)

// Service resuelve el catálogo maestro de productos.
type Service struct {
	repo producto.Repository
}

// NewService instancia el servicio inyectando el repositorio del catálogo.
func NewService(repo producto.Repository) *Service {
	return &Service{repo: repo}
}

// List devuelve todo el catálogo (incluidos los productos inactivos); el
// filtrado por vigencia lo decide el consumidor.
func (s *Service) List(ctx context.Context) ([]producto.Producto, error) {
	return s.repo.List(ctx)
}
