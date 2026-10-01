package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
)

// fakeProductoService es un doble de prueba configurable del catálogo.
type fakeProductoService struct {
	productos []producto.Producto
	listErr   error

	createFn func(context.Context, producto.ProductoRequest) (*producto.Producto, error)
	updateFn func(context.Context, string, producto.ProductoRequest) (*producto.Producto, error)
	deleteFn func(context.Context, string) error

	lastUpdateID  string
	lastDeletedID string
}

func (f *fakeProductoService) List(context.Context) ([]producto.Producto, error) {
	return f.productos, f.listErr
}

func (f *fakeProductoService) Create(ctx context.Context, req producto.ProductoRequest) (*producto.Producto, error) {
	if f.createFn == nil {
		return &producto.Producto{ID: "nuevo", Nombre: req.Nombre}, nil
	}
	return f.createFn(ctx, req)
}

func (f *fakeProductoService) Update(ctx context.Context, id string, req producto.ProductoRequest) (*producto.Producto, error) {
	f.lastUpdateID = id
	if f.updateFn == nil {
		return &producto.Producto{ID: id, Nombre: req.Nombre}, nil
	}
	return f.updateFn(ctx, id, req)
}

func (f *fakeProductoService) Deactivate(ctx context.Context, id string) error {
	f.lastDeletedID = id
	if f.deleteFn == nil {
		return nil
	}
	return f.deleteFn(ctx, id)
}

const productoBodyValido = `{
	"nombre": "Tostada Integral",
	"pesoReferenciaKg": 0.25, "toleranciaPesoPct": 5.0,
	"dimensionBaseCm": 12.0, "toleranciaDimensionCm": 0.5,
	"tempMin": 180.0, "tempMax": 220.0,
	"velocidadCintaMin": 0.1, "velocidadCintaMax": 0.3
}`

func jsonRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
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

func TestProductoHandleListError500(t *testing.T) {
	h := NewProductoHandler(&fakeProductoService{listErr: errors.New("boom")})

	rec := httptest.NewRecorder()
	h.Handle(rec, httptest.NewRequest(http.MethodGet, "/api/v1/productos", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestProductoHandleCreateOK201(t *testing.T) {
	svc := &fakeProductoService{}
	h := NewProductoHandler(svc)

	rec := httptest.NewRecorder()
	h.Handle(rec, jsonRequest(http.MethodPost, "/api/v1/productos", productoBodyValido))

	require.Equal(t, http.StatusCreated, rec.Code)
	var env struct {
		Success bool              `json:"success"`
		Data    producto.Producto `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.True(t, env.Success)
	assert.Equal(t, "Tostada Integral", env.Data.Nombre)
}

func TestProductoHandleCreateValidacionFallida422(t *testing.T) {
	svc := &fakeProductoService{
		createFn: func(context.Context, producto.ProductoRequest) (*producto.Producto, error) {
			t.Fatal("el service no debe invocarse si la validación falla")
			return nil, nil
		},
	}
	h := NewProductoHandler(svc)

	rec := httptest.NewRecorder()
	h.Handle(rec, jsonRequest(http.MethodPost, "/api/v1/productos", `{}`))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var env struct {
		Success bool   `json:"success"`
		Errors  string `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.False(t, env.Success)
	assert.Contains(t, env.Errors, "nombre")
}

func TestProductoHandleCreateDuplicado409(t *testing.T) {
	svc := &fakeProductoService{
		createFn: func(context.Context, producto.ProductoRequest) (*producto.Producto, error) {
			return nil, producto.ErrDuplicado
		},
	}
	h := NewProductoHandler(svc)

	rec := httptest.NewRecorder()
	h.Handle(rec, jsonRequest(http.MethodPost, "/api/v1/productos", productoBodyValido))

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestProductoHandleUpdateIDInexistente404(t *testing.T) {
	svc := &fakeProductoService{
		updateFn: func(context.Context, string, producto.ProductoRequest) (*producto.Producto, error) {
			return nil, producto.ErrDesconocido
		},
	}
	h := NewProductoHandler(svc)

	rec := httptest.NewRecorder()
	h.Handle(rec, jsonRequest(http.MethodPut, "/api/v1/productos/00000000-0000-0000-0000-000000000000", productoBodyValido))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestProductoHandleUpdateOKExtraeIDDelPath(t *testing.T) {
	svc := &fakeProductoService{}
	h := NewProductoHandler(svc)

	rec := httptest.NewRecorder()
	h.Handle(rec, jsonRequest(http.MethodPut, "/api/v1/productos/prod-42", productoBodyValido))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "prod-42", svc.lastUpdateID)
}

func TestProductoHandleDeleteOK200(t *testing.T) {
	svc := &fakeProductoService{}
	h := NewProductoHandler(svc)

	rec := httptest.NewRecorder()
	h.Handle(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/productos/prod-9", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "prod-9", svc.lastDeletedID)
}

func TestProductoHandleDeleteInexistente404(t *testing.T) {
	svc := &fakeProductoService{
		deleteFn: func(context.Context, string) error { return producto.ErrDesconocido },
	}
	h := NewProductoHandler(svc)

	rec := httptest.NewRecorder()
	h.Handle(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/productos/prod-9", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestProductoHandleMetodoNoPermitido(t *testing.T) {
	h := NewProductoHandler(&fakeProductoService{})

	rec := httptest.NewRecorder()
	h.Handle(rec, httptest.NewRequest(http.MethodPatch, "/api/v1/productos", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
