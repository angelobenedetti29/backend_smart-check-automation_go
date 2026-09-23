package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/horno"
	parametrosproducto "github.com/angelobenedetti29/smart-check-automation/internal/domain/parametros_producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/oven_controller"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// fakeController es un doble de prueba determinista de OvenController: en vez
// del ~5% de fallo aleatorio del cliente simulado real, siempre devuelve el
// DispatchResult configurado — necesario para probar la rama de fallo de
// enlace (alerta crítica + CONTROL_MANUAL) sin depender del azar.
type fakeController struct {
	result oven_controller.DispatchResult
	calls  int
}

func (f *fakeController) SendSetpoint(hornoID string, temperatura, velocidad float64) oven_controller.DispatchResult {
	f.calls++
	return f.result
}

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

// newTestService arma un ConsignaService con el cliente simulado real (~5%
// de fallo aleatorio) — usado por los tests existentes que toleran esa
// variabilidad. database.PostgresRepository (en memoria) implementa tanto
// horno.Repository como alerta.Repository, así que se reutiliza para ambos.
func newTestService(paramRepo *fakeParamRepo) (*ConsignaService, *database.PostgresRepository, *fakeConsignaRepo) {
	hornoRepo := database.NewPostgresRepository()
	consignaRepo := &fakeConsignaRepo{}
	ctrl := oven_controller.NewOvenControllerClient("http://localhost:8600/plc/horno")
	broker := sse.NewBroker()
	svc := NewConsignaService(consignaRepo, hornoRepo, paramRepo, ctrl, broker, hornoRepo)
	return svc, hornoRepo, consignaRepo
}

// newTestServiceWithController arma un ConsignaService con un OvenController
// determinista (fakeController), para probar rutas de éxito/fallo sin depender
// del azar del cliente simulado real.
func newTestServiceWithController(paramRepo *fakeParamRepo, ctrl OvenController) (*ConsignaService, *database.PostgresRepository, *fakeConsignaRepo) {
	hornoRepo := database.NewPostgresRepository()
	consignaRepo := &fakeConsignaRepo{}
	broker := sse.NewBroker()
	svc := NewConsignaService(consignaRepo, hornoRepo, paramRepo, ctrl, broker, hornoRepo)
	return svc, hornoRepo, consignaRepo
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

func TestDispatchManual_ConLoteID_QuedaCorrelacionadoEnHistorial(t *testing.T) {
	paramRepo := newFakeParamRepo()
	paramRepo.byProducto[testProductoID] = baseParametros()
	svc, _, consignaRepo := newTestService(paramRepo)

	loteID := "lote-correlacion-1"
	req := consigna.ConsignaManualRequest{
		HornoID:                "horno-01",
		ProductoID:             testProductoID,
		TemperaturaObjetivo:    170.0,
		VelocidadCintaObjetivo: 0.20,
		LoteID:                 &loteID,
	}

	rec, err := svc.DispatchManual(context.Background(), req)
	if err != nil && !errors.Is(err, consigna.ErrDispatchFallido) {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.LoteID == nil || *rec.LoteID != loteID {
		t.Fatalf("expected LoteID %q en el registro, got %v", loteID, rec.LoteID)
	}

	historial, err := consignaRepo.GetByLoteID(context.Background(), loteID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(historial) != 1 {
		t.Fatalf("expected 1 registro en el historial del lote, got %d", len(historial))
	}
	if historial[0].Origen != consigna.OrigenManual {
		t.Errorf("expected origen MANUAL en el historial, got %s", historial[0].Origen)
	}
}

func TestDispatchManual_SinLoteID_NoQuedaAsociadoANingunLote(t *testing.T) {
	paramRepo := newFakeParamRepo()
	paramRepo.byProducto[testProductoID] = baseParametros()
	svc, _, _ := newTestService(paramRepo)

	req := consigna.ConsignaManualRequest{
		HornoID:                "horno-01",
		ProductoID:             testProductoID,
		TemperaturaObjetivo:    170.0,
		VelocidadCintaObjetivo: 0.20,
	}

	rec, err := svc.DispatchManual(context.Background(), req)
	if err != nil && !errors.Is(err, consigna.ErrDispatchFallido) {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.LoteID != nil {
		t.Errorf("expected LoteID nil cuando no se especifica en el request, got %v", *rec.LoteID)
	}
}

func TestDispatch_Fallido_GeneraAlertaCriticaYPasaAControlManual(t *testing.T) {
	paramRepo := newFakeParamRepo()
	paramRepo.byProducto[testProductoID] = baseParametros()
	failing := &fakeController{result: oven_controller.DispatchResult{Aplicada: false, Motivo: "timeout simulado del enlace"}}
	svc, hornoRepo, consignaRepo := newTestServiceWithController(paramRepo, failing)

	req := consigna.ConsignaManualRequest{
		HornoID:                "horno-01",
		ProductoID:             testProductoID,
		TemperaturaObjetivo:    170.0,
		VelocidadCintaObjetivo: 0.20,
	}

	_, err := svc.DispatchManual(context.Background(), req)
	if !errors.Is(err, consigna.ErrDispatchFallido) {
		t.Fatalf("expected ErrDispatchFallido, got %v", err)
	}
	if consignaRepo.count() != 1 {
		t.Fatalf("expected el intento fallido auditado, got %d registros", consignaRepo.count())
	}

	h, getErr := hornoRepo.GetByID("horno-01")
	if getErr != nil {
		t.Fatalf("unexpected error: %v", getErr)
	}
	if h.Estado != horno.EstadoControlManual {
		t.Errorf("expected Estado CONTROL_MANUAL tras el fallo, got %s", h.Estado)
	}

	alertas, alertaErr := hornoRepo.GetByHornoID("horno-01")
	if alertaErr != nil {
		t.Fatalf("unexpected error: %v", alertaErr)
	}
	if len(alertas) != 1 {
		t.Fatalf("expected 1 alerta crítica generada, got %d", len(alertas))
	}
	if alertas[0].Nivel != "CRITICAL" {
		t.Errorf("expected nivel CRITICAL, got %s", alertas[0].Nivel)
	}
}

func TestDispatchManual_NoSeBloqueaAunqueHornoEsteEnControlManual(t *testing.T) {
	paramRepo := newFakeParamRepo()
	paramRepo.byProducto[testProductoID] = baseParametros()
	svc, hornoRepo, _ := newTestServiceWithController(paramRepo, &fakeController{result: oven_controller.DispatchResult{Aplicada: true, TemperaturaReal: 170, VelocidadReal: 0.20}})

	h, _ := hornoRepo.GetByID("horno-01")
	h.Estado = horno.EstadoControlManual
	_ = hornoRepo.Update(h)

	req := consigna.ConsignaManualRequest{
		HornoID:                "horno-01",
		ProductoID:             testProductoID,
		TemperaturaObjetivo:    170.0,
		VelocidadCintaObjetivo: 0.20,
	}

	rec, err := svc.DispatchManual(context.Background(), req)
	if err != nil {
		t.Fatalf("el envío manual no debería bloquearse en CONTROL_MANUAL: %v", err)
	}
	if !rec.Exitosa {
		t.Errorf("expected consigna exitosa, got Exitosa=false")
	}
}

func TestDispatchManual_ExitosoTrasFalloRestauraEstadoActivo(t *testing.T) {
	paramRepo := newFakeParamRepo()
	paramRepo.byProducto[testProductoID] = baseParametros()

	failingCtrl := &fakeController{result: oven_controller.DispatchResult{Aplicada: false, Motivo: "timeout simulado"}}
	failingSvc, hornoRepo, consignaRepo := newTestServiceWithController(paramRepo, failingCtrl)

	req := consigna.ConsignaManualRequest{
		HornoID:                "horno-01",
		ProductoID:             testProductoID,
		TemperaturaObjetivo:    170.0,
		VelocidadCintaObjetivo: 0.20,
	}

	_, err := failingSvc.DispatchManual(context.Background(), req)
	if !errors.Is(err, consigna.ErrDispatchFallido) {
		t.Fatalf("expected ErrDispatchFallido, got %v", err)
	}
	h, _ := hornoRepo.GetByID("horno-01")
	if h.Estado != horno.EstadoControlManual {
		t.Fatalf("precondición fallida: esperaba CONTROL_MANUAL, got %s", h.Estado)
	}

	// Reutiliza el mismo hornoRepo/consignaRepo/paramRepo, pero con un
	// controller exitoso, para simular que el operario reactivó el enlace.
	okCtrl := &fakeController{result: oven_controller.DispatchResult{Aplicada: true, TemperaturaReal: 170, VelocidadReal: 0.20}}
	broker := sse.NewBroker()
	okSvc := NewConsignaService(consignaRepo, hornoRepo, paramRepo, okCtrl, broker, hornoRepo)

	_, err = okSvc.DispatchManual(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	h2, _ := hornoRepo.GetByID("horno-01")
	if h2.Estado != "ACTIVO" {
		t.Errorf("expected Estado ACTIVO tras despacho exitoso, got %s", h2.Estado)
	}
}
