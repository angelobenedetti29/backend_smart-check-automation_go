package control

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
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
	t2 := 170.0
	l := &Loop{
		propuestaPendiente: &optimizador.Propuesta{Temp1: 180, Temp2: &t2, Velocidad: 0.45},
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
	if s1.Temp2 == nil || *s1.Temp2 != 170 {
		t.Fatalf("Temp2 no propagado al snapshot: %+v", s1.Temp2)
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

// fakeDecisionesRepo implementa la interface mínima decisionesRepo del Loop.
type fakeDecisionesRepo struct {
	saveCalled      int
	saveErr         error
	lastAplicadoPor string
	marcarCalled    int
	marcarArgs      struct {
		decisionID  string
		temp, vel   float64
		aplicadoPor string
	}
	marcarErr error
}

// Save simula el RETURNING de la DB: completa ID/CreatedAt (salvo error).
func (f *fakeDecisionesRepo) Save(_ context.Context, d *decisiones_control.DecisionControl) error {
	f.saveCalled++
	f.lastAplicadoPor = d.AplicadoPor
	if f.saveErr != nil {
		return f.saveErr
	}
	d.ID = "dec-save-1"
	d.CreatedAt = time.Now().UTC()
	return nil
}

func (f *fakeDecisionesRepo) MarcarAplicada(_ context.Context, id string, t, v float64, ap string) error {
	f.marcarCalled++
	f.marcarArgs.decisionID = id
	f.marcarArgs.temp, f.marcarArgs.vel, f.marcarArgs.aplicadoPor = t, v, ap
	return f.marcarErr
}

// fakeConsignaSrv implementa ConsignaDispatchService y devuelve una consigna
// de éxito con los setpoints configurados.
type fakeConsignaSrv struct {
	cons *consigna.Consigna
	err  error
}

func (f *fakeConsignaSrv) Dispatch(_ context.Context, _ consigna.ConsignaManualRequest, _ consigna.Origen, _ *string) (*consigna.Consigna, error) {
	return f.cons, f.err
}

// TestNewLoopAsignaLoteID verifica que NewLoop propaga el loteID al campo del
// struct (sin esto el tick persiste con lote_id vacío).
func TestNewLoopAsignaLoteID(t *testing.T) {
	loop := NewLoop("lote-xyz", nil, nil, nil, nil, nil, nil, nil, nil)
	if loop.loteID != "lote-xyz" {
		t.Fatalf("loteID = %q, want %q", loop.loteID, "lote-xyz")
	}
}

// TestAplicarPropuestaMarcaFilaComoAplicada verifica que tras un dispatch
// exitoso la fila persistida se marca como aplicada por OPERARIO.
func TestAplicarPropuestaMarcaFilaComoAplicada(t *testing.T) {
	repo := &fakeDecisionesRepo{}
	srv := &fakeConsignaSrv{cons: &consigna.Consigna{TemperaturaObjetivo: 180, VelocidadCintaObjetivo: 0.45}}
	l := &Loop{
		propuestaPendiente: &optimizador.Propuesta{Temp1: 180, Velocidad: 0.45},
		lastDecisionID:     "dec-1",
		decisiones:         repo,
		consigna:           srv,
	}

	cons, err := l.AplicarPropuesta(context.Background(), "operario@x")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if cons == nil {
		t.Fatalf("se esperaba consigna no-nil")
	}
	if repo.marcarCalled != 1 {
		t.Fatalf("MarcarAplicada llamado %d veces, want 1", repo.marcarCalled)
	}
	if repo.marcarArgs.decisionID != "dec-1" {
		t.Fatalf("decisionID = %q, want dec-1", repo.marcarArgs.decisionID)
	}
	if repo.marcarArgs.temp != 180 || repo.marcarArgs.vel != 0.45 {
		t.Fatalf("setpoints mal propagados: %+v", repo.marcarArgs)
	}
	if repo.marcarArgs.aplicadoPor != decisiones_control.AplicadoPorOperario {
		t.Fatalf("aplicadoPor = %q, want %q", repo.marcarArgs.aplicadoPor, decisiones_control.AplicadoPorOperario)
	}
	if l.propuestaPendiente != nil {
		t.Fatalf("propuestaPendiente debe quedar nil")
	}
	if l.lastDecisionID != "" {
		t.Fatalf("lastDecisionID debe quedar vacío")
	}
}

// TestAplicarPropuestaSinDecisionIDNoFalla verifica el caso en que la
// persistencia previa falló y no hay decisionID que marcar.
func TestAplicarPropuestaSinDecisionIDNoFalla(t *testing.T) {
	repo := &fakeDecisionesRepo{}
	srv := &fakeConsignaSrv{cons: &consigna.Consigna{TemperaturaObjetivo: 180, VelocidadCintaObjetivo: 0.45}}
	l := &Loop{
		propuestaPendiente: &optimizador.Propuesta{Temp1: 180, Velocidad: 0.45},
		lastDecisionID:     "",
		decisiones:         repo,
		consigna:           srv,
	}

	cons, err := l.AplicarPropuesta(context.Background(), "operario@x")
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if cons == nil {
		t.Fatalf("se esperaba consigna no-nil")
	}
	if repo.marcarCalled != 0 {
		t.Fatalf("no debería llamar MarcarAplicada sin decisionID; calls=%d", repo.marcarCalled)
	}
}

// TestAplicarPropuestaErrorEnMarcarNoFallaElDispatch verifica que un error al
// marcar el audit no oculta el dispatch exitoso.
func TestAplicarPropuestaErrorEnMarcarNoFallaElDispatch(t *testing.T) {
	repo := &fakeDecisionesRepo{marcarErr: context.DeadlineExceeded}
	srv := &fakeConsignaSrv{cons: &consigna.Consigna{TemperaturaObjetivo: 180, VelocidadCintaObjetivo: 0.45}}
	l := &Loop{
		propuestaPendiente: &optimizador.Propuesta{Temp1: 180, Velocidad: 0.45},
		lastDecisionID:     "dec-2",
		decisiones:         repo,
		consigna:           srv,
	}

	cons, err := l.AplicarPropuesta(context.Background(), "operario@x")
	if err != nil {
		t.Fatalf("el error de MarcarAplicada no debe propagarse: %v", err)
	}
	if cons == nil {
		t.Fatalf("se esperaba consigna no-nil")
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

// TestTickAutoDespachaYNoPublica verifica el camino AUTOMATICO: tick despacha
// (Save con aplicado_por=MPC), NO deja propuesta pendiente y NO emite el
// broadcast de control.propuesta reservado a SUPERVISION.
func TestTickAutoDespachaYNoPublica(t *testing.T) {
	repo := &fakeDecisionesRepo{}
	broker := sse.NewBroker()
	client := broker.Subscribe()
	defer broker.Unsubscribe(client)

	now := time.Now().UTC()
	loop := newSupervisionLoop(now, repo, broker, fakePredictorControl{out: optimizador.PrediccionSalida{PctCrudos: 10, PctOk: 80, PctQuemados: 10}})
	loop.modo = lote_sector.ModoAutomatico
	loop.consigna = &fakeConsignaSrv{cons: &consigna.Consigna{TemperaturaObjetivo: 205, VelocidadCintaObjetivo: 0.22}}

	loop.tick(context.Background())

	if repo.saveCalled != 1 {
		t.Fatalf("Save llamado %d veces, want 1", repo.saveCalled)
	}
	if repo.lastAplicadoPor != decisiones_control.AplicadoPorMPC {
		t.Fatalf("aplicado_por = %q, want %q", repo.lastAplicadoPor, decisiones_control.AplicadoPorMPC)
	}
	if loop.propuestaPendiente != nil {
		t.Fatalf("AUTOMATICO no debe dejar propuesta pendiente")
	}
	if loop.lastDecisionID != "" {
		t.Fatalf("AUTOMATICO no debe setear lastDecisionID; got=%q", loop.lastDecisionID)
	}
	select {
	case ev := <-client.Events:
		t.Fatalf("AUTOMATICO no debe emitir control.propuesta; got=%s", ev.EventType)
	default:
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
