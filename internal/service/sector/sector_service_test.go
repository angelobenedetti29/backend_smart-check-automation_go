package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
)

// fakeSectorRepo implementa sector.Repository para los tests del service.
type fakeSectorRepo struct {
	list       []sector.Sector
	listErr    error
	createErr  func(attempt int) error
	created    []sector.Sector
	updateErr  error
	updated    *sector.Sector
	deleteErr  error
	deletedID  string
	createCall int
}

func (f *fakeSectorRepo) GetDeviceInfo(context.Context, string) (*sector.DeviceInfo, error) {
	return nil, sector.ErrSinSector
}

func (f *fakeSectorRepo) GetByID(context.Context, string) (*sector.Sector, error) {
	return nil, sector.ErrSinSector
}

func (f *fakeSectorRepo) ListCompaneros(context.Context, string, string) ([]sector.Companero, error) {
	return nil, nil
}

func (f *fakeSectorRepo) List(context.Context) ([]sector.Sector, error) {
	return f.list, f.listErr
}

func (f *fakeSectorRepo) Create(_ context.Context, s *sector.Sector) error {
	f.createCall++
	if f.createErr != nil {
		if err := f.createErr(f.createCall); err != nil {
			return err
		}
	}
	f.created = append(f.created, *s)
	return nil
}

func (f *fakeSectorRepo) Update(_ context.Context, s *sector.Sector) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	cp := *s
	f.updated = &cp
	return nil
}

func (f *fakeSectorRepo) Delete(_ context.Context, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedID = id
	return nil
}

func TestSectorService_List(t *testing.T) {
	want := []sector.Sector{{ID: "horno-1", Nombre: "Horno 1"}}
	repo := &fakeSectorRepo{list: want}
	svc := NewService(repo)

	got, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "horno-1" {
		t.Fatalf("unexpected list: %+v", got)
	}
}

func TestSectorService_Create_GeneraSlug(t *testing.T) {
	tests := []struct {
		nombre string
		wantID string
	}{
		{"Horno 1", "horno-1"},
		{"Línea A", "linea-a"},
		{"  Sector  Ñandú  ", "sector-nandu"},
		{"!!!", "sector"},
	}
	for _, tt := range tests {
		t.Run(tt.nombre, func(t *testing.T) {
			repo := &fakeSectorRepo{}
			svc := NewService(repo)

			sec, err := svc.Create(context.Background(), sector.CreateSectorRequest{Nombre: tt.nombre})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sec.ID != tt.wantID {
				t.Fatalf("expected id %q, got %q", tt.wantID, sec.ID)
			}
			if sec.Nombre != strings.TrimSpace(tt.nombre) {
				t.Fatalf("expected trimmed nombre, got %q", sec.Nombre)
			}
		})
	}
}

func TestSectorService_Create_ColisionAgregaSufijo(t *testing.T) {
	repo := &fakeSectorRepo{
		createErr: func(attempt int) error {
			if attempt == 1 {
				return sector.ErrSectorIDExists
			}
			return nil
		},
	}
	svc := NewService(repo)

	sec, err := svc.Create(context.Background(), sector.CreateSectorRequest{Nombre: "Horno 1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sec.ID != "horno-1-2" {
		t.Fatalf("expected suffixed id horno-1-2, got %q", sec.ID)
	}
	if repo.createCall != 2 {
		t.Fatalf("expected 2 attempts, got %d", repo.createCall)
	}
}

func TestSectorService_Create_TruncadoNoSupera50(t *testing.T) {
	repo := &fakeSectorRepo{
		createErr: func(attempt int) error {
			if attempt < 3 {
				return sector.ErrSectorIDExists
			}
			return nil
		},
	}
	svc := NewService(repo)

	sec, err := svc.Create(context.Background(), sector.CreateSectorRequest{Nombre: strings.Repeat("a", 60)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len([]rune(sec.ID)) > maxSectorIDLength {
		t.Fatalf("expected id <= %d chars, got %d (%q)", maxSectorIDLength, len([]rune(sec.ID)), sec.ID)
	}
	if !strings.HasSuffix(sec.ID, "-3") {
		t.Fatalf("expected suffixed id ending in -3, got %q", sec.ID)
	}
}

func TestSectorService_Create_ValidaNombreAntesDelRepo(t *testing.T) {
	repo := &fakeSectorRepo{}
	svc := NewService(repo)

	_, err := svc.Create(context.Background(), sector.CreateSectorRequest{Nombre: "   "})
	if !sector.IsValidationError(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
	if repo.createCall != 0 {
		t.Fatalf("invalid request must not hit repo, got %d calls", repo.createCall)
	}
}

func TestSectorService_Update(t *testing.T) {
	repo := &fakeSectorRepo{}
	svc := NewService(repo)

	sec, err := svc.Update(context.Background(), "horno-1", sector.UpdateSectorRequest{Nombre: "  Horno Uno  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sec.ID != "horno-1" || sec.Nombre != "Horno Uno" {
		t.Fatalf("unexpected sector: %+v", sec)
	}
	if repo.updated == nil || repo.updated.ID != "horno-1" || repo.updated.Nombre != "Horno Uno" {
		t.Fatalf("unexpected persisted sector: %+v", repo.updated)
	}
}

func TestSectorService_Update_PropagaNotFound(t *testing.T) {
	repo := &fakeSectorRepo{updateErr: sector.ErrSectorNotFound}
	svc := NewService(repo)

	_, err := svc.Update(context.Background(), "missing", sector.UpdateSectorRequest{Nombre: "X"})
	if !errors.Is(err, sector.ErrSectorNotFound) {
		t.Fatalf("expected ErrSectorNotFound, got %v", err)
	}
}

func TestSectorService_Delete_PropagaConflicto(t *testing.T) {
	repo := &fakeSectorRepo{deleteErr: sector.ErrSectorConLotes}
	svc := NewService(repo)

	err := svc.Delete(context.Background(), "horno-1")
	if !errors.Is(err, sector.ErrSectorConLotes) {
		t.Fatalf("expected ErrSectorConLotes, got %v", err)
	}
}

func TestSectorService_Delete_Ok(t *testing.T) {
	repo := &fakeSectorRepo{}
	svc := NewService(repo)

	if err := svc.Delete(context.Background(), "  horno-1  "); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.deletedID != "horno-1" {
		t.Fatalf("expected trimmed id, got %q", repo.deletedID)
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Horno 1", "horno-1"},
		{"Línea A", "linea-a"},
		{"A--B", "a-b"},
		{"-Horno-", "horno"},
		{"", "sector"},
		{"ÁÉÍÓÚÑÇ", "aeiounc"},
	}
	for _, tt := range tests {
		if got := slugify(tt.in); got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
