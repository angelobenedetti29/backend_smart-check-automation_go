package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
)

type fakeProductoService struct {
	productos []producto.Producto
	err       error
}

func (f *fakeProductoService) List(context.Context) ([]producto.Producto, error) {
	return f.productos, f.err
}

func TestProductoHandleOKIncluyeInactivos(t *testing.T) {
	svc := &fakeProductoService{productos: []producto.Producto{
		{ID: "prod-1", Nombre: "Tostada", Activo: true},
		{ID: "prod-2", Nombre: "Retirado", Activo: false},
	}}
	h := NewProductoHandler(svc)

	rec := httptest.NewRecorder()
	h.Handle(rec, httptest.NewRequest(http.MethodGet, "/api/v1/productos", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Success bool                `json:"success"`
		Data    []producto.Producto `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.True(t, env.Success)
	require.Len(t, env.Data, 2)
	assert.False(t, env.Data[1].Activo)
}

func TestProductoHandleError500(t *testing.T) {
	h := NewProductoHandler(&fakeProductoService{err: errors.New("boom")})

	rec := httptest.NewRecorder()
	h.Handle(rec, httptest.NewRequest(http.MethodGet, "/api/v1/productos", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestProductoHandleMetodoNoPermitido(t *testing.T) {
	h := NewProductoHandler(&fakeProductoService{})

	rec := httptest.NewRecorder()
	h.Handle(rec, httptest.NewRequest(http.MethodPost, "/api/v1/productos", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
