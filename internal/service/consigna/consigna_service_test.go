package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	parametrosproducto "github.com/angelobenedetti29/smart-check-automation/internal/domain/parametros_producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/oven_controller"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

const testProductoID = "a1b2c3d4-5678-90ab-cdef-1234567890ab"

// fakeParamRepo es un mock en memoria de parametros_producto.Repository.
type fakeParamRepo struct {
	byProducto map[string]parametrosproducto.ParametroProducto
}

func newFakeParamRepo() *fakeParamRepo {
	return &fakeParamRepo{byProducto: make(map[string]parametrosproducto.ParametroProducto)}
}

func (f *fakeParamRepo) GetAll(ctx context.Context) ([]parametrosproducto.ParametroProducto, error) {
	var out []parametrosproducto.ParametroProducto
	for _, p := range f.byProducto {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeParamRepo) GetByProductoID(ctx context.Context, productoID string) (*parametrosproducto.ParametroProducto, error) {
	if p, ok := f.byProducto[productoID]; ok {
		return &p, nil
	}
	return nil, parametrosproducto.ErrNotFound
}

func (f *fakeParamRepo) Create(ctx context.Context, p *parametrosproducto.ParametroProducto) error {
	f.byProducto[p.ProductoID] = *p
	return nil
}

func (f *fakeParamRepo) Update(ctx context.Context, p *parametrosproducto.ParametroProducto) error {
	f.byProducto[p.ProductoID] = *p
	return nil
}

func floatPtr(v float64) *float64 { return &v }

// baseParametros devuelve un set de parámetros válido con setpoint cargado para testProductoID.
func baseParametros() parametrosproducto.ParametroProducto {
	return parametrosproducto.ParametroProducto{
		ProductoID:             testProductoID,
		TempMin:                160.00,
		TempMax:                180.00,
		VelocidadCintaMin:      0.10,
		VelocidadCintaMax:      0.30,
		TempSetpoint:           floatPtr(170.00),
		VelocidadCintaSetpoint: floatPtr(0.20),
	}
}

// fakeConsignaRepo es un mock en memoria de consigna.Repository.
type fakeConsignaRepo struct {
	mu    sync.Mutex
	saved []consigna.Consigna
}

func (f *fakeConsignaRepo) Save(ctx context.Context, c *consigna.Consigna) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c.ID = "consigna-1"
	f.saved = append(f.saved, *c)
	return nil
}

func (f *fakeConsignaRepo) GetByLoteID(ctx context.Context, loteID string) ([]consigna.Consigna, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []consigna.Consigna
	for _, c := range f.saved {
		if c.LoteID != nil && *c.LoteID == loteID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeConsignaRepo) GetByHornoID(ctx context.Context, hornoID string, limit int) ([]consigna.Consigna, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []consigna.Consigna
	for _, c := range f.saved {
		if c.HornoID == hornoID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeConsignaRepo) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

func newTestService(paramRepo *fakeParamRepo) (*ConsignaService, *database.PostgresRepository, *fakeConsignaRepo) {
	hornoRepo := database.NewPostgresRepository()
	consignaRepo := &fakeConsignaRepo{}
	ctrl := oven_controller.NewOvenControllerClient("http://localhost:8600/plc/horno")
	broker := sse.NewBroker()
	svc := NewConsignaService(consignaRepo, hornoRepo, paramRepo, ctrl, broker)
	return svc, hornoRepo, consignaRepo
}

func TestDispatchAutomatico_SinSetpointCargado(t *testing.T) {
	paramRepo := newFakeParamRepo()
	p := baseParametros()
	p.TempSetpoint = nil
	paramRepo.byProducto[testProductoID] = p

	svc, _, consignaRepo := newTestService(paramRepo)

	_, err := svc.DispatchAutomatico(context.Background(), "horno-01", "lote-1", testProductoID)
	if !errors.Is(err, consigna.ErrParametrosNoExiste) {
		t.Fatalf("expected ErrParametrosNoExiste, got %v", err)
	}
	if consignaRepo.count() != 0 {
		t.Fatalf("no debería auditarse un intento que ni siquiera llega al controlador")
	}
}

func TestDispatchAutomatico_ProductoSinParametros(t *testing.T) {
	paramRepo := newFakeParamRepo()
	svc, _, _ := newTestService(paramRepo)

	_, err := svc.DispatchAutomatico(context.Background(), "horno-01", "lote-1", "producto-inexistente")
	if !errors.Is(err, consigna.ErrParametrosNoExiste) {
		t.Fatalf("expected ErrParametrosNoExiste, got %v", err)
	}
}

func TestDispatchAutomatico_HornoInexistente(t *testing.T) {
	paramRepo := newFakeParamRepo()
	paramRepo.byProducto[testProductoID] = baseParametros()
	svc, _, _ := newTestService(paramRepo)

	_, err := svc.DispatchAutomatico(context.Background(), "horno-inexistente", "lote-1", testProductoID)
	if !errors.Is(err, consigna.ErrHornoNoExiste) {
		t.Fatalf("expected ErrHornoNoExiste, got %v", err)
	}
}

func TestDispatchManual_FueraDeRango(t *testing.T) {
	paramRepo := newFakeParamRepo()
	paramRepo.byProducto[testProductoID] = baseParametros()
	svc, _, consignaRepo := newTestService(paramRepo)

	req := consigna.ConsignaManualRequest{
		HornoID:                "horno-01",
		ProductoID:             testProductoID,
		TemperaturaObjetivo:    999.0, // fuera de [160, 180]
		VelocidadCintaObjetivo: 0.20,
	}

	_, err := svc.DispatchManual(context.Background(), req)
	if !errors.Is(err, consigna.ErrFueraDeRango) {
		t.Fatalf("expected ErrFueraDeRango, got %v", err)
	}
	if consignaRepo.count() != 0 {
		t.Fatalf("un valor fuera de rango no debería llegar a auditarse/despacharse")
	}
}

func TestDispatchManual_DentroDeRango_ActualizaHornoYAudita(t *testing.T) {
	paramRepo := newFakeParamRepo()
	paramRepo.byProducto[testProductoID] = baseParametros()
	svc, hornoRepo, consignaRepo := newTestService(paramRepo)

	req := consigna.ConsignaManualRequest{
		HornoID:                "horno-01",
		ProductoID:             testProductoID,
		TemperaturaObjetivo:    170.0,
		VelocidadCintaObjetivo: 0.20,
		Usuario:                "operario-demo",
	}

	rec, err := svc.DispatchManual(context.Background(), req)
	// El controlador simulado falla ~5% de las veces; ambas ramas deben auditar.
	if err != nil && !errors.Is(err, consigna.ErrDispatchFallido) {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Origen != consigna.OrigenManual {
		t.Errorf("expected origen MANUAL, got %s", rec.Origen)
	}
	if consignaRepo.count() != 1 {
		t.Fatalf("expected 1 registro de auditoría, got %d", consignaRepo.count())
	}

	if err == nil {
		h, getErr := hornoRepo.GetByID("horno-01")
		if getErr != nil {
			t.Fatalf("unexpected error fetching horno: %v", getErr)
		}
		if h.Temperatura != 170.0 || h.VelocidadCinta != 0.20 {
			t.Errorf("expected horno state to reflect dispatched setpoint, got temp=%.2f vel=%.2f", h.Temperatura, h.VelocidadCinta)
		}
	}
}
