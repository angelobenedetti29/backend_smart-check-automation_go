package control

import (
	"context"
	"testing"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
)

// fakeEventosRepo implementa la interface mínima para los tests de Loop.
// Devuelve los conteos que el test configure.
type fakeEventosRepo struct {
	okN, crudoN, quemadoN int
	err                 error
	calls               int
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
		loteID: "test-lote",
		pesos:  optimizador.Pesos{WC: 1.0, WQ: 1.0},
		eventos: &fakeEventosRepo{okN: 0, crudoN: 0, quemadoN: 0},
		now:    func() time.Time { return time.Unix(0, 0) },
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
		loteID: "test-lote",
		pesos:  optimizador.Pesos{WC: 1.0, WQ: 1.0, DT: 0.05, DC: 0.5},
		eventos: &fakeEventosRepo{okN: 6, crudoN: 3, quemadoN: 1},
		now:    func() time.Time { return time.Unix(0, 0) },
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

// TestCalcularZObservadoErrorRepo verifica que un fallo del repo NO rompe el
// tick: devuelve (nil, false) y los % se quedan en 0, coherente con "sin datos
// observados".
func TestCalcularZObservadoErrorRepo(t *testing.T) {
	l := &Loop{
		loteID: "test-lote",
		pesos:  optimizador.Pesos{WC: 1.0, WQ: 1.0},
		eventos: &fakeEventosRepo{err: context.DeadlineExceeded},
		now:    func() time.Time { return time.Unix(0, 0) },
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