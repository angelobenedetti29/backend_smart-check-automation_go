package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
)

func TestSectorRepository_CRUD(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)
	repo := NewSectorPostgresRepository(pool)

	// Create + GetByID.
	sec := &sector.Sector{ID: "horno-x", Nombre: "Horno X"}
	require.NoError(t, repo.Create(ctx, sec))

	got, err := repo.GetByID(ctx, "horno-x")
	require.NoError(t, err)
	require.Equal(t, "Horno X", got.Nombre)

	// List lo incluye.
	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "horno-x", list[0].ID)

	// Id duplicado -> ErrSectorIDExists.
	require.ErrorIs(t, repo.Create(ctx, &sector.Sector{ID: "horno-x", Nombre: "Otro"}), sector.ErrSectorIDExists)

	// Update cambia el nombre.
	require.NoError(t, repo.Update(ctx, &sector.Sector{ID: "horno-x", Nombre: "Horno X Renombrado"}))
	got, err = repo.GetByID(ctx, "horno-x")
	require.NoError(t, err)
	require.Equal(t, "Horno X Renombrado", got.Nombre)

	// Update de inexistente.
	require.ErrorIs(t, repo.Update(ctx, &sector.Sector{ID: "missing", Nombre: "X"}), sector.ErrSectorNotFound)

	// Delete de inexistente.
	require.ErrorIs(t, repo.Delete(ctx, "missing"), sector.ErrSectorNotFound)

	// Delete del existente.
	require.NoError(t, repo.Delete(ctx, "horno-x"))
	_, err = repo.GetByID(ctx, "horno-x")
	require.ErrorIs(t, err, sector.ErrSinSector)
}

func TestSectorRepository_DeleteBloqueadoConLotes(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)
	repo := NewSectorPostgresRepository(pool)

	require.NoError(t, repo.Create(ctx, &sector.Sector{ID: "sector-lotes", Nombre: "Sector Lotes"}))

	const productoID = "aaaaaaaa-0000-0000-0000-0000000000aa"
	_, err := pool.Exec(ctx, `INSERT INTO productos (id, nombre) VALUES ($1, 'Producto Lotes')`, productoID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO lotes_productivos (producto_id, inicio_at, estado, sector_id)
		VALUES ($1, now(), 'ABIERTO', 'sector-lotes')`, productoID)
	require.NoError(t, err)

	require.ErrorIs(t, repo.Delete(ctx, "sector-lotes"), sector.ErrSectorConLotes)

	// El sector sigue existiendo tras el intento.
	var exists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sectores WHERE id='sector-lotes')`).Scan(&exists))
	require.True(t, exists)
}

func TestDispositivoRepository_AsignaSector(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t)
	repo := NewPostgresDispositivoRepository(pool)
	sectorRepo := NewSectorPostgresRepository(pool)

	require.NoError(t, sectorRepo.Create(ctx, &sector.Sector{ID: "horno-a", Nombre: "Horno A"}))
	require.NoError(t, sectorRepo.Create(ctx, &sector.Sector{ID: "horno-b", Nombre: "Horno B"}))

	tipoEntrada := "ENTRADA_HORNO"
	sectorA := "horno-a"

	// Alta con sector válido.
	d := dispositivo.Dispositivo{Nombre: "pi-entrada-a", Tipo: &tipoEntrada, SectorID: &sectorA}
	require.NoError(t, repo.Create(ctx, &d))
	got, err := repo.GetDispositivoByID(ctx, d.ID)
	require.NoError(t, err)
	require.NotNil(t, got.SectorID)
	require.Equal(t, "horno-a", *got.SectorID)

	// Segundo dispositivo del mismo tipo en el mismo sector -> 409 de dominio.
	d2 := dispositivo.Dispositivo{Nombre: "pi-entrada-a2", Tipo: &tipoEntrada, SectorID: &sectorA}
	require.ErrorIs(t, repo.Create(ctx, &d2), dispositivo.ErrSectorTipoDuplicado)

	// Sector inexistente -> error de dominio.
	sectorInexistente := "no-existe"
	d3 := dispositivo.Dispositivo{Nombre: "pi-x", Tipo: &tipoEntrada, SectorID: &sectorInexistente}
	require.ErrorIs(t, repo.Create(ctx, &d3), dispositivo.ErrSectorNotFound)

	// Update: desasignar y reasignar a otro sector.
	got.SectorID = nil
	require.NoError(t, repo.Update(ctx, got))
	got, err = repo.GetDispositivoByID(ctx, d.ID)
	require.NoError(t, err)
	require.Nil(t, got.SectorID)

	sectorB := "horno-b"
	got.SectorID = &sectorB
	require.NoError(t, repo.Update(ctx, got))

	// Un SALIDA en horno-b convive con el ENTRADA.
	tipoSalida := "SALIDA_HORNO"
	d4 := dispositivo.Dispositivo{Nombre: "pi-salida-b", Tipo: &tipoSalida, SectorID: &sectorB}
	require.NoError(t, repo.Create(ctx, &d4))

	// Un segundo SALIDA en horno-b es conflicto.
	d5 := dispositivo.Dispositivo{Nombre: "pi-salida-b2", Tipo: &tipoSalida, SectorID: &sectorB}
	require.ErrorIs(t, repo.Create(ctx, &d5), dispositivo.ErrSectorTipoDuplicado)

	// Update a sector inexistente.
	got.SectorID = &sectorInexistente
	require.ErrorIs(t, repo.Update(ctx, got), dispositivo.ErrSectorNotFound)
}
