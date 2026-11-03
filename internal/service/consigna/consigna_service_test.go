package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/horno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
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

// fakeHornoAlertaRepo implementa horno.Repository (delegando en el fake en
// memoria) y alerta.Repository, para inyectar un único doble en ambos roles.
type fakeHornoAlertaRepo struct {
	*database.PostgresRepository
	mu      sync.Mutex
	alertas []alerta.Alerta
}

func newFakeHornoAlertaRepo() *fakeHornoAlertaRepo {
	return &fakeHornoAlertaRepo{PostgresRepository: database.NewPostgresRepository()}
}

func (f *fakeHornoAlertaRepo) Save(a *alerta.Alerta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a.ID = "alerta-1"
	f.alertas = append(f.alertas, *a)
	return nil
}

func (f *fakeHornoAlertaRepo) GetByHornoID(hornoID string) ([]alerta.Alerta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []alerta.Alerta
	for _, a := range f.alertas {
		if a.HornoID == hornoID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeHornoAlertaRepo) Listar(context.Context, string) ([]alerta.Alerta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alertas, nil
}

func (f *fakeHornoAlertaRepo) Ack(context.Context, string, alerta.Usuario) (*alerta.Alerta, error) {
	return nil, alerta.ErrNotFound
}

const testProductoID = "a1b2c3d4-5678-90ab-cdef-1234567890ab"

// fakeParamRepo es un mock en memoria de producto.Repository.
type fakeParamRepo struct {
	byProducto map[string]producto.Producto
}

func newFakeParamRepo() *fakeParamRepo {
	return &fakeParamRepo{byProducto: make(map[string]producto.Producto)}
}

func (f *fakeParamRepo) List(context.Context) ([]producto.Producto, error) {
	var out []producto.Producto
	for _, p := range f.byProducto {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeParamRepo) GetByID(ctx context.Context, productoID string) (*producto.Producto, error) {
	if p, ok := f.byProducto[productoID]; ok {
		return &p, nil
	}
	return nil, producto.ErrDesconocido
}

func (f *fakeParamRepo) Create(ctx context.Context, p *producto.Producto) (*producto.Producto, error) {
	f.byProducto[p.ID] = *p
	return p, nil
}

func (f *fakeParamRepo) Update(ctx context.Context, p *producto.Producto) (*producto.Producto, error) {
	f.byProducto[p.ID] = *p
	return p, nil
}

func (f *fakeParamRepo) Deactivate(ctx context.Context, id string) error {
	p, ok := f.byProducto[id]
	if !ok {
		return producto.ErrDesconocido
	}
	p.Activo = false
	f.byProducto[id] = p
	return nil
}

func (f *fakeParamRepo) ExistsByNombre(ctx context.Context, nombre string, exceptoID string) (bool, error) {
	return false, nil
}

// baseParametros devuelve un producto válido con rango de control cargado para testProductoID.
func baseParametros() producto.Producto {
	return producto.Producto{
		ID:                testProductoID,
		Nombre:            "Tostada Integral",
		TempMin:           160.00,
		TempMax:           180.00,
		VelocidadCintaMin: 0.10,
		VelocidadCintaMax: 0.30,
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

func (f *fakeConsignaRepo) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

// fakeDecisionesRepo es un doble en memoria del mínimo de decisiones_control
// que consume el service (Save + Aprobar).
type fakeDecisionesRepo struct {
	mu        sync.Mutex
	saved     []decisiones_control.DecisionControl
	aprobadas []string
}

func (f *fakeDecisionesRepo) Save(ctx context.Context, d *decisiones_control.DecisionControl) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d.ID = fmt.Sprintf("dec-%d", len(f.saved)+1)
	f.saved = append(f.saved, *d)
	return nil
}

func (f *fakeDecisionesRepo) Aprobar(ctx context.Context, decisionID, usuario string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aprobadas = append(f.aprobadas, decisionID)
	return nil
}

func (f *fakeDecisionesRepo) snapshot() ([]decisiones_control.DecisionControl, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]decisiones_control.DecisionControl(nil), f.saved...), append([]string(nil), f.aprobadas...)
}

// fakeLoteLookup devuelve siempre el mismo lote (ABIERTO/MANUAL por default).
type fakeLoteLookup struct {
	lote *lote_sector.Lote
	err  error
}

func (f *fakeLoteLookup) GetByID(ctx context.Context, loteID string) (*lote_sector.Lote, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.lote, nil
}

func loteAbiertoManual() *lote_sector.Lote {
	return &lote_sector.Lote{Estado: lote_sector.EstadoAbierto, Modo: lote_sector.ModoManual}
}

// newTestService arma un ConsignaService con el cliente simulado real (~5%
// de fallo aleatorio) — usado por los tests existentes que toleran esa
// variabilidad. fakeHornoAlertaRepo cubre horno.Repository y alerta.Repository.
func newTestService(paramRepo *fakeParamRepo) (*ConsignaService, *fakeHornoAlertaRepo, *fakeConsignaRepo) {
	hornoRepo := newFakeHornoAlertaRepo()
	consignaRepo := &fakeConsignaRepo{}
	ctrl := oven_controller.NewSimulatedOvenController("http://localhost:8600/plc/horno")
	broker := sse.NewBroker()
	svc := NewConsignaService(consignaRepo, hornoRepo, paramRepo, ctrl, broker, hornoRepo,
		&fakeDecisionesRepo{}, &fakeLoteLookup{lote: loteAbiertoManual()}, true)
	return svc, hornoRepo, consignaRepo
}

// newTestServiceWithController arma un ConsignaService con un OvenController
// determinista (fakeController), para probar rutas de éxito/fallo sin depender
// del azar del cliente simulado real.
func newTestServiceWithController(paramRepo *fakeParamRepo, ctrl OvenController) (*ConsignaService, *fakeHornoAlertaRepo, *fakeConsignaRepo) {
	hornoRepo := newFakeHornoAlertaRepo()
	consignaRepo := &fakeConsignaRepo{}
	broker := sse.NewBroker()
	svc := NewConsignaService(consignaRepo, hornoRepo, paramRepo, ctrl, broker, hornoRepo,
		&fakeDecisionesRepo{}, &fakeLoteLookup{lote: loteAbiertoManual()}, true)
	return svc, hornoRepo, consignaRepo
}

// newTestServiceWithOverride arma un service determinista con los fakes de
// decisiones/lote expuestos, para verificar el encolado del override manual.
func newTestServiceWithOverride(paramRepo *fakeParamRepo, lote *lote_sector.Lote) (*ConsignaService, *fakeConsignaRepo, *fakeDecisionesRepo) {
	hornoRepo := newFakeHornoAlertaRepo()
	consignaRepo := &fakeConsignaRepo{}
	decisionesRepo := &fakeDecisionesRepo{}
	ctrl := &fakeController{result: oven_controller.DispatchResult{Aplicada: true, TemperaturaReal: 170, VelocidadReal: 0.20}}
	svc := NewConsignaService(consignaRepo, hornoRepo, paramRepo, ctrl, sse.NewBroker(), hornoRepo,
		decisionesRepo, &fakeLoteLookup{lote: lote}, true)
	return svc, consignaRepo, decisionesRepo
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
	svc, consignaRepo, decisionesRepo := newTestServiceWithOverride(paramRepo, loteAbiertoManual())

	loteID := "lote-correlacion-1"
	req := consigna.ConsignaManualRequest{
		HornoID:                "horno-01",
		ProductoID:             testProductoID,
		TemperaturaObjetivo:    170.0,
		VelocidadCintaObjetivo: 0.20,
		LoteID:                 &loteID,
	}

	rec, err := svc.DispatchManual(context.Background(), req)
	if err != nil {
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

	decisiones, aprobadas := decisionesRepo.snapshot()
	if len(decisiones) != 1 {
		t.Fatalf("expected 1 decisión encolada, got %d", len(decisiones))
	}
	if decisiones[0].LoteID != loteID {
		t.Errorf("expected decision para lote %q, got %q", loteID, decisiones[0].LoteID)
	}
	if decisiones[0].TempPropuesto != 170.0 || decisiones[0].VelPropuesto != 0.20 {
		t.Errorf("expected propuesta temp=170 vel=0.20, got temp=%.2f vel=%.2f",
			decisiones[0].TempPropuesto, decisiones[0].VelPropuesto)
	}
	if len(aprobadas) != 1 || aprobadas[0] != decisiones[0].ID {
		t.Errorf("expected la decisión %q aprobada, got %v", decisiones[0].ID, aprobadas)
	}
}

func TestDispatchManual_ConLoteID_NoManual_NoEncolaDecision(t *testing.T) {
	paramRepo := newFakeParamRepo()
	paramRepo.byProducto[testProductoID] = baseParametros()
	lote := loteAbiertoManual()
	lote.Modo = lote_sector.ModoAutomatico
	svc, _, decisionesRepo := newTestServiceWithOverride(paramRepo, lote)

	loteID := "lote-automatico-1"
	req := consigna.ConsignaManualRequest{
		HornoID:                "horno-01",
		ProductoID:             testProductoID,
		TemperaturaObjetivo:    170.0,
		VelocidadCintaObjetivo: 0.20,
		LoteID:                 &loteID,
	}

	if _, err := svc.DispatchManual(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decisiones, aprobadas := decisionesRepo.snapshot(); len(decisiones) != 0 || len(aprobadas) != 0 {
		t.Fatalf("un lote no MANUAL no debe encolar override, got decisiones=%d aprobadas=%d",
			len(decisiones), len(aprobadas))
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
	if alertas[0].Severidad != alerta.SeveridadCritical {
		t.Errorf("expected severidad CRITICAL, got %s", alertas[0].Severidad)
	}
	if alertas[0].Tipo != alerta.TipoConsignaEnlace {
		t.Errorf("expected tipo %s, got %s", alerta.TipoConsignaEnlace, alertas[0].Tipo)
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
	okSvc := NewConsignaService(consignaRepo, hornoRepo, paramRepo, okCtrl, broker, hornoRepo, nil, nil, false)

	_, err = okSvc.DispatchManual(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	h2, _ := hornoRepo.GetByID("horno-01")
	if h2.Estado != "ACTIVO" {
		t.Errorf("expected Estado ACTIVO tras despacho exitoso, got %s", h2.Estado)
	}
}
