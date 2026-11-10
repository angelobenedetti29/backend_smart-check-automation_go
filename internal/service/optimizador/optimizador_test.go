package optimizador_test

import (
	"context"
	"math"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
)

// fakePredictor devuelve una salida determinística según la temperatura
// (más caliente → más quemados; más frío → más crudos). Sirve para
// ejercitar el optimizador sin depender del sidecar.
type fakePredictor struct{}

func (fakePredictor) Predict(_ context.Context, in optimizador.EntradasPredictor) (optimizador.PrediccionSalida, error) {
	t1 := in.TempHorno1
	// Centro ideal = 200 °C. Quemados crecen +1%/°C por encima; crudos +1%/°C por debajo.
	pct_quemados := math.Max(0, t1-200) * 1.0
	pct_crudos := math.Max(0, 200-t1) * 1.0
	if pct_quemados+pct_crudos > 100 {
		exceso := pct_quemados + pct_crudos - 100
		if pct_quemados > pct_crudos {
			pct_quemados -= exceso
		} else {
			pct_crudos -= exceso
		}
	}
	pct_ok := 100 - pct_crudos - pct_quemados
	return optimizador.PrediccionSalida{
		PctCrudos:     pct_crudos,
		PctOk:         pct_ok,
		PctQuemados:   pct_quemados,
		ModelVersion:  "fake_v1",
		EsPlaceholder: false,
	}, nil
}

func TestLimitesConsistente(t *testing.T) {
	lim := optimizador.Limites{
		TempMin: 150, TempMax: 250,
		VelMin: 0.05, VelMax: 0.50,
		DeltaTemp1: 10, DeltaVel: 0.05,
	}
	if !limConsistente(lim) {
		t.Fatal("limites deberían ser consistentes")
	}

	malos := optimizador.Limites{TempMin: 250, TempMax: 150, VelMin: 0.05, VelMax: 0.50, DeltaTemp1: 10, DeltaVel: 0.05}
	if limConsistente(malos) {
		t.Fatal("TempMin >= TempMax debería ser inconsistente")
	}
}

// limConsistente delega en el método del paquete (exportamos uno fino
// para tests) o se reimplementa acá.
func limConsistente(l optimizador.Limites) bool {
	if l.TempMin >= l.TempMax || l.VelMin >= l.VelMax {
		return false
	}
	if l.DeltaTemp1 <= 0 || l.DeltaVel <= 0 {
		return false
	}
	return true
}

func TestResolverEligeMejorCandidato(t *testing.T) {
	opt := optimizador.NewOptimizer(fakePredictor{})
	ctx := context.Background()

	// Estado actual: T=210 (sobrecalentado, alto pct_quemados). El optimizador
	// debería proponer bajar la temperatura (hacia 200, el centro ideal).
	estado := optimizador.Estado{
		TempObjetivo: 210,
		VelObjetivo:  0.20,
	}
	lim := optimizador.LimitesDe(&producto.Producto{
		TempMin: 150, TempMax: 250,
		VelocidadCintaMin: 0.05, VelocidadCintaMax: 0.50,
		DeltaTemp1MaxC: 10,
		DeltaVelMax:    0.05,
	}, nil)
	pesos := optimizador.Pesos{WC: 1.0, WQ: 1.0, WO: 0.0, DT: 0.05, DC: 0.5}

	p, err := opt.Resolver(ctx, "prod-x", estado, lim, pesos)
	if err != nil {
		t.Fatalf("Resolver: %v", err)
	}
	if p.Temp1 >= 210 {
		t.Errorf("optimizador no bajó T1: actual=%.2f propuesta=%.2f", estado.TempObjetivo, p.Temp1)
	}
	if p.Temp1 < 200 {
		t.Errorf("optimizador bajó demasiado: propuesta=%.2f (debería acercarse a 200)", p.Temp1)
	}
	if p.Prediccion.PctQuemados > p.Prediccion.PctCrudos {
		t.Errorf("optimizador eligió un candidato con más quemados que crudos: %.2f vs %.2f",
			p.Prediccion.PctQuemados, p.Prediccion.PctCrudos)
	}
}

func TestResolverRespetaDeltaMaxPorCiclo(t *testing.T) {
	opt := optimizador.NewOptimizer(fakePredictor{})
	ctx := context.Background()

	estado := optimizador.Estado{TempObjetivo: 220, VelObjetivo: 0.20}
	lim := optimizador.Limites{
		TempMin: 100, TempMax: 300,
		VelMin: 0.05, VelMax: 0.50,
		DeltaTemp1: 5, // restrictivo: sólo ±5 °C por ciclo
		DeltaVel:   0.02,
	}
	pesos := optimizador.Pesos{WC: 1.0, WQ: 1.0, WO: 0.0, DT: 0.05, DC: 0.5}
	p, err := opt.Resolver(ctx, "prod-x", estado, lim, pesos)
	if err != nil {
		t.Fatalf("Resolver: %v", err)
	}
	if math.Abs(p.Temp1-estado.TempObjetivo) > lim.DeltaTemp1+1e-9 {
		t.Errorf("ΔT1=%.2f excede delta_max=%.2f", p.Temp1-estado.TempObjetivo, lim.DeltaTemp1)
	}
	if math.Abs(p.Velocidad-estado.VelObjetivo) > lim.DeltaVel+1e-9 {
		t.Errorf("ΔV=%.4f excede delta_max=%.4f", p.Velocidad-estado.VelObjetivo, lim.DeltaVel)
	}
}

func TestPesosDesdeLote(t *testing.T) {
	if got := optimizador.PesosDesdeLote(nil); got.WC != 1.0 {
		t.Errorf("defaults no aplicados: %+v", got)
	}
	p := &struct{}{} // no es un PesosObjetivo válido; el cast fallaría en runtime
	_ = p
}

// TestLimitesDePrefiereOverridesDelLote verifica que los Δmáx del lote ganen al
// valor del producto y que un lote sin override conserve los del producto.
func TestLimitesDePrefiereOverridesDelLote(t *testing.T) {
	prod := &producto.Producto{
		TempMin: 100, TempMax: 300,
		VelocidadCintaMin: 0.05, VelocidadCintaMax: 0.5,
		DeltaTemp1MaxC: 10, DeltaVelMax: 0.05,
	}
	dt, dv := 4.0, 0.01
	got := optimizador.LimitesDe(prod, &lote_sector.Lote{DeltaTemp1MaxC: &dt, DeltaVelMax: &dv})
	if got.DeltaTemp1 != dt || got.DeltaVel != dv {
		t.Fatalf("overrides del lote no aplicados: ΔT=%.2f ΔV=%.4f", got.DeltaTemp1, got.DeltaVel)
	}

	sinOverride := optimizador.LimitesDe(prod, &lote_sector.Lote{})
	if sinOverride.DeltaTemp1 != 10 || sinOverride.DeltaVel != 0.05 {
		t.Fatalf("sin override deben quedar los del producto: ΔT=%.2f ΔV=%.4f",
			sinOverride.DeltaTemp1, sinOverride.DeltaVel)
	}
}

// capturaPredictor registra las entradas del último Predict.
type capturaPredictor struct{ in optimizador.EntradasPredictor }

func (c *capturaPredictor) Predict(_ context.Context, in optimizador.EntradasPredictor) (optimizador.PrediccionSalida, error) {
	c.in = in
	return optimizador.PrediccionSalida{PctOk: 100}, nil
}

// TestResolverPropagaRangosAlPredictor verifica que los rangos del producto
// viajen en cada evaluación (feature ranges: evita train/serve skew).
func TestResolverPropagaRangosAlPredictor(t *testing.T) {
	pred := &capturaPredictor{}
	opt := optimizador.NewOptimizer(pred)
	lim := optimizador.Limites{
		TempMin: 150, TempMax: 250,
		VelMin: 0.1, VelMax: 0.4,
		DeltaTemp1: 2, DeltaVel: 0.02,
	}
	if _, err := opt.Resolver(context.Background(), "prod-x",
		optimizador.Estado{TempObjetivo: 200, VelObjetivo: 0.2}, lim,
		optimizador.Pesos{WO: 1}); err != nil {
		t.Fatalf("Resolver: %v", err)
	}
	got := pred.in
	if got.TempMin != 150 || got.TempMax != 250 || got.VelMin != 0.1 || got.VelMax != 0.4 {
		t.Fatalf("rangos no propagados: %+v", got)
	}
}
