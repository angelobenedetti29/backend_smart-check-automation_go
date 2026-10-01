// Package service expone la lógica de negocio del catálogo de productos.
package service

import (
	"context"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
)

// Service resuelve el catálogo maestro de productos y su ABM.
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

// Create valida el request, verifica la unicidad case-insensitive del nombre y
// da de alta un producto nuevo con sus parámetros de control embebidos.
func (s *Service) Create(ctx context.Context, req producto.ProductoRequest) (*producto.Producto, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	nombre := strings.TrimSpace(req.Nombre)

	ocupado, err := s.repo.ExistsByNombre(ctx, nombre, "")
	if err != nil {
		return nil, err
	}
	if ocupado {
		return nil, producto.ErrDuplicado
	}

	p := &producto.Producto{
		Nombre:                 nombre,
		PesoReferenciaKg:       req.PesoReferenciaKg,
		ToleranciaPesoPct:      req.ToleranciaPesoPct,
		DimensionBaseCm:        req.DimensionBaseCm,
		ToleranciaDimensionCm:  req.ToleranciaDimensionCm,
		TempMin:                req.TempMin,
		TempMax:                req.TempMax,
		VelocidadCintaMin:      req.VelocidadCintaMin,
		VelocidadCintaMax:      req.VelocidadCintaMax,
		TempSetpoint:           req.TempSetpoint,
		VelocidadCintaSetpoint: req.VelocidadCintaSetpoint,
	}
	return s.repo.Create(ctx, p)
}

// Update valida el request, verifica la unicidad del nombre excluyendo el propio
// producto y aplica los cambios sobre el catálogo.
func (s *Service) Update(ctx context.Context, id string, req producto.ProductoRequest) (*producto.Producto, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	nombre := strings.TrimSpace(req.Nombre)

	ocupado, err := s.repo.ExistsByNombre(ctx, nombre, id)
	if err != nil {
		return nil, err
	}
	if ocupado {
		return nil, producto.ErrDuplicado
	}

	p := &producto.Producto{
		ID:                     id,
		Nombre:                 nombre,
		PesoReferenciaKg:       req.PesoReferenciaKg,
		ToleranciaPesoPct:      req.ToleranciaPesoPct,
		DimensionBaseCm:        req.DimensionBaseCm,
		ToleranciaDimensionCm:  req.ToleranciaDimensionCm,
		TempMin:                req.TempMin,
		TempMax:                req.TempMax,
		VelocidadCintaMin:      req.VelocidadCintaMin,
		VelocidadCintaMax:      req.VelocidadCintaMax,
		TempSetpoint:           req.TempSetpoint,
		VelocidadCintaSetpoint: req.VelocidadCintaSetpoint,
	}
	return s.repo.Update(ctx, p)
}

// Deactivate aplica la baja lógica (activo=false) delegando en el repositorio.
func (s *Service) Deactivate(ctx context.Context, id string) error {
	return s.repo.Deactivate(ctx, id)
}
