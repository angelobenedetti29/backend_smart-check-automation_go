package service

import (
	"context"

	parametrosproducto "github.com/angelobenedetti29/smart-check-automation/internal/domain/parametros_producto"
)

// ParametrosProductoService implementa parametros_producto.Service.
type ParametrosProductoService struct {
	repo parametrosproducto.Repository
}

// NewParametrosProductoService instancia el servicio inyectando el repositorio.
func NewParametrosProductoService(repo parametrosproducto.Repository) *ParametrosProductoService {
	return &ParametrosProductoService{repo: repo}
}

// GetAll recupera todos los sets de parámetros de control configurados por producto.
func (s *ParametrosProductoService) GetAll(ctx context.Context) ([]parametrosproducto.ParametroProducto, error) {
	return s.repo.GetAll(ctx)
}

// Create da de alta un nuevo set de parámetros para un producto.
func (s *ParametrosProductoService) Create(ctx context.Context, req parametrosproducto.ParametroProductoRequest) (*parametrosproducto.ParametroProducto, error) {
	p := parametrosproducto.MapRequestToParametroProducto(req)
	if err := s.repo.Create(ctx, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Update aplica las nuevas reglas operativas a un producto existente. Las lógicas
// operativas del sistema (detección/recomendador) consultan siempre el registro
// vigente en parametros_producto, por lo que la actualización tiene efecto inmediato
// sobre los próximos lotes de ese producto.
func (s *ParametrosProductoService) Update(ctx context.Context, req parametrosproducto.ParametroProductoRequest) (*parametrosproducto.ParametroProducto, error) {
	p := parametrosproducto.MapRequestToParametroProducto(req)
	if err := s.repo.Update(ctx, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
