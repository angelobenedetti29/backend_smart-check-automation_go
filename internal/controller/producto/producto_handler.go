// Package controller expone el endpoint HTTP del catálogo de productos.
package controller

import (
	"context"
	"net/http"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// ProductoService es el contrato mínimo que consume el handler.
type ProductoService interface {
	List(ctx context.Context) ([]producto.Producto, error)
}

// ProductoHandler maneja GET /api/v1/productos.
type ProductoHandler struct {
	service ProductoService
}

// NewProductoHandler instancia el handler inyectando el servicio del catálogo.
func NewProductoHandler(svc ProductoService) *ProductoHandler {
	return &ProductoHandler{service: svc}
}

// Handle maneja GET /api/v1/productos devolviendo el catálogo completo,
// incluidos los productos inactivos (la Pi filtra por vigencia).
func (h *ProductoHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	productos, err := h.service.List(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "Error al obtener el catálogo de productos", nil)
		return
	}

	response.OK(w, "Productos obtenidos exitosamente", productos)
}
