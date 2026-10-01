package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
)

// nuevoProductoDePrueba arma un producto válido (cumple los CHECK constraints).
func nuevoProductoDePrueba(nombre string) *producto.Producto {
	return &producto.Producto{
		Nombre:                nombre,
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

func TestProductoRepository_CRUDYSetpoints(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)
	repo := NewProductoPostgresRepository(pool)

	// Create con setpoints.
	setpointTemp := 200.0
	setpointVel := 0.2
	p := nuevoProductoDePrueba("Producto Repo Test")
	p.TempSetpoint = &setpointTemp
	p.VelocidadCintaSetpoint = &setpointVel

	creado, err := repo.Create(ctx, p)
	require.NoError(t, err)
	require.NotEmpty(t, creado.ID)
	require.True(t, creado.Activo)
	require.False(t, creado.CreatedAt.IsZero())
	require.False(t, creado.UpdatedAt.IsZero())

	// GetByID trae numericos y setpoints.
	got, err := repo.GetByID(ctx, creado.ID)
	require.NoError(t, err)
	assert.Equal(t, "Producto Repo Test", got.Nombre)
	assert.Equal(t, 0.25, got.PesoReferenciaKg)
	assert.Equal(t, 180.0, got.TempMin)
	require.NotNil(t, got.TempSetpoint)
	assert.Equal(t, 200.0, *got.TempSetpoint)
	require.NotNil(t, got.VelocidadCintaSetpoint)
	assert.Equal(t, 0.2, *got.VelocidadCintaSetpoint)

	// List lo incluye.
	list, err := repo.List(ctx)
	require.NoError(t, err)
	var encontrado bool
	for _, item := range list {
		if item.ID == creado.ID {
			encontrado = true
		}
	}
	assert.True(t, encontrado, "List debe incluir el producto creado")

	// ExistsByNombre es case-insensitive y excluye el propio id.
	ocupado, err := repo.ExistsByNombre(ctx, "producto repo test", "")
	require.NoError(t, err)
	assert.True(t, ocupado)
	ocupado, err = repo.ExistsByNombre(ctx, "producto repo test", creado.ID)
	require.NoError(t, err)
	assert.False(t, ocupado, "no debe considerarse duplicado contra sí mismo")

	// Update cambia nombre, rangos y limpia el setpoint de velocidad (nil -> NULL).
	p2 := nuevoProductoDePrueba("Producto Repo Test Editado")
	p2.ID = creado.ID
	nuevoSetpoint := 210.0
	p2.TempSetpoint = &nuevoSetpoint
	p2.VelocidadCintaSetpoint = nil

	actualizado, err := repo.Update(ctx, p2)
	require.NoError(t, err)
	assert.Equal(t, "Producto Repo Test Editado", actualizado.Nombre)
	require.NotNil(t, actualizado.TempSetpoint)
	assert.Equal(t, 210.0, *actualizado.TempSetpoint)
	assert.Nil(t, actualizado.VelocidadCintaSetpoint)

	// Update de inexistente.
	inexistente := nuevoProductoDePrueba("No Existe")
	inexistente.ID = "00000000-0000-0000-0000-000000000000"
	_, err = repo.Update(ctx, inexistente)
	require.ErrorIs(t, err, producto.ErrDesconocido)

	// GetByID de inexistente y de id no-UUID.
	_, err = repo.GetByID(ctx, "00000000-0000-0000-0000-000000000000")
	require.ErrorIs(t, err, producto.ErrDesconocido)
	_, err = repo.GetByID(ctx, "no-es-uuid")
	require.ErrorIs(t, err, producto.ErrDesconocido)

	// Deactivate marca activo=false.
	require.NoError(t, repo.Deactivate(ctx, creado.ID))
	desactivado, err := repo.GetByID(ctx, creado.ID)
	require.NoError(t, err)
	assert.False(t, desactivado.Activo)

	// Deactivate de inexistente.
	require.ErrorIs(t, repo.Deactivate(ctx, "00000000-0000-0000-0000-000000000000"), producto.ErrDesconocido)
}
