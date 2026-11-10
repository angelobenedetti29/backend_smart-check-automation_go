package control

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// fakeEventosRepo implementa la interface mínima para los tests de Loop.
// Devuelve los conteos que el test configure.
type fakeEventosRepo struct {
	okN, crudoN, quemadoN int
	err                   error
	calls                 int
	confPromedio          float64
	confMuestras          int
	confErr               error
}

func (f *fakeEventosRepo) CountEventosEnVentana(
	_ context.Context, _ string, _, _ time.Time,
) (int, int, int, error) {
	f.calls++
	if f.err != nil {
		return 0, 0, 0, f.err
	}
	return f.okN, f.crudoN, f.quemadoN, nil
}

func (f *fakeEventosRepo) ConfianzaPromedioEnVentana(
	_ context.Context, _ string, _, _ time.Time,
) (float64, int, error) {
	if f.confErr != nil {
		return 0, 0, f.confErr
	}
	return f.confPromedio, f.confMuestras, nil
}

// TestCalcularZObservadoVentanaVacia verifica que cuando la ventana no tiene
// eventos, z_observado se persiste como nil y los % observados como 0 (no
// como datos falsos).
func TestCalcularZObservadoVentanaVacia(t *testing.T) {
	l := &Loop{
		loteID:  "test-lote",
		pesos:   optimizador.Pesos{WC: 1.0, WQ: 1.0},
		eventos: &fakeEventosRepo{okN: 0, crudoN: 0, quemadoN: 0},
		now:     func() time.Time { return time.Unix(0, 0) },
	}
	var c, o, q float64
	z, set := l.calcularZObservado(context.Background(), time.Unix(0, 0), time.Unix(0, 0), &c, &o, &q)
	if set {
		t.Fatalf("z observada no debería estar seteada en ventana vacía; got=%v", z)
	}
	if z != nil {
		t.Fatalf("z debería ser nil en ventana vacía; got=%v", z)
	}
	if c != 0 || o != 0 || q != 0 {
		t.Fatalf("porcentajes deberían ser 0; got C=%.2f O=%.2f Q=%.2f", c, o, q)
	}
}

// TestCalcularZObservadoConEventos verifica que cuando la ventana tiene
// eventos, z_observado se calcula con los % reales sobre el total.
func TestCalcularZObservadoConEventos(t *testing.T) {
	// 60% OK, 30% Crudo, 10% Quemado sobre 10 eventos
	l := &Loop{
		loteID:  "test-lote",
		pesos:   optimizador.Pesos{WC: 1.0, WQ: 1.0, DT: 0.05, DC: 0.5},
		eventos: &fakeEventosRepo{okN: 6, crudoN: 3, quemadoN: 1},
		now:     func() time.Time { return time.Unix(0, 0) },
	}
	var c, o, q float64
	z, set := l.calcularZObservado(context.Background(), time.Unix(0, 0), time.Unix(0, 0), &c, &o, &q)
	if !set {
		t.Fatalf("z debería estar seteada con eventos")
	}
	wantC, wantO, wantQ := 30.0, 60.0, 10.0
	if c != wantC || o != wantO || q != wantQ {
		t.Fatalf("porcentajes mal calculados; got C=%.2f O=%.2f Q=%.2f, want C=%.2f O=%.2f Q=%.2f",
			c, o, q, wantC, wantO, wantQ)
	}
	// Z = 1.0 * 30 + 1.0 * 10 = 40
	if *z != 40 {
		t.Fatalf("z mal calculada; got=%.2f want=40", *z)
	}
}

// TestCalcularZObservadoIgualZPredichoConMismosPorcentajes verifica que
// z_observado y z_predicho son comparables: con los mismos porcentajes y pesos,
// ambas Z usan la única fuente de verdad optimizador.ZCalidad.
func TestCalcularZObservadoIgualZPredichoConMismosPorcentajes(t *testing.T) {
	pesos := optimizador.Pesos{WC: 1, WQ: 1, WO: 1, DT: 0.05, DC: 0.5}
	l := &Loop{
		loteID:  "test-lote",
		pesos:   pesos,
		eventos: &fakeEventosRepo{okN: 70, crudoN: 10, quemadoN: 20},
		now:     func() time.Time { return time.Unix(0, 0) },
	}
	var c, o, q float64
	zObs, set := l.calcularZObservado(context.Background(), time.Unix(0, 0), time.Unix(0, 0), &c, &o, &q)
	if !set || zObs == nil {
		t.Fatalf("z observada debería estar seteada con eventos")
	}
	zPredicho := optimizador.ZCalidad(pesos, 10, 70, 20)
	if math.Abs(*zObs-zPredicho) > 1e-9 {
		t.Fatalf("z_observado=%.6f != z_predicho=%.6f con los mismos porcentajes", *zObs, zPredicho)
	}
}

// TestCalcularZObservadoErrorRepo verifica que un fallo del repo NO rompe el
// tick: devuelve (nil, false) y los % se quedan en 0, coherente con "sin datos
// observados".
func TestCalcularZObservadoErrorRepo(t *testing.T) {
	l := &Loop{
		loteID:  "test-lote",
		pesos:   optimizador.Pesos{WC: 1.0, WQ: 1.0},
		eventos: &fakeEventosRepo{err: context.DeadlineExceeded},
		now:     func() time.Time { return time.Unix(0, 0) },
	}
	var c, o, q float64
	z, set := l.calcularZObservado(context.Background(), time.Unix(0, 0), time.Unix(0, 0), &c, &o, &q)
	if set {
		t.Fatalf("z no debería estar seteada en error de repo")
	}
	if z != nil {
		t.Fatalf("z debería ser nil en error de repo; got=%v", z)
	}
	if c != 0 || o != 0 || q != 0 {
		t.Fatalf("porcentajes deberían ser 0 en error de repo")
	}
}

// TestCalcularZObservadoSinRepo verifica que sin repo inyectado (nil defensivo)
// no hay panic y devuelve nil/false.
func TestCalcularZObservadoSinRepo(t *testing.T) {
	l := &Loop{
		loteID:  "test-lote",
		pesos:   optimizador.Pesos{WC: 1.0, WQ: 1.0},
		eventos: nil,
		now:     func() time.Time { return time.Unix(0, 0) },
	}
	var c, o, q float64
	z, set := l.calcularZObservado(context.Background(), time.Unix(0, 0), time.Unix(0, 0), &c, &o, &q)
	if set || z != nil {
		t.Fatalf("sin repo inyectado no debería haber z")
	}
	if c != 0 || o != 0 || q != 0 {
		t.Fatalf("porcentajes deberían ser 0 sin repo")
	}
}

// TestLoopPropuestaPendienteNoDestructivo verifica que PropuestaPendiente
// devuelve un snapshot estable y NO consume el estado interno del loop.
func TestLoopPropuestaPendienteNoDestructivo(t *testing.T) {
	l := &Loop{
		propuestaPendiente: &optimizador.Propuesta{Temp1: 180, Velocidad: 0.45},
		lastDecisionID:     "dec-1",
		lastVentanaInicio:  time.Unix(100, 0).UTC(),
		lastVentanaFin:     time.Unix(200, 0).UTC(),
		lastCreatedAt:      time.Unix(200, 0).UTC(),
	}

	s1 := l.PropuestaPendiente(30)
	s2 := l.PropuestaPendiente(30)
	if s1 == nil || s2 == nil {
		t.Fatalf("el snapshot no debe ser nil con propuesta pendiente")
	}
	if l.propuestaPendiente == nil {
		t.Fatalf("PropuestaPendiente no debe consumir el estado interno")
	}
	if s1.Temp1 != 180 || s1.Velocidad != 0.45 || s1.DecisionID != "dec-1" {
		t.Fatalf("snapshot incorrecto: %+v", s1)
	}
	if !s1.VentanaInicio.Equal(time.Unix(100, 0).UTC()) {
		t.Fatalf("VentanaInicio incorrecta: %v", s1.VentanaInicio)
	}
}

// TestPropuestaVaciaDevuelveNil verifica que sin propuesta pendiente el
// snapshot es nil.
func TestPropuestaVaciaDevuelveNil(t *testing.T) {
	l := &Loop{}
	if got := l.PropuestaPendiente(30); got != nil {
		t.Fatalf("sin propuesta pendiente debe devolver nil; got=%+v", got)
	}
}

// TestLoopPropuestaPendienteTTL verifica que el TTL del snapshot refleja el
// parámetro recibido.
func TestLoopPropuestaPendienteTTL(t *testing.T) {
	l := &Loop{
		propuestaPendiente: &optimizador.Propuesta{Temp1: 180, Velocidad: 0.45},
	}
	s := l.PropuestaPendiente(42)
	if s == nil {
		t.Fatalf("se esperaba snapshot no nil")
	}
	if s.TTLSegundos != 42 {
		t.Fatalf("TTLSegundos = %d, want 42", s.TTLSegundos)
	}
}

type marcarArgs struct {
	decisionID  string
	temp, vel   float64
	aplicadoPor string
}

type decisionesRepoSnapshot struct {
	saveCalled      int
	lastAplicadoPor string
	lastDecision    *decisiones_control.DecisionControl
	marcarCalled    int
	marcarArgs      marcarArgs
}

// fakeDecisionesRepo implementa la interface mínima decisionesRepo del Loop.
type fakeDecisionesRepo struct {
	mu              sync.Mutex
	saveCalled      int
	saveErr         error
	lastAplicadoPor string
	lastDecision    *decisiones_control.DecisionControl
	marcarCalled    int
	marcarArgs      marcarArgs
	marcarErr       error
}

// snapshot copia el estado del fake para leerlo desde el test sin carreras
// con la goroutine del Loop.
func (f *fakeDecisionesRepo) snapshot() decisionesRepoSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := decisionesRepoSnapshot{
		saveCalled:      f.saveCalled,
		lastAplicadoPor: f.lastAplicadoPor,
		marcarCalled:    f.marcarCalled,
		marcarArgs:      f.marcarArgs,
	}
	if f.lastDecision != nil {
		cp := *f.lastDecision
		s.lastDecision = &cp
	}
	return s
}

// Save simula el RETURNING de la DB: completa ID/CreatedAt (salvo error).
func (f *fakeDecisionesRepo) Save(_ context.Context, d *decisiones_control.DecisionControl) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalled++
	f.lastAplicadoPor = d.AplicadoPor
	if f.saveErr != nil {
		return f.saveErr
	}
	d.ID = "dec-save-1"
	d.CreatedAt = time.Now().UTC()
	f.lastDecision = d
	return nil
}

func (f *fakeDecisionesRepo) GetByID(_ context.Context, _ string) (*decisiones_control.DecisionControl, error) {
	return nil, nil
}

func (f *fakeDecisionesRepo) Aprobar(_ context.Context, _, _ string) error { return nil }

func (f *fakeDecisionesRepo) MarcarAplicada(_ context.Context, id string, t, v float64, ap string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marcarCalled++
	f.marcarArgs.decisionID = id
	f.marcarArgs.temp, f.marcarArgs.vel, f.marcarArgs.aplicadoPor = t, v, ap
	return f.marcarErr
}

// fakeAlertaRepoControl captura las alertas que persiste el loop.
type fakeAlertaRepoControl struct {
	guardadas []alerta.Alerta
}

func (f *fakeAlertaRepoControl) Save(a *alerta.Alerta) error {
	f.guardadas = append(f.guardadas, *a)
	return nil
}

func (f *fakeAlertaRepoControl) GetByHornoID(string) ([]alerta.Alerta, error) {
	return f.guardadas, nil
}

func (f *fakeAlertaRepoControl) Listar(context.Context, string) ([]alerta.Alerta, error) {
	return f.guardadas, nil
}

func (f *fakeAlertaRepoControl) Ack(context.Context, string, alerta.Usuario) (*alerta.Alerta, error) {
	return nil, alerta.ErrNotFound
}

// TestNewLoopAsignaLoteID verifica que NewLoop propaga el loteID al campo del
// struct (sin esto el tick persiste con lote_id vacío).
func TestNewLoopAsignaLoteID(t *testing.T) {
	loop := NewLoop("lote-xyz", nil, nil, nil, nil, nil, nil, nil)
	if loop.loteID != "lote-xyz" {
		t.Fatalf("loteID = %q, want %q", loop.loteID, "lote-xyz")
	}
}

// --- Fakes de soporte para tick/run ---

// fakeLoteLookupControl implementa loteLookupService.
type fakeLoteLookupControl struct {
	lote *lote_sector.Lote
	err  error
}

func (f *fakeLoteLookupControl) GetByID(_ context.Context, _ string) (*lote_sector.Lote, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.lote == nil {
		return nil, nil
	}
	cp := *f.lote
	return &cp, nil
}

// fakeMetricasRepoControl implementa metricasRepo.
type fakeMetricasRepoControl struct {
	metricas []metricashorno.MetricaHorno
	err      error
}

func (f *fakeMetricasRepoControl) GetUltimasParaLote(_ context.Context, _ string, _ int) ([]metricashorno.MetricaHorno, error) {
	return f.metricas, f.err
}

// fakeProductoLookup implementa productoLookupService.
type fakeProductoLookup struct {
	prod *producto.Producto
	err  error
}

func (f *fakeProductoLookup) GetByID(_ context.Context, _ string) (*producto.Producto, error) {
	return f.prod, f.err
}

// fakePredictorControl devuelve una predicción fija para el optimizador real.
type fakePredictorControl struct {
	out optimizador.PrediccionSalida
	err error
}

func (p fakePredictorControl) Predict(_ context.Context, _ optimizador.EntradasPredictor) (optimizador.PrediccionSalida, error) {
	return p.out, p.err
}

// newSupervisionLoop arma un Loop en modo SUPERVISION con métricas y eventos
// dentro de la ventana, listo para ejercitar tick.
func newSupervisionLoop(now time.Time, repo *fakeDecisionesRepo, broker *sse.Broker, pred optimizador.Predictor) *Loop {
	return &Loop{
		loteID:         "lote-1",
		productoID:     "prod-1",
		modo:           lote_sector.ModoSupervision,
		ventanaEvalSeg: 600,
		ventanaActSeg:  300,
		pesos:          optimizador.Pesos{WC: 1, WQ: 1, WO: 1, DT: 0.05, DC: 0.5},
		limites: optimizador.Limites{
			TempMin: 100, TempMax: 300,
			VelMin: 0.05, VelMax: 0.5,
			DeltaTemp1: 10, DeltaVel: 0.05,
			ToleranciaTempC: 2.0,
		},
		clienteLote: &fakeLoteLookupControl{lote: &lote_sector.Lote{
			ID: "lote-1", Estado: lote_sector.EstadoAbierto, Modo: lote_sector.ModoSupervision,
		}},
		clienteMet: &fakeMetricasRepoControl{metricas: []metricashorno.MetricaHorno{{
			ReceivedAt:     now.Add(-time.Second),
			TempHorno1:     floatPtr(120),
			VelocidadCinta: floatPtr(0.2),
			TempObjetivo:   floatPtr(120),
			VelObjetivo:    floatPtr(0.2),
		}}},
		eventos:    &fakeEventosRepo{okN: 8, crudoN: 1, quemadoN: 1},
		producto:   &fakeProductoLookup{prod: &producto.Producto{ID: "prod-1"}},
		optimizer:  optimizador.NewOptimizer(pred),
		decisiones: repo,
		broker:     broker,
		now:        func() time.Time { return now },
	}
}

// TestTickSaleCuandoLoteCerrado verifica que tick marca shouldExit si el lote
// dejó de estar ABIERTO.
func TestTickSaleCuandoLoteCerrado(t *testing.T) {
	loop := &Loop{
		loteID:     "lote-1",
		productoID: "prod-1",
		modo:       lote_sector.ModoSupervision,
		clienteLote: &fakeLoteLookupControl{lote: &lote_sector.Lote{
			ID: "lote-1", Estado: lote_sector.EstadoCerrado, Modo: lote_sector.ModoSupervision,
		}},
	}
	loop.tick(context.Background())
	if !loop.shouldExit {
		t.Fatalf("tick debe marcar shouldExit cuando el lote está cerrado")
	}
}

// TestTickSupervisionNoPublicaSiSaveFalla verifica el orden persistir→publicar:
// si Save falla, no queda propuesta en memoria ni metadatos.
func TestTickSupervisionNoPublicaSiSaveFalla(t *testing.T) {
	repo := &fakeDecisionesRepo{saveErr: errors.New("db down")}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	now := time.Now().UTC()
	loop := newSupervisionLoop(now, repo, broker, fakePredictorControl{out: optimizador.PrediccionSalida{PctCrudos: 10, PctOk: 80, PctQuemados: 10}})
	loop.tick(context.Background())

	if loop.propuestaPendiente != nil {
		t.Fatalf("no debe publicar propuesta si Save falla")
	}
	if loop.lastDecisionID != "" {
		t.Fatalf("lastDecisionID debe quedar vacío; got=%q", loop.lastDecisionID)
	}
	if !loop.lastCreatedAt.IsZero() || !loop.lastVentanaInicio.IsZero() || !loop.lastVentanaFin.IsZero() {
		t.Fatalf("metadatos deben quedar zero")
	}
	select {
	case ev := <-client.Events:
		t.Fatalf("no debe emitir broadcast si Save falla; got=%s", ev.EventType)
	default:
	}
}

// TestTickSupervisionPublicaAtomico verifica que con Save exitoso se publica la
// propuesta y sus metadatos juntos, y se emite exactamente un broadcast.
func TestTickSupervisionPublicaAtomico(t *testing.T) {
	repo := &fakeDecisionesRepo{}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	now := time.Now().UTC()
	loop := newSupervisionLoop(now, repo, broker, fakePredictorControl{out: optimizador.PrediccionSalida{PctCrudos: 10, PctOk: 80, PctQuemados: 10}})
	loop.tick(context.Background())

	if loop.propuestaPendiente == nil {
		t.Fatalf("debe publicar la propuesta tras Save exitoso")
	}
	if loop.lastDecisionID != "dec-save-1" {
		t.Fatalf("lastDecisionID = %q, want dec-save-1", loop.lastDecisionID)
	}
	if loop.lastCreatedAt.IsZero() {
		t.Fatalf("lastCreatedAt no debe ser zero")
	}

	got := 0
	for {
		select {
		case <-client.Events:
			got++
			continue
		default:
		}
		break
	}
	if got != 1 {
		t.Fatalf("broadcasts = %d, want 1", got)
	}
}

// TestTickAutomaticoPublicaAutorizada verifica el camino AUTOMATICO: tick
// persiste la decisión como NADIE, publica la propuesta autorizada y no
// despacha (la Pi hace el pull).
func TestTickAutomaticoPublicaAutorizada(t *testing.T) {
	repo := &fakeDecisionesRepo{}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	now := time.Now().UTC()
	loop := newSupervisionLoop(now, repo, broker, fakePredictorControl{out: optimizador.PrediccionSalida{PctCrudos: 10, PctOk: 80, PctQuemados: 10}})
	loop.modo = lote_sector.ModoAutomatico
	loop.fase = FaseAdaptando

	loop.tick(context.Background())

	if repo.saveCalled != 1 {
		t.Fatalf("Save llamado %d veces, want 1", repo.saveCalled)
	}
	if repo.lastAplicadoPor != decisiones_control.AplicadoPorNadie {
		t.Fatalf("aplicado_por = %q, want %q", repo.lastAplicadoPor, decisiones_control.AplicadoPorNadie)
	}
	snap := loop.PropuestaPendiente(30)
	if snap == nil || !snap.Autorizada {
		t.Fatalf("AUTOMATICO debe publicar propuesta autorizada; got %+v", snap)
	}
	if got := drainBroadcasts(client); got != 1 {
		t.Fatalf("broadcasts = %d, want 1", got)
	}
}

// TestRunCancelaCtxCuandoLoteCerrado es end-to-end: run detecta el lote cerrado
// y llama a cancel() vía defer, cerrando el ctx en pocos segundos.
func TestRunCancelaCtxCuandoLoteCerrado(t *testing.T) {
	now := time.Now().UTC()
	loop := newSupervisionLoop(now, &fakeDecisionesRepo{}, sse.NewBroker(), fakePredictorControl{})
	loop.ventanaEvalSeg = 2
	loop.clienteLote = &fakeLoteLookupControl{lote: &lote_sector.Lote{
		ID: "lote-1", Estado: lote_sector.EstadoCerrado, Modo: lote_sector.ModoSupervision,
	}}

	ctx, cancel := context.WithCancel(context.Background())
	loop.Start(ctx, cancel)

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("run no invocó cancel al detectar el lote cerrado")
	}
}

// TestStartInvocaCancelAlSalirRun verifica que cuando run sale por
// shouldExit/MANUAL/lote cerrado, defer cancel() se ejecuta.
func TestStartInvocaCancelAlSalirRun(t *testing.T) {
	now := time.Now().UTC()
	loop := newSupervisionLoop(now, &fakeDecisionesRepo{}, sse.NewBroker(), fakePredictorControl{})
	loop.ventanaEvalSeg = 600 // ventana larga, pero shouldExit=true hace run salir rápido
	loop.shouldExit = true

	ctx, cancel := context.WithCancel(context.Background())
	loop.Start(ctx, cancel)

	select {
	case <-ctx.Done():
		// OK: defer cancel() se ejecutó al salir run.
	case <-time.After(2 * time.Second):
		t.Fatal("cancel no fue invocado al salir run")
	}
}

// --- F-B10: E2E del control loop (multi-tick) ---

// drainBroadcasts cuenta, sin bloquear, los eventos encolados para un
// subscriber del broker.
func drainBroadcasts(client *sse.Client) int {
	n := 0
	for {
		select {
		case <-client.Events:
			n++
			continue
		default:
		}
		return n
	}
}

// propuestaOK es la salida fija del predictor para los tests multi-tick.
func propuestaOK() fakePredictorControl {
	return fakePredictorControl{out: optimizador.PrediccionSalida{
		PctCrudos: 10, PctOk: 80, PctQuemados: 10, ModelVersion: "test",
	}}
}

// TestRunTresTicksModoAutomaticoPublica corre el loop real en modo AUTOMATICO
// con ventanas de 1s y verifica que cada tick persiste la decisión y publica la
// propuesta autorizada por SSE (la Pi la levanta).
func TestRunTresTicksModoAutomaticoPublica(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeDecisionesRepo{}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	loop := newSupervisionLoop(now, repo, broker, propuestaOK())
	loop.modo = lote_sector.ModoAutomatico
	loop.fase = FaseAdaptando
	loop.ventanaEvalSeg = 1
	loop.ventanaActSeg = 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx, cancel)

	time.Sleep(3500 * time.Millisecond)
	cancel()
	time.Sleep(150 * time.Millisecond) // deja terminar el tick en vuelo

	if snap := repo.snapshot(); snap.saveCalled < 3 {
		t.Fatalf("saveCalls = %d, want >= 3", snap.saveCalled)
	}
	if got := drainBroadcasts(client); got < 3 {
		t.Fatalf("broadcasts = %d, want >= 3", got)
	}
	if loop.propuestaPendiente == nil {
		t.Fatalf("AUTOMATICO debe dejar propuesta pendiente")
	}
}

// TestRunModoManualNoTicKea verifica que un loop arrancado en modo MANUAL sale
// sin ejecutar ningún tick (ni dispatch ni persistencia).
func TestRunModoManualNoTicKea(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeDecisionesRepo{}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	loop := newSupervisionLoop(now, repo, broker, propuestaOK())
	loop.modo = lote_sector.ModoManual
	loop.ventanaEvalSeg = 1
	loop.ventanaActSeg = 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx, cancel)

	time.Sleep(1500 * time.Millisecond)
	cancel()
	time.Sleep(150 * time.Millisecond)

	if snap := repo.snapshot(); snap.saveCalled != 0 {
		t.Fatalf("saveCalls = %d, want 0 en MANUAL", snap.saveCalled)
	}
	if got := drainBroadcasts(client); got != 0 {
		t.Fatalf("broadcasts = %d, want 0 en MANUAL", got)
	}
}

// TestRunTresTicksModoSupervisionPublicaPropuesta corre el loop real en modo
// SUPERVISION y verifica que NO despacha pero publica propuesta por SSE y deja
// el snapshot en memoria.
func TestRunTresTicksModoSupervisionPublicaPropuesta(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeDecisionesRepo{}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	loop := newSupervisionLoop(now, repo, broker, propuestaOK())
	loop.ventanaEvalSeg = 1
	loop.ventanaActSeg = 1

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx, cancel)

	time.Sleep(3500 * time.Millisecond)
	cancel()
	time.Sleep(150 * time.Millisecond)

	if snap := repo.snapshot(); snap.saveCalled < 3 {
		t.Fatalf("saveCalls = %d, want >= 3", snap.saveCalled)
	}
	if got := drainBroadcasts(client); got < 3 {
		t.Fatalf("broadcasts = %d, want >= 3", got)
	}
	if loop.propuestaPendiente == nil {
		t.Fatalf("SUPERVISION debe dejar propuesta pendiente en memoria")
	}
	snap := loop.PropuestaPendiente(30)
	if snap == nil || snap.Autorizada {
		t.Fatalf("la propuesta de SUPERVISION no debe estar autorizada sin aprobación; got %+v", snap)
	}
}

// TestAprobarYLimpiarPropuesta verifica la aprobación y el descarte por
// decisionID, incluido el caso de un id que ya no está pendiente.
func TestAprobarYLimpiarPropuesta(t *testing.T) {
	l := &Loop{
		propuestaPendiente: &optimizador.Propuesta{Temp1: 180, Velocidad: 0.4},
		lastDecisionID:     "dec-1",
		fase:               FaseCalentando,
	}

	if l.AprobarPropuesta("otra") {
		t.Fatalf("no debe aprobar una decisión distinta")
	}
	if !l.AprobarPropuesta("dec-1") {
		t.Fatalf("debe aprobar la decisión pendiente")
	}
	snap := l.PropuestaPendiente(30)
	if snap == nil || !snap.Autorizada {
		t.Fatalf("snapshot debe reflejar autorización: %+v", snap)
	}
	if snap.Fase != FaseCalentando {
		t.Fatalf("snapshot.Fase = %q, want %q", snap.Fase, FaseCalentando)
	}

	l.ClearPropuesta("otra")
	if l.propuestaPendiente == nil {
		t.Fatalf("un id distinto no debe limpiar la propuesta")
	}
	l.ClearPropuesta("dec-1")
	if l.propuestaPendiente != nil || l.lastDecisionID != "" || l.propuestaAprobada {
		t.Fatalf("ClearPropuesta debe limpiar propuesta, id y autorización")
	}
}

// fakePredictorCapturaControl registra las entradas que recibe del MPC.
type fakePredictorCapturaControl struct {
	out    optimizador.PrediccionSalida
	called bool
	last   optimizador.EntradasPredictor
}

func (p *fakePredictorCapturaControl) Predict(_ context.Context, in optimizador.EntradasPredictor) (optimizador.PrediccionSalida, error) {
	p.called = true
	p.last = in
	return p.out, nil
}

// TestTickPasaCombustionYHumedadAlPredictor verifica que estadoDesdeMetricas y
// Resolver propagan las features opcionales hasta el predictor.
func TestTickPasaCombustionYHumedadAlPredictor(t *testing.T) {
	now := time.Now().UTC()
	comb := 55.5
	hum := 7.25
	pred := &fakePredictorCapturaControl{out: optimizador.PrediccionSalida{PctCrudos: 10, PctOk: 80, PctQuemados: 10}}
	loop := newSupervisionLoop(now, &fakeDecisionesRepo{}, sse.NewBroker(), pred)
	loop.clienteMet = &fakeMetricasRepoControl{metricas: []metricashorno.MetricaHorno{{
		ReceivedAt:     now.Add(-time.Second),
		TempHorno1:     floatPtr(120),
		TempComb1:      &comb,
		HumedadHorno:   &hum,
		VelocidadCinta: floatPtr(0.2),
		TempObjetivo:   floatPtr(120),
		VelObjetivo:    floatPtr(0.2),
	}}}

	loop.tick(context.Background())

	if !pred.called {
		t.Fatalf("el predictor no fue invocado")
	}
	if pred.last.TempComb1 == nil || *pred.last.TempComb1 != comb {
		t.Fatalf("TempComb1 = %+v, want %v", pred.last.TempComb1, comb)
	}
	if pred.last.HumedadHorno == nil || *pred.last.HumedadHorno != hum {
		t.Fatalf("HumedadHorno = %+v, want %v", pred.last.HumedadHorno, hum)
	}
}

// TestTickConfianzaBajaPersisteSkip verifica que la confianza reportada por los
// eventos (Pi SALIDA) gatea el tick aunque la telemetría del horno tenga una
// confianza alta: la fuente es el evento, no la métrica.
func TestTickConfianzaBajaPersisteSkip(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeDecisionesRepo{}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	loop := newSupervisionLoop(now, repo, broker, propuestaOK())
	loop.confianzaMin = 0.5
	loop.clienteMet = &fakeMetricasRepoControl{metricas: []metricashorno.MetricaHorno{{
		ReceivedAt:         now.Add(-time.Second),
		TempHorno1:         floatPtr(120),
		VelocidadCinta:     floatPtr(0.2),
		TempObjetivo:       floatPtr(120),
		VelObjetivo:        floatPtr(0.2),
		ConfianzaDeteccion: floatPtr(0.9),
	}}}
	loop.eventos = &fakeEventosRepo{okN: 8, crudoN: 1, quemadoN: 1, confPromedio: 0.2, confMuestras: 5}

	loop.tick(context.Background())

	if repo.saveCalled != 1 || repo.lastDecision == nil {
		t.Fatalf("se esperaba una decisión persistida")
	}
	if repo.lastDecision.RazonSkipped == nil || *repo.lastDecision.RazonSkipped != "confianza_baja" {
		t.Fatalf("razon_skipped = %v, want confianza_baja", repo.lastDecision.RazonSkipped)
	}
	if math.Abs(repo.lastDecision.ConfPromedio-0.2) > 1e-9 {
		t.Fatalf("ConfPromedio = %v, want 0.2 (promedio de eventos)", repo.lastDecision.ConfPromedio)
	}
	if loop.propuestaPendiente != nil {
		t.Fatalf("no debe publicar propuesta con confianza baja")
	}
	if got := drainBroadcasts(client); got != 0 {
		t.Fatalf("broadcasts = %d, want 0", got)
	}
}

// TestTickConfPromedioPersisteEventos verifica que con muestras de confianza en
// los eventos, la decisión persiste el promedio de eventos.
func TestTickConfPromedioPersisteEventos(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeDecisionesRepo{}
	loop := newSupervisionLoop(now, repo, sse.NewBroker(), propuestaOK())
	loop.eventos = &fakeEventosRepo{okN: 8, crudoN: 1, quemadoN: 1, confPromedio: 0.73, confMuestras: 10}

	loop.tick(context.Background())

	if repo.lastDecision == nil {
		t.Fatalf("se esperaba una decisión persistida")
	}
	if math.Abs(repo.lastDecision.ConfPromedio-0.73) > 1e-9 {
		t.Fatalf("ConfPromedio = %v, want 0.73 (promedio de eventos)", repo.lastDecision.ConfPromedio)
	}
}

// TestTickPlaceholderAutomaticoNoPublicaNiDespacha verifica que en AUTOMATICO
// un modelo placeholder no se aplica ni se publica.
func TestTickPlaceholderAutomaticoNoPublicaNiDespacha(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeDecisionesRepo{}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	pred := fakePredictorControl{out: optimizador.PrediccionSalida{PctCrudos: 10, PctOk: 80, PctQuemados: 10, EsPlaceholder: true}}
	loop := newSupervisionLoop(now, repo, broker, pred)
	loop.modo = lote_sector.ModoAutomatico
	loop.fase = FaseAdaptando

	loop.tick(context.Background())

	if repo.lastDecision == nil || repo.lastDecision.RazonSkipped == nil || *repo.lastDecision.RazonSkipped != "modelo_no_entrenado" {
		t.Fatalf("se esperaba skip modelo_no_entrenado; got=%+v", repo.lastDecision)
	}
	if loop.propuestaPendiente != nil {
		t.Fatalf("AUTOMATICO no debe publicar un placeholder")
	}
	if got := drainBroadcasts(client); got != 0 {
		t.Fatalf("broadcasts = %d, want 0", got)
	}
}

// TestTickPlaceholderSupervisionPublica verifica que en SUPERVISION el
// placeholder se publica para que el operario vea el warning.
func TestTickPlaceholderSupervisionPublica(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeDecisionesRepo{}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	pred := fakePredictorControl{out: optimizador.PrediccionSalida{PctCrudos: 10, PctOk: 80, PctQuemados: 10, EsPlaceholder: true}}
	loop := newSupervisionLoop(now, repo, broker, pred)

	loop.tick(context.Background())

	if repo.saveCalled != 1 {
		t.Fatalf("saveCalled = %d, want 1", repo.saveCalled)
	}
	if loop.propuestaPendiente == nil {
		t.Fatalf("SUPERVISION debe publicar el placeholder")
	}
	if got := drainBroadcasts(client); got != 1 {
		t.Fatalf("broadcasts = %d, want 1", got)
	}
}

// newPreheatLoop arma un Loop AUTOMATICO en fase CALENTANDO con la receta y
// telemetría dadas.
func newPreheatLoop(now time.Time, repo *fakeDecisionesRepo, broker *sse.Broker, pred optimizador.Predictor, target, vel, tol float64, metricas []metricashorno.MetricaHorno) *Loop {
	loop := newSupervisionLoop(now, repo, broker, pred)
	loop.modo = lote_sector.ModoAutomatico
	loop.fase = FaseCalentando
	loop.preheatPollSeg = 15
	loop.confianzaMin = 0.5
	loop.clienteLote = &fakeLoteLookupControl{lote: &lote_sector.Lote{
		ID: "lote-1", Estado: lote_sector.EstadoAbierto, Modo: lote_sector.ModoAutomatico,
		Temp1Objetivo: floatPtr(target), VelObjetivo: floatPtr(vel), ToleranciaTempC: tol,
	}}
	loop.clienteMet = &fakeMetricasRepoControl{metricas: metricas}
	return loop
}

// TestPreheatSecuenciaTempLuegoVelocidad verifica que primero se sostiene T1
// con V=0 y, tras 3 muestras en tolerancia, se entrega la transición con V.
func TestPreheatSecuenciaTempLuegoVelocidad(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeDecisionesRepo{}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	target := 200.0
	metrics := []metricashorno.MetricaHorno{{
		ReceivedAt:     now.Add(-time.Second),
		TempHorno1:     floatPtr(target),
		VelocidadCinta: floatPtr(0.1),
		TempObjetivo:   floatPtr(target),
	}}
	loop := newPreheatLoop(now, repo, broker, propuestaOK(), target, 0.35, 2.0, metrics)

	for i := 0; i < 2; i++ {
		repo.lastDecision = nil
		loop.tick(context.Background())
		if loop.fase != FaseCalentando {
			t.Fatalf("tick %d: fase = %q, want CALENTANDO", i+1, loop.fase)
		}
		if loop.propuestaPendiente == nil || loop.propuestaPendiente.Temp1 != target || loop.propuestaPendiente.Velocidad != 0 {
			t.Fatalf("tick %d: preheat debe proponer T=%.1f V=0; got=%+v", i+1, target, loop.propuestaPendiente)
		}
		if repo.lastDecision == nil || repo.lastDecision.RazonSkipped == nil || *repo.lastDecision.RazonSkipped != "precalentando" {
			t.Fatalf("tick %d: se esperaba decisión precalentando", i+1)
		}
	}

	repo.lastDecision = nil
	loop.tick(context.Background())

	if loop.fase != FaseAdaptando {
		t.Fatalf("fase = %q, want ADAPTANDO tras 3 muestras", loop.fase)
	}
	if loop.muestrasEnTolerancia != 0 {
		t.Fatalf("contador no se reseteó: %d", loop.muestrasEnTolerancia)
	}
	if loop.propuestaPendiente == nil || loop.propuestaPendiente.Velocidad != 0.35 {
		t.Fatalf("transición debe llevar V=0.35; got=%+v", loop.propuestaPendiente)
	}
	if repo.lastDecision == nil || repo.lastDecision.RazonSkipped != nil {
		t.Fatalf("la transición no debe llevar razon_skipped; got=%+v", repo.lastDecision)
	}
	if got := drainBroadcasts(client); got != 4 {
		t.Fatalf("broadcasts = %d, want 4 (3 preheat + 1 transición)", got)
	}
}

// TestPreheatSinTelemetriaPersisteSkip verifica que sin TempHorno1 no se
// propone nada y se registra el skip.
func TestPreheatSinTelemetriaPersisteSkip(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeDecisionesRepo{}
	loop := newPreheatLoop(now, repo, sse.NewBroker(), propuestaOK(), 200, 0.35, 2.0, []metricashorno.MetricaHorno{{
		ReceivedAt: now.Add(-time.Second),
		HumedadHorno: func() *float64 {
			v := 5.0
			return &v
		}(),
	}})

	loop.tick(context.Background())

	if repo.saveCalled != 1 || repo.lastDecision == nil {
		t.Fatalf("se esperaba una decisión skip persistida")
	}
	if repo.lastDecision.RazonSkipped == nil || *repo.lastDecision.RazonSkipped != "ventana_sin_muestras" {
		t.Fatalf("razon_skipped = %v, want ventana_sin_muestras", repo.lastDecision.RazonSkipped)
	}
	if loop.propuestaPendiente != nil {
		t.Fatalf("no debe publicar propuesta sin telemetría")
	}
	if loop.fase != FaseCalentando {
		t.Fatalf("fase = %q, want CALENTANDO", loop.fase)
	}
}

// TestPreheatTimeoutAborta verifica que un horno que nunca llega a la
// tolerancia no deja el loop esperando para siempre.
func TestPreheatTimeoutAborta(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeDecisionesRepo{}
	alertas := &fakeAlertaRepoControl{}
	loop := newPreheatLoop(now, repo, sse.NewBroker(), propuestaOK(), 200, 0.35, 2.0, nil)
	loop.SetAlertas(alertas)
	loop.preheatInicio = now.Add(-time.Hour)
	loop.preheatTimeoutSeg = 1800

	loop.tick(context.Background())

	if !loop.shouldExit {
		t.Fatalf("el loop debe abortar tras el timeout")
	}
	if repo.lastDecision == nil || repo.lastDecision.RazonSkipped == nil ||
		*repo.lastDecision.RazonSkipped != "precalentamiento_timeout" {
		t.Fatalf("razon_skipped = %v, want precalentamiento_timeout", repo.lastDecision)
	}
	if loop.propuestaPendiente != nil {
		t.Fatalf("no debe publicar propuesta al abortar")
	}
	if len(alertas.guardadas) != 1 {
		t.Fatalf("se esperaba 1 alerta de timeout, got %d", len(alertas.guardadas))
	}
	a := alertas.guardadas[0]
	if a.Tipo != alerta.TipoPrecalentamientoTimeout {
		t.Errorf("tipo = %q, want %q", a.Tipo, alerta.TipoPrecalentamientoTimeout)
	}
	if a.Estado != alerta.EstadoActiva {
		t.Errorf("estado = %q, want %q", a.Estado, alerta.EstadoActiva)
	}
	if a.LoteID == nil || *a.LoteID != "lote-1" {
		t.Errorf("lote_id = %v, want lote-1", a.LoteID)
	}
}

// TestRunFaseInicialAdaptandoSaltaPreheat verifica que una reanudación puede
// forzar ADAPTANDO y run no entra en el precalentamiento.
func TestRunFaseInicialAdaptandoSaltaPreheat(t *testing.T) {
	casos := []struct {
		name     string
		fase     string
		wantFase string
	}{
		{"forzada-adaptando", FaseAdaptando, FaseAdaptando},
		{"sin-forzar-automatico-frio", "", FaseCalentando},
	}
	for _, tc := range casos {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			loop := newSupervisionLoop(now, &fakeDecisionesRepo{}, sse.NewBroker(), propuestaOK())
			loop.modo = lote_sector.ModoAutomatico
			loop.clienteLote = &fakeLoteLookupControl{lote: &lote_sector.Lote{
				ID: "lote-1", Estado: lote_sector.EstadoCerrado, Modo: lote_sector.ModoAutomatico,
			}}
			loop.SetFaseInicial(tc.fase)

			loop.run(context.Background())

			if loop.fase != tc.wantFase {
				t.Fatalf("fase = %q, want %q", loop.fase, tc.wantFase)
			}
		})
	}
}

// TestSetFaseInicialIgnoraValoresInvalidos verifica que una fase desconocida no
// altera la derivación por defecto.
func TestSetFaseInicialIgnoraValoresInvalidos(t *testing.T) {
	loop := &Loop{}
	loop.SetFaseInicial("VOLANDO")
	if loop.FaseInicial() != "" {
		t.Fatalf("faseInicial = %q, want vacío", loop.FaseInicial())
	}
}

// TestLimitesVigentesAplicaTuningDelLote verifica que el tuning del panel
// aplique en el tick siguiente sin reiniciar el loop.
func TestLimitesVigentesAplicaTuningDelLote(t *testing.T) {
	loop := &Loop{limites: optimizador.Limites{
		TempMin: 100, TempMax: 300, VelMin: 0.05, VelMax: 0.5,
		DeltaTemp1: 10, DeltaVel: 0.05,
		UmbralCrudo: 5, UmbralQuem: 5, ToleranciaTempC: 2,
	}}
	deltaT, deltaV := 4.0, 0.01
	lote := &lote_sector.Lote{
		DeltaTemp1MaxC: &deltaT, DeltaVelMax: &deltaV,
		UmbralAceptacionCrudo: 3, UmbralAceptacionQuemado: 4, ToleranciaTempC: 1.5,
	}

	got := loop.limitesVigentes(lote)
	if got.DeltaTemp1 != deltaT || got.DeltaVel != deltaV {
		t.Fatalf("deltas del lote no aplicados: %+v", got)
	}
	if got.UmbralCrudo != 3 || got.UmbralQuem != 4 || got.ToleranciaTempC != 1.5 {
		t.Fatalf("umbrales/tolerancia no aplicados: %+v", got)
	}
	// Los rangos de la receta se conservan del arranque.
	if got.TempMin != 100 || got.TempMax != 300 || got.VelMax != 0.5 {
		t.Fatalf("rango del producto alterado: %+v", got)
	}

	// Sin lote (o sin overrides) se comporta como el arranque.
	if got := loop.limitesVigentes(nil); got.DeltaTemp1 != 10 {
		t.Fatalf("sin lote debe conservar el arranque: %+v", got)
	}
}

// newCalidadLoop arma un Loop mínimo para ejercitar evaluarCalidad.
func newCalidadLoop(alertas alerta.Repository) *Loop {
	return &Loop{
		loteID:         "lote-1",
		sectorID:       "horno-1",
		ventanaEvalSeg: 600,
		alertas:        alertas,
	}
}

// TestEvaluarCalidadVentanaVaciaNoRearma verifica que una ventana sin eventos
// no resetee el latch: no hay datos para afirmar que la condición se normalizó.
func TestEvaluarCalidadVentanaVaciaNoRearma(t *testing.T) {
	repo := &fakeAlertaRepoControl{}
	loop := newCalidadLoop(repo)
	lote := &lote_sector.Lote{UmbralAceptacionQuemado: 5}

	loop.evaluarCalidad(lote, 0, 12.5, true)
	loop.evaluarCalidad(lote, 0, 0, false) // ventana vacía
	loop.evaluarCalidad(lote, 0, 12.5, true)
	if len(repo.guardadas) != 1 {
		t.Fatalf("la ventana vacía no debe rearmar; got %d alertas", len(repo.guardadas))
	}
}

// TestEvaluarCalidadQuemadosUnaPorPeriodo verifica que se dispara sobre el
// umbral, no duplica en la evaluación siguiente y vuelve a disparar tras
// normalizarse.
func TestEvaluarCalidadQuemadosUnaPorPeriodo(t *testing.T) {
	repo := &fakeAlertaRepoControl{}
	loop := newCalidadLoop(repo)
	lote := &lote_sector.Lote{UmbralAceptacionQuemado: 5}

	loop.evaluarCalidad(lote, 0, 12.5, true)
	if len(repo.guardadas) != 1 {
		t.Fatalf("se esperaba 1 alerta de quemados, got %d", len(repo.guardadas))
	}
	a := repo.guardadas[0]
	if a.Tipo != alerta.TipoPorcentajeQuemadosAlto || a.Severidad != alerta.SeveridadWarning ||
		a.Estado != alerta.EstadoActiva {
		t.Fatalf("alerta de quemados mal formada: %+v", a)
	}
	if a.LoteID == nil || *a.LoteID != "lote-1" || a.HornoID != "" {
		t.Fatalf("la alerta debe atarse al lote sin horno: %+v", a)
	}

	// Sigue sobre el umbral: no duplica.
	loop.evaluarCalidad(lote, 0, 20, true)
	if len(repo.guardadas) != 1 {
		t.Fatalf("no debe duplicar mientras la condición persiste; got %d", len(repo.guardadas))
	}

	// Se normaliza y luego vuelve a subir: nueva alerta.
	loop.evaluarCalidad(lote, 0, 3, true)
	loop.evaluarCalidad(lote, 0, 9, true)
	if len(repo.guardadas) != 2 {
		t.Fatalf("debe rearmarse tras normalizar; got %d alertas", len(repo.guardadas))
	}
}

// TestEvaluarCalidadCrudosUnaPorPeriodo cubre el mismo ciclo para crudos.
func TestEvaluarCalidadCrudosUnaPorPeriodo(t *testing.T) {
	repo := &fakeAlertaRepoControl{}
	loop := newCalidadLoop(repo)
	lote := &lote_sector.Lote{UmbralAceptacionCrudo: 5}

	loop.evaluarCalidad(lote, 11, 0, true)
	loop.evaluarCalidad(lote, 11, 0, true)
	if len(repo.guardadas) != 1 {
		t.Fatalf("se esperaba 1 alerta de crudos sin duplicar, got %d", len(repo.guardadas))
	}
	if repo.guardadas[0].Tipo != alerta.TipoPorcentajeCrudosAlto {
		t.Fatalf("tipo = %q, want %q", repo.guardadas[0].Tipo, alerta.TipoPorcentajeCrudosAlto)
	}

	loop.evaluarCalidad(lote, 2, 0, true)
	loop.evaluarCalidad(lote, 8, 0, true)
	if len(repo.guardadas) != 2 {
		t.Fatalf("debe rearmarse tras normalizar; got %d alertas", len(repo.guardadas))
	}
}

// TestEvaluarCalidadUsaUmbralesDelLoteYDefault verifica que el umbral del lote
// manda y que 0 cae al default 5.0.
func TestEvaluarCalidadUsaUmbralesDelLoteYDefault(t *testing.T) {
	repo := &fakeAlertaRepoControl{}
	loop := newCalidadLoop(repo)

	lote := &lote_sector.Lote{UmbralAceptacionQuemado: 8}
	loop.evaluarCalidad(lote, 0, 7, true)
	if len(repo.guardadas) != 0 {
		t.Fatalf("7%% no supera el umbral del lote 8%%; got %d alertas", len(repo.guardadas))
	}
	loop.evaluarCalidad(lote, 0, 9, true)
	if len(repo.guardadas) != 1 {
		t.Fatalf("9%% debe superar el umbral del lote; got %d alertas", len(repo.guardadas))
	}

	// Umbral 0 => default 5.0.
	repo.guardadas = nil
	loop.alertaQuemadosActiva = false
	defaultLote := &lote_sector.Lote{}
	loop.evaluarCalidad(defaultLote, 0, 4.9, true)
	if len(repo.guardadas) != 0 {
		t.Fatalf("4.9%% no supera el default 5%%; got %d alertas", len(repo.guardadas))
	}
	loop.evaluarCalidad(defaultLote, 0, 5.1, true)
	if len(repo.guardadas) != 1 {
		t.Fatalf("5.1%% debe superar el default 5%%; got %d alertas", len(repo.guardadas))
	}
}

// TestEvaluarTemperaturaFueraDeTolerancia verifica la alerta WARNING de T1
// fuera de objetivo, el latch por período continuo y el rearme al volver a
// tolerancia.
func TestEvaluarTemperaturaFueraDeTolerancia(t *testing.T) {
	repo := &fakeAlertaRepoControl{}
	loop := &Loop{loteID: "lote-1", sectorID: "horno-1", alertas: repo, ventanaEvalSeg: 600}
	lote := &lote_sector.Lote{Temp1Objetivo: floatPtr(200), ToleranciaTempC: 2}

	muestras := func(vals ...float64) []metricashorno.MetricaHorno {
		out := make([]metricashorno.MetricaHorno, len(vals))
		for i, v := range vals {
			out[i] = metricashorno.MetricaHorno{TempHorno1: floatPtr(v)}
		}
		return out
	}

	// Promedio 210 vs objetivo 200: fuera de la tolerancia de 2 °C.
	loop.evaluarTemperatura(context.Background(), lote, muestras(210, 211, 209))
	if len(repo.guardadas) != 1 {
		t.Fatalf("se esperaba 1 alerta de temperatura, got %d", len(repo.guardadas))
	}
	a := repo.guardadas[0]
	if a.Tipo != alerta.TipoTemperaturaHorno || a.Severidad != alerta.SeveridadWarning ||
		a.Estado != alerta.EstadoActiva {
		t.Fatalf("alerta de temperatura mal formada: %+v", a)
	}
	if a.LoteID == nil || *a.LoteID != "lote-1" || a.HornoID != "" {
		t.Fatalf("la alerta debe atarse al lote sin horno: %+v", a)
	}

	// Sigue fuera: no duplica.
	loop.evaluarTemperatura(context.Background(), lote, muestras(215, 215, 215))
	if len(repo.guardadas) != 1 {
		t.Fatalf("no debe duplicar mientras la condición persiste; got %d", len(repo.guardadas))
	}

	// Vuelve a tolerancia y luego se sale de nuevo: nueva alerta.
	loop.evaluarTemperatura(context.Background(), lote, muestras(200, 201, 199))
	loop.evaluarTemperatura(context.Background(), lote, muestras(210, 211, 209))
	if len(repo.guardadas) != 2 {
		t.Fatalf("debe rearmarse tras normalizar; got %d alertas", len(repo.guardadas))
	}
}
