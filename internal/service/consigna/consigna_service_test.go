package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

const testProductoID = "a1b2c3d4-5678-90ab-cdef-1234567890ab"

// fakeProductoRepo es un mock en memoria de producto.Repository.
type fakeProductoRepo struct {
	byID map[string]producto.Producto
}

func newFakeProductoRepo() *fakeProductoRepo {
	return &fakeProductoRepo{byID: make(map[string]producto.Producto)}
}

func (f *fakeProductoRepo) List(context.Context) ([]producto.Producto, error) {
	out := make([]producto.Producto, 0, len(f.byID))
	for _, p := range f.byID {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeProductoRepo) GetByID(_ context.Context, id string) (*producto.Producto, error) {
	if p, ok := f.byID[id]; ok {
		return &p, nil
	}
	return nil, producto.ErrDesconocido
}

func (f *fakeProductoRepo) Create(_ context.Context, p *producto.Producto) (*producto.Producto, error) {
	f.byID[p.ID] = *p
	return p, nil
}

func (f *fakeProductoRepo) Update(_ context.Context, p *producto.Producto) (*producto.Producto, error) {
	f.byID[p.ID] = *p
	return p, nil
}

func (f *fakeProductoRepo) Deactivate(_ context.Context, id string) error {
	p, ok := f.byID[id]
	if !ok {
		return producto.ErrDesconocido
	}
	p.Activo = false
	f.byID[id] = p
	return nil
}

func (f *fakeProductoRepo) ExistsByNombre(context.Context, string, string) (bool, error) {
	return false, nil
}

func productoValido() producto.Producto {
	return producto.Producto{
		ID:                testProductoID,
		Nombre:            "Tostada Integral",
		TempMin:           160,
		TempMax:           180,
		VelocidadCintaMin: 0.10,
		VelocidadCintaMax: 0.30,
	}
}

// fakeConsignaRepo es un mock en memoria de consigna.Repository.
type fakeConsignaRepo struct {
	mu      sync.Mutex
	saved   []consigna.Consigna
	saveErr error
}

func (f *fakeConsignaRepo) Save(_ context.Context, c *consigna.Consigna) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	c.ID = "consigna-1"
	f.saved = append(f.saved, *c)
	return nil
}

func (f *fakeConsignaRepo) GetByLoteID(_ context.Context, loteID string) ([]consigna.Consigna, error) {
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

func (f *fakeConsignaRepo) MarcarAplicadaPorDecision(context.Context, string) error { return nil }

func (f *fakeConsignaRepo) all() []consigna.Consigna {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]consigna.Consigna(nil), f.saved...)
}

func (f *fakeConsignaRepo) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saved)
}

// fakeDecisionesRepo es un doble en memoria del mínimo de decisiones_control
// que consume el service (Save + Aprobar).
type fakeDecisionesRepo struct {
	mu         sync.Mutex
	saved      []decisiones_control.DecisionControl
	aprobadas  []string
	saveErr    error
	aprobarErr error
}

func (f *fakeDecisionesRepo) Save(_ context.Context, d *decisiones_control.DecisionControl) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	d.ID = fmt.Sprintf("dec-%d", len(f.saved)+1)
	f.saved = append(f.saved, *d)
	return nil
}

func (f *fakeDecisionesRepo) Aprobar(_ context.Context, decisionID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.aprobarErr != nil {
		return f.aprobarErr
	}
	f.aprobadas = append(f.aprobadas, decisionID)
	return nil
}

func (f *fakeDecisionesRepo) snapshot() ([]decisiones_control.DecisionControl, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]decisiones_control.DecisionControl(nil), f.saved...), append([]string(nil), f.aprobadas...)
}

// fakeLoteLookup resuelve el lote configurado por id.
type fakeLoteLookup struct {
	lotes map[string]*lote_sector.Lote
	err   error
}

func (f *fakeLoteLookup) GetByID(_ context.Context, loteID string) (*lote_sector.Lote, error) {
	if f.err != nil {
		return nil, f.err
	}
	if l, ok := f.lotes[loteID]; ok {
		return l, nil
	}
	return nil, lote_sector.ErrNotFound
}

// fakeMetricasLookup devuelve la telemetría configurada para cualquier lote.
type fakeMetricasLookup struct {
	muestras []metricashorno.MetricaHorno
	err      error
}

func (f *fakeMetricasLookup) GetUltimasParaLote(context.Context, string, int) ([]metricashorno.MetricaHorno, error) {
	return f.muestras, f.err
}

func loteAbiertoManual() *lote_sector.Lote {
	return &lote_sector.Lote{
		ID:       "lote-1",
		SectorID: "sector-1",
		Estado:   lote_sector.EstadoAbierto,
		Modo:     lote_sector.ModoManual,
	}
}

// testHarness agrupa el service y sus dobles para inspeccionar el resultado.
type testHarness struct {
	svc        *ConsignaService
	consignas  *fakeConsignaRepo
	decisiones *fakeDecisionesRepo
	lotes      *fakeLoteLookup
	metricas   *fakeMetricasLookup
	productos  *fakeProductoRepo
}

func newHarness(lote *lote_sector.Lote) *testHarness {
	productos := newFakeProductoRepo()
	productos.byID[testProductoID] = productoValido()
	consignas := &fakeConsignaRepo{}
	decisiones := &fakeDecisionesRepo{}
	lotes := &fakeLoteLookup{lotes: map[string]*lote_sector.Lote{}}
	if lote != nil {
		lotes.lotes[lote.ID] = lote
	}
	metricas := &fakeMetricasLookup{}
	return &testHarness{
		svc:        NewConsignaService(consignas, productos, sse.NewBroker(), decisiones, lotes, metricas),
		consignas:  consignas,
		decisiones: decisiones,
		lotes:      lotes,
		metricas:   metricas,
		productos:  productos,
	}
}

func reqManual(loteID string) consigna.ConsignaManualRequest {
	var lote *string
	if loteID != "" {
		lote = &loteID
	}
	return consigna.ConsignaManualRequest{
		ProductoID:             testProductoID,
		TemperaturaObjetivo:    170,
		VelocidadCintaObjetivo: 0.20,
		Usuario:                "operario-demo",
		LoteID:                 lote,
	}
}

func TestDispatchManual_LoteRequerido(t *testing.T) {
	h := newHarness(loteAbiertoManual())

	for _, loteID := range []string{"", "   "} {
		_, err := h.svc.DispatchManual(context.Background(), reqManual(loteID))
		if !errors.Is(err, consigna.ErrLoteRequerido) {
			t.Fatalf("loteID=%q: expected ErrLoteRequerido, got %v", loteID, err)
		}
	}
	if h.consignas.count() != 0 {
		t.Fatalf("un request sin lote no debe auditarse")
	}
}

func TestDispatchManual_LoteNoExiste(t *testing.T) {
	h := newHarness(loteAbiertoManual())

	_, err := h.svc.DispatchManual(context.Background(), reqManual("lote-inexistente"))
	if !errors.Is(err, consigna.ErrLoteNoExiste) {
		t.Fatalf("expected ErrLoteNoExiste, got %v", err)
	}
	if h.consignas.count() != 0 {
		t.Fatalf("un lote inexistente no debe auditarse")
	}
}

func TestDispatchManual_LoteNoAbierto(t *testing.T) {
	lote := loteAbiertoManual()
	lote.Estado = lote_sector.EstadoCerrado
	h := newHarness(lote)

	_, err := h.svc.DispatchManual(context.Background(), reqManual(lote.ID))
	if !errors.Is(err, consigna.ErrLoteNoAbierto) {
		t.Fatalf("expected ErrLoteNoAbierto, got %v", err)
	}
	if h.consignas.count() != 0 {
		t.Fatalf("un lote cerrado no debe auditarse")
	}
}

func TestDispatchManual_LoteNoManual(t *testing.T) {
	lote := loteAbiertoManual()
	lote.Modo = lote_sector.ModoAutomatico
	h := newHarness(lote)

	_, err := h.svc.DispatchManual(context.Background(), reqManual(lote.ID))
	if !errors.Is(err, consigna.ErrLoteNoManual) {
		t.Fatalf("expected ErrLoteNoManual, got %v", err)
	}
	if decisiones, aprobadas := h.decisiones.snapshot(); len(decisiones) != 0 || len(aprobadas) != 0 {
		t.Fatalf("un lote no MANUAL no debe encolar ni aprobar; decisiones=%d aprobadas=%d", len(decisiones), len(aprobadas))
	}
}

func TestDispatchManual_SectorMismatch(t *testing.T) {
	h := newHarness(loteAbiertoManual())

	req := reqManual("lote-1")
	req.SectorID = "otro-sector"
	_, err := h.svc.DispatchManual(context.Background(), req)
	if !errors.Is(err, consigna.ErrSectorMismatch) {
		t.Fatalf("expected ErrSectorMismatch, got %v", err)
	}
	if h.consignas.count() != 0 {
		t.Fatalf("un mismatch no debe auditarse")
	}

	// El alias legacy hornoId también se compara contra el sector del lote.
	req = reqManual("lote-1")
	req.HornoID = "otro-sector"
	_, err = h.svc.DispatchManual(context.Background(), req)
	if !errors.Is(err, consigna.ErrSectorMismatch) {
		t.Fatalf("alias hornoId: expected ErrSectorMismatch, got %v", err)
	}
}

func TestDispatchManual_SectorOpcionalCoincide(t *testing.T) {
	h := newHarness(loteAbiertoManual())

	req := reqManual("lote-1")
	req.SectorID = "sector-1"
	rec, err := h.svc.DispatchManual(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.HornoID != "sector-1" {
		t.Fatalf("HornoID = %q, want sector-1", rec.HornoID)
	}
}

func TestDispatchManual_LoteSinSector(t *testing.T) {
	lote := loteAbiertoManual()
	lote.SectorID = ""
	h := newHarness(lote)

	_, err := h.svc.DispatchManual(context.Background(), reqManual(lote.ID))
	if !errors.Is(err, consigna.ErrSectorRequerido) {
		t.Fatalf("expected ErrSectorRequerido, got %v", err)
	}
}

func TestDispatchManual_ParametrosNoExiste(t *testing.T) {
	h := newHarness(loteAbiertoManual())

	req := reqManual("lote-1")
	req.ProductoID = "producto-inexistente"
	_, err := h.svc.DispatchManual(context.Background(), req)
	if !errors.Is(err, consigna.ErrParametrosNoExiste) {
		t.Fatalf("expected ErrParametrosNoExiste, got %v", err)
	}
}

func TestDispatchManual_FueraDeRango(t *testing.T) {
	h := newHarness(loteAbiertoManual())

	req := reqManual("lote-1")
	req.TemperaturaObjetivo = 999
	_, err := h.svc.DispatchManual(context.Background(), req)
	if !errors.Is(err, consigna.ErrFueraDeRango) {
		t.Fatalf("expected ErrFueraDeRango, got %v", err)
	}
	if h.consignas.count() != 0 {
		t.Fatalf("un valor fuera de rango no debe auditarse")
	}
}

func TestDispatchManual_ExitoEncolaYAudita(t *testing.T) {
	temp, vel := 198.0, 0.31
	h := newHarness(loteAbiertoManual())
	h.metricas.muestras = []metricashorno.MetricaHorno{{TempHorno1: &temp, VelocidadCinta: &vel}}

	rec, err := h.svc.DispatchManual(context.Background(), reqManual("lote-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rec.Exitosa {
		t.Fatalf("Exitosa = false, want true (aceptada y encolada)")
	}
	if rec.Estado != consigna.EstadoConsignaPendiente {
		t.Fatalf("Estado = %q, want PENDIENTE", rec.Estado)
	}
	if rec.DecisionID == nil || *rec.DecisionID != "dec-1" {
		t.Fatalf("DecisionID = %v, want dec-1", rec.DecisionID)
	}
	if rec.HornoID != "sector-1" {
		t.Fatalf("HornoID = %q, want sector-1", rec.HornoID)
	}
	if rec.Origen != consigna.OrigenManual {
		t.Fatalf("Origen = %q, want MANUAL", rec.Origen)
	}
	if rec.LoteID == nil || *rec.LoteID != "lote-1" {
		t.Fatalf("LoteID = %v, want lote-1", rec.LoteID)
	}
	if rec.ProductoID == nil || *rec.ProductoID != testProductoID {
		t.Fatalf("ProductoID = %v, want %s", rec.ProductoID, testProductoID)
	}
	if rec.Usuario == nil || *rec.Usuario != "operario-demo" {
		t.Fatalf("Usuario = %v, want operario-demo", rec.Usuario)
	}
	if rec.TemperaturaPrevia == nil || *rec.TemperaturaPrevia != 198 {
		t.Fatalf("TemperaturaPrevia = %v, want 198", rec.TemperaturaPrevia)
	}
	if rec.VelocidadCintaPrevia == nil || *rec.VelocidadCintaPrevia != 0.31 {
		t.Fatalf("VelocidadCintaPrevia = %v, want 0.31", rec.VelocidadCintaPrevia)
	}

	if h.consignas.count() != 1 {
		t.Fatalf("expected 1 registro auditado, got %d", h.consignas.count())
	}
	decisiones, aprobadas := h.decisiones.snapshot()
	if len(decisiones) != 1 {
		t.Fatalf("expected 1 decisión encolada, got %d", len(decisiones))
	}
	d := decisiones[0]
	if d.LoteID != "lote-1" {
		t.Fatalf("decision.LoteID = %q, want lote-1", d.LoteID)
	}
	if d.Muestras != 0 {
		t.Fatalf("decision.Muestras = %d, want 0 (override manual)", d.Muestras)
	}
	if d.TempPropuesto != 170 || d.VelPropuesto != 0.20 {
		t.Fatalf("propuesta = %.2f/%.2f, want 170/0.20", d.TempPropuesto, d.VelPropuesto)
	}
	if d.TempActual != 198 {
		t.Fatalf("TempActual = %.2f, want 198 (telemetría)", d.TempActual)
	}
	if d.VelActual == nil || *d.VelActual != 0.31 {
		t.Fatalf("VelActual = %v, want 0.31", d.VelActual)
	}
	if d.AplicadoPor != decisiones_control.AplicadoPorNadie {
		t.Fatalf("AplicadoPor = %q, want NADIE", d.AplicadoPor)
	}
	if len(aprobadas) != 1 || aprobadas[0] != d.ID {
		t.Fatalf("expected la decisión %q aprobada, got %v", d.ID, aprobadas)
	}
}

func TestDispatchManual_SinTelemetriaPreviosNil(t *testing.T) {
	h := newHarness(loteAbiertoManual())

	rec, err := h.svc.DispatchManual(context.Background(), reqManual("lote-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.TemperaturaPrevia != nil || rec.VelocidadCintaPrevia != nil {
		t.Fatalf("sin telemetría los previos deben ser nil; got %v/%v", rec.TemperaturaPrevia, rec.VelocidadCintaPrevia)
	}
	decisiones, _ := h.decisiones.snapshot()
	if len(decisiones) != 1 {
		t.Fatalf("expected 1 decisión, got %d", len(decisiones))
	}
	if decisiones[0].TempActual != 0 {
		t.Fatalf("TempActual = %v, want 0 sin telemetría", decisiones[0].TempActual)
	}
	if decisiones[0].VelActual == nil || *decisiones[0].VelActual != 0 {
		t.Fatalf("VelActual = %v, want 0 sin telemetría", decisiones[0].VelActual)
	}
}

func TestDispatchManual_FalloDeEncolado(t *testing.T) {
	casos := []struct {
		nombre string
		setErr func(*testHarness)
	}{
		{"save_decision", func(h *testHarness) { h.decisiones.saveErr = errors.New("db down") }},
		{"aprobar_decision", func(h *testHarness) { h.decisiones.aprobarErr = errors.New("db down") }},
	}
	for _, tc := range casos {
		t.Run(tc.nombre, func(t *testing.T) {
			h := newHarness(loteAbiertoManual())
			tc.setErr(h)

			rec, err := h.svc.DispatchManual(context.Background(), reqManual("lote-1"))
			if !errors.Is(err, consigna.ErrEncoladoFallido) {
				t.Fatalf("expected ErrEncoladoFallido, got %v", err)
			}
			if rec == nil || rec.Exitosa {
				t.Fatalf("el registro debe quedar con exitosa=false; got %+v", rec)
			}
			if rec.Estado != consigna.EstadoConsignaRechazada {
				t.Fatalf("Estado = %q, want RECHAZADA", rec.Estado)
			}
			if rec.DecisionID != nil {
				t.Fatalf("DecisionID = %v, want nil sin encolado", rec.DecisionID)
			}
			if rec.MotivoError == nil {
				t.Fatalf("el registro fallido debe tener motivo_error")
			}
			saved := h.consignas.all()
			if len(saved) != 1 {
				t.Fatalf("expected el intento fallido auditado, got %d", len(saved))
			}
			if saved[0].Exitosa {
				t.Fatalf("el historial debe guardar exitosa=false")
			}
			if saved[0].Estado != consigna.EstadoConsignaRechazada {
				t.Fatalf("el historial debe guardar estado=RECHAZADA; got %q", saved[0].Estado)
			}
			if saved[0].MotivoError == nil {
				t.Fatalf("el historial debe guardar motivo_error")
			}
		})
	}
}

func TestGetHistorialByLote(t *testing.T) {
	h := newHarness(loteAbiertoManual())
	if _, err := h.svc.DispatchManual(context.Background(), reqManual("lote-1")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	historial, err := h.svc.GetHistorialByLote(context.Background(), "lote-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(historial) != 1 {
		t.Fatalf("expected 1 registro en el historial, got %d", len(historial))
	}
	if historial[0].Origen != consigna.OrigenManual {
		t.Fatalf("Origen = %q, want MANUAL", historial[0].Origen)
	}
}
