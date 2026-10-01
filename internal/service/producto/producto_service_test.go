package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
)

// fakeProductoRepo es un doble configurable de producto.Repository.
type fakeProductoRepo struct {
	list []producto.Producto

	exists    bool
	existsErr error

	createErr error
	updateErr error
	deleteErr error

	created   *producto.Producto
	updated   *producto.Producto
	deletedID string
}

func (f *fakeProductoRepo) List(context.Context) ([]producto.Producto, error) {
	return f.list, nil
}

func (f *fakeProductoRepo) GetByID(context.Context, string) (*producto.Producto, error) {
	return nil, producto.ErrDesconocido
}

func (f *fakeProductoRepo) Create(_ context.Context, p *producto.Producto) (*producto.Producto, error) {
	f.created = p
	if f.createErr != nil {
		return nil, f.createErr
	}
	p.ID = "prod-nuevo"
	return p, nil
}

func (f *fakeProductoRepo) Update(_ context.Context, p *producto.Producto) (*producto.Producto, error) {
	f.updated = p
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return p, nil
}

func (f *fakeProductoRepo) Deactivate(_ context.Context, id string) error {
	f.deletedID = id
	return f.deleteErr
}

func (f *fakeProductoRepo) ExistsByNombre(context.Context, string, string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	return f.exists, nil
}

func reqValido() producto.ProductoRequest {
	return producto.ProductoRequest{
		Nombre:                "Tostada Integral",
		PesoReferenciaKg:      0.25,
		ToleranciaPesoPct:     5.0,
		DimensionBaseCm:       12.0,
		ToleranciaDimensionCm: 0.5,
		TempMin:               180.0,
		TempMax:               220.0,
		VelocidadCintaMin:     0.1,
		VelocidadCintaMax:     0.3,
	}
}

func TestCreate_ExitoNormalizaNombreYPersiste(t *testing.T) {
	repo := &fakeProductoRepo{}
	svc := NewService(repo)

	req := reqValido()
	req.Nombre = "  Tostada Integral  "
	setpoint := 200.0
	req.TempSetpoint = &setpoint

	got, err := svc.Create(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, repo.created)
	assert.Equal(t, "Tostada Integral", repo.created.Nombre)
	assert.Equal(t, "prod-nuevo", got.ID)
	require.NotNil(t, got.TempSetpoint)
	assert.Equal(t, 200.0, *got.TempSetpoint)
}

func TestCreate_Duplicado(t *testing.T) {
	svc := NewService(&fakeProductoRepo{exists: true})

	_, err := svc.Create(context.Background(), reqValido())
	require.ErrorIs(t, err, producto.ErrDuplicado)
}

func TestCreate_ValidacionFallida(t *testing.T) {
	repo := &fakeProductoRepo{}
	svc := NewService(repo)

	req := reqValido()
	req.Nombre = ""
	req.PesoReferenciaKg = 0

	_, err := svc.Create(context.Background(), req)
	var ve *producto.ValidationError
	require.ErrorAs(t, err, &ve)
	require.NotEmpty(t, ve.Fields["nombre"])
	require.NotEmpty(t, ve.Fields["pesoReferenciaKg"])
	assert.Nil(t, repo.created, "no debe llegar al repo si la validación falla")
}

func TestUpdate_ExitoUsaIDDelPathYPersiste(t *testing.T) {
	repo := &fakeProductoRepo{}
	svc := NewService(repo)

	setpoint := 0.2
	req := reqValido()
	req.VelocidadCintaSetpoint = &setpoint

	got, err := svc.Update(context.Background(), "prod-42", req)
	require.NoError(t, err)
	require.NotNil(t, repo.updated)
	assert.Equal(t, "prod-42", repo.updated.ID)
	assert.Equal(t, "prod-42", got.ID)
	require.NotNil(t, got.VelocidadCintaSetpoint)
	assert.Equal(t, 0.2, *got.VelocidadCintaSetpoint)
}

func TestUpdate_IDInexistente(t *testing.T) {
	svc := NewService(&fakeProductoRepo{updateErr: producto.ErrDesconocido})

	_, err := svc.Update(context.Background(), "prod-42", reqValido())
	require.ErrorIs(t, err, producto.ErrDesconocido)
}

func TestUpdate_ValidacionFallidaNoLlegaAlRepo(t *testing.T) {
	repo := &fakeProductoRepo{}
	svc := NewService(repo)

	req := reqValido()
	req.TempMax = 100 // < TempMin

	_, err := svc.Update(context.Background(), "prod-42", req)
	var ve *producto.ValidationError
	require.ErrorAs(t, err, &ve)
	require.NotEmpty(t, ve.Fields["tempMax"])
	assert.Nil(t, repo.updated)
}

func TestDeactivate_Exito(t *testing.T) {
	repo := &fakeProductoRepo{}
	svc := NewService(repo)

	require.NoError(t, svc.Deactivate(context.Background(), "prod-9"))
	assert.Equal(t, "prod-9", repo.deletedID)
}

func TestDeactivate_IDInexistente(t *testing.T) {
	svc := NewService(&fakeProductoRepo{deleteErr: producto.ErrDesconocido})

	err := svc.Deactivate(context.Background(), "prod-9")
	require.ErrorIs(t, err, producto.ErrDesconocido)
}

func TestCreate_ErrorDeUnicidadDelRepo(t *testing.T) {
	svc := NewService(&fakeProductoRepo{
		createErr: errors.New("boom"),
	})

	_, err := svc.Create(context.Background(), reqValido())
	require.Error(t, err)
}
