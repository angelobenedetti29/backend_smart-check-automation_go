package optimizador

import (
	"context"
	"math"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
)

// constPredictor devuelve siempre la misma predicción. Sirve para aislar los
// términos de Z que no dependen del predictor (WO, tolerancia de T, ΔV).
type constPredictor struct {
	out PrediccionSalida
}

func (p constPredictor) Predict(_ context.Context, _ EntradasPredictor) (PrediccionSalida, error) {
	return p.out, nil
}

// TestZCalidadEsUnicaFuenteDeVerdad ancla la fórmula compartida entre la Z
// predicha (objetivo) y la observada (control.calcularZObservado).
func TestZCalidadEsUnicaFuenteDeVerdad(t *testing.T) {
	pesos := Pesos{WC: 1, WQ: 1, WO: 1, DT: 0.05, DC: 0.5}
	// Z = 1·10 + 1·20 - 1·70/100 = 30 - 0.7 = 29.3.
	if got := ZCalidad(pesos, 10, 70, 20); math.Abs(got-29.3) > 1e-9 {
		t.Fatalf("ZCalidad = %.6f, want 29.3", got)
	}
	// Con WO=0 no hay contribución de OK: Z = 30.
	sinOK := Pesos{WC: 1, WQ: 1, WO: 0, DT: 0.05, DC: 0.5}
	if got := ZCalidad(sinOK, 10, 70, 20); got != 30 {
		t.Fatalf("ZCalidad con WO=0 = %.6f, want 30", got)
	}
}

// TestObjetivoIncluyeWO valida F-B4: WO actúa como recompensa aditiva sobre %OK.
// Con predC/predQ constantes, Z debe decrecer cuando pred.Ok crece. La
// normalización /100 de ZCalidad hace que WO=1 aporte 1 punto por cada 100% de
// OK, es decir 0.8 puntos por 80 puntos porcentuales.
func TestObjetivoIncluyeWO(t *testing.T) {
	o := NewOptimizer(constPredictor{})
	lim := Limites{}
	estado := Estado{TempObjetivo: 200, VelObjetivo: 0.2}
	pesos := Pesos{WO: 1.0}

	constante := PrediccionSalida{PctCrudos: 10, PctQuemados: 10}
	zOk0 := constante
	zOk0.PctOk = 0
	zOk80 := constante
	zOk80.PctOk = 80

	zBajo := o.objetivo(estado, lim, pesos, zOk0, 200, 0.2)
	zAlto := o.objetivo(estado, lim, pesos, zOk80, 200, 0.2)

	if zAlto >= zBajo {
		t.Fatalf("con WO=1 Z debe decrecer al crecer pred.Ok: Z(ok=0)=%.4f Z(ok=80)=%.4f", zBajo, zAlto)
	}
	if diff := zBajo - zAlto; diff < 0.7999 || diff > 0.8001 {
		t.Fatalf("diferencia por WO = %.4f, want 0.8", diff)
	}
}

// TestLimitesDesdeProductoYLote valida F-B3: los umbrales y la tolerancia salen
// del lote; sin lote se usan defaults conservadores.
func TestLimitesDesdeProductoYLote(t *testing.T) {
	prod := &producto.Producto{
		TempMin: 100, TempMax: 300,
		VelocidadCintaMin: 0.05, VelocidadCintaMax: 0.5,
		DeltaTemp1MaxC: 10, DeltaVelMax: 0.05,
	}
	lote := &lote_sector.Lote{
		UmbralAceptacionCrudo:   3.0,
		UmbralAceptacionQuemado: 4.0,
		ToleranciaTempC:         1.5,
	}

	got := LimitesDe(prod, lote)
	if got.UmbralCrudo != 3.0 || got.UmbralQuem != 4.0 || got.ToleranciaTempC != 1.5 {
		t.Fatalf("valores del lote no aplicados: crudo=%.2f quem=%.2f tol=%.2f",
			got.UmbralCrudo, got.UmbralQuem, got.ToleranciaTempC)
	}

	def := LimitesDe(prod, nil)
	if def.UmbralCrudo != 5.0 || def.UmbralQuem != 5.0 || def.ToleranciaTempC != 2.0 {
		t.Fatalf("defaults incorrectos: crudo=%.2f quem=%.2f tol=%.2f",
			def.UmbralCrudo, def.UmbralQuem, def.ToleranciaTempC)
	}
}

// TestObjetivoAplicaTolerancia valida la penalización suave por exceder la
// tolerancia de temperatura: base |ΔT|·DT + max(0, |ΔT|-tol)·DT.
func TestObjetivoAplicaTolerancia(t *testing.T) {
	o := NewOptimizer(constPredictor{})
	lim := Limites{ToleranciaTempC: 2.0}
	estado := Estado{TempObjetivo: 200, VelObjetivo: 0.2}
	pesos := Pesos{DT: 1.0}
	constante := PrediccionSalida{}

	zCentro := o.objetivo(estado, lim, pesos, constante, 200, 0.2)
	zCerca := o.objetivo(estado, lim, pesos, constante, 201, 0.2)
	zLejos := o.objetivo(estado, lim, pesos, constante, 205, 0.2)

	// Dentro de la tolerancia no hay penalty extra: Z = |ΔT|·DT.
	if zCentro != 0 || zCerca != 1.0 {
		t.Fatalf("dentro de tolerancia Z debe ser |ΔT|·DT: centro=%.3f cerca=%.3f", zCentro, zCerca)
	}
	// t1=actual+5, tol=2, DT=1 → base 5 + exceso (5-2)=3 → 8.
	if zLejos != 8.0 {
		t.Fatalf("Z(actual+5) = %.3f, want 8 (base 5 + exceso 3)", zLejos)
	}
	// Z(lejos) > Z(cerca) por la diferencia esperada (8 - 1 = 7).
	if zLejos-zCerca != 7.0 {
		t.Fatalf("Z(lejos)-Z(cerca) = %.3f, want 7", zLejos-zCerca)
	}
}

// TestObjetivoSinToleranciaCompatibilidad verifica que un Limites con
// ToleranciaTempC=0 (literal viejo) reproduce el comportamiento anterior: sólo
// penalización base |ΔT|·DT, sin término extra.
func TestObjetivoSinToleranciaCompatibilidad(t *testing.T) {
	o := NewOptimizer(constPredictor{})
	lim := Limites{} // ToleranciaTempC = 0
	estado := Estado{TempObjetivo: 200, VelObjetivo: 0.2}
	pesos := Pesos{DT: 1.0}
	constante := PrediccionSalida{}

	if got := o.objetivo(estado, lim, pesos, constante, 205, 0.2); got != 5.0 {
		t.Fatalf("sin tolerancia Z(actual+5) = %.3f, want 5 (sólo base)", got)
	}
}

// predictorPorTemp permite definir la salida en función de T1 para ejercitar
// las restricciones de calidad del optimizador.
type predictorPorTemp func(t1 float64) PrediccionSalida

func (f predictorPorTemp) Predict(_ context.Context, in EntradasPredictor) (PrediccionSalida, error) {
	return f(in.TempHorno1), nil
}

// TestResolverPrefiereCandidatoFactible verifica que un candidato que cumple
// los umbrales gana aunque su Z sea peor que el mejor Z absoluto (infeasible).
func TestResolverPrefiereCandidatoFactible(t *testing.T) {
	pred := predictorPorTemp(func(t1 float64) PrediccionSalida {
		switch t1 {
		case 210:
			// Mejor Z absoluto pero quemados por encima del umbral.
			return PrediccionSalida{PctCrudos: 0, PctOk: 100, PctQuemados: 20}
		case 190:
			// Z peor pero factible.
			return PrediccionSalida{PctCrudos: 10, PctOk: 0, PctQuemados: 0}
		default:
			return PrediccionSalida{PctCrudos: 100, PctOk: 0, PctQuemados: 0}
		}
	})
	opt := NewOptimizer(pred)
	estado := Estado{TempObjetivo: 200, VelObjetivo: 0.2}
	lim := Limites{
		TempMin: 100, TempMax: 300,
		VelMin: 0.05, VelMax: 0.5,
		DeltaTemp1: 10, DeltaVel: 0.05,
		UmbralCrudo: 10, UmbralQuem: 10,
	}
	pesos := Pesos{WC: 1, WQ: 0, WO: 1, DT: 0}

	p, err := opt.Resolver(context.Background(), "prod-x", estado, lim, pesos)
	if err != nil {
		t.Fatalf("Resolver: %v", err)
	}
	if p.Temp1 != 190 {
		t.Fatalf("Temp1 = %.2f, want 190 (factible)", p.Temp1)
	}
	if p.RestriccionRelajada {
		t.Fatalf("no debe marcarse relajada cuando hay candidato factible")
	}
}

// TestResolverRelajaSiNingunoEsFactible verifica que sin candidatos factibles
// se devuelve el mejor Z absoluto con la bandera de restricción relajada.
func TestResolverRelajaSiNingunoEsFactible(t *testing.T) {
	pred := predictorPorTemp(func(t1 float64) PrediccionSalida {
		if t1 == 210 {
			return PrediccionSalida{PctCrudos: 0, PctOk: 100, PctQuemados: 20}
		}
		return PrediccionSalida{PctCrudos: 20, PctOk: 0, PctQuemados: 20}
	})
	opt := NewOptimizer(pred)
	estado := Estado{TempObjetivo: 200, VelObjetivo: 0.2}
	lim := Limites{
		TempMin: 100, TempMax: 300,
		VelMin: 0.05, VelMax: 0.5,
		DeltaTemp1: 10, DeltaVel: 0.05,
		UmbralCrudo: 10, UmbralQuem: 10,
	}
	pesos := Pesos{WC: 1, WQ: 0, WO: 1, DT: 0}

	p, err := opt.Resolver(context.Background(), "prod-x", estado, lim, pesos)
	if err != nil {
		t.Fatalf("Resolver: %v", err)
	}
	if p.Temp1 != 210 {
		t.Fatalf("Temp1 = %.2f, want 210 (mejor Z absoluto)", p.Temp1)
	}
	if !p.RestriccionRelajada {
		t.Fatalf("debe marcarse RestriccionRelajada cuando no hay factibles")
	}
}

// TestResolverUmbralesCeroDesactivanRestriccion verifica que umbrales <= 0
// desactivan las restricciones de calidad.
func TestResolverUmbralesCeroDesactivanRestriccion(t *testing.T) {
	pred := predictorPorTemp(func(t1 float64) PrediccionSalida {
		if t1 == 210 {
			return PrediccionSalida{PctCrudos: 0, PctOk: 100, PctQuemados: 20}
		}
		return PrediccionSalida{PctCrudos: 10, PctOk: 0, PctQuemados: 0}
	})
	opt := NewOptimizer(pred)
	estado := Estado{TempObjetivo: 200, VelObjetivo: 0.2}
	lim := Limites{
		TempMin: 100, TempMax: 300,
		VelMin: 0.05, VelMax: 0.5,
		DeltaTemp1: 10, DeltaVel: 0.05,
		// UmbralCrudo/UmbralQuem = 0: sin restricción.
	}
	pesos := Pesos{WC: 1, WQ: 0, WO: 1, DT: 0}

	p, err := opt.Resolver(context.Background(), "prod-x", estado, lim, pesos)
	if err != nil {
		t.Fatalf("Resolver: %v", err)
	}
	if p.Temp1 != 210 {
		t.Fatalf("Temp1 = %.2f, want 210 (mejor Z sin restricción)", p.Temp1)
	}
	if p.RestriccionRelajada {
		t.Fatalf("umbrales <= 0 no deben marcar la propuesta como relajada")
	}
}

// TestResolverPasaCombustionYHumedadAlPredictor verifica que las features
// opcionales del estado llegan al predictor.
func TestResolverPasaCombustionYHumedadAlPredictor(t *testing.T) {
	comb := 55.5
	hum := 7.25
	var capturado EntradasPredictor
	opt := NewOptimizer(predictorPorTemp(func(float64) PrediccionSalida { return PrediccionSalida{} }))
	// Envolvemos el predictor con captura.
	opt.predictor = predictorCaptura{fn: func(in EntradasPredictor) (PrediccionSalida, error) {
		capturado = in
		return PrediccionSalida{}, nil
	}}

	estado := Estado{
		TempObjetivo: 200, VelObjetivo: 0.2,
		TempComb1: &comb, HumedadHorno: &hum,
	}
	lim := Limites{
		TempMin: 100, TempMax: 300,
		VelMin: 0.05, VelMax: 0.5,
		DeltaTemp1: 10, DeltaVel: 0.05,
	}
	if _, err := opt.Resolver(context.Background(), "prod-x", estado, lim, Pesos{}); err != nil {
		t.Fatalf("Resolver: %v", err)
	}
	if capturado.TempComb1 == nil || *capturado.TempComb1 != comb {
		t.Fatalf("TempComb1 no llegó al predictor: %+v", capturado.TempComb1)
	}
	if capturado.HumedadHorno == nil || *capturado.HumedadHorno != hum {
		t.Fatalf("HumedadHorno no llegó al predictor: %+v", capturado.HumedadHorno)
	}
}

type predictorCaptura struct {
	fn func(EntradasPredictor) (PrediccionSalida, error)
}

func (p predictorCaptura) Predict(_ context.Context, in EntradasPredictor) (PrediccionSalida, error) {
	return p.fn(in)
}

// TestLimitesDeConToleranciaCero verifica que una tolerancia 0 del lote se
// propaga y que, con ese Limites, objetivo() no aplica penalización extra.
func TestLimitesDeConToleranciaCero(t *testing.T) {
	prod := &producto.Producto{
		TempMin: 100, TempMax: 300,
		VelocidadCintaMin: 0.05, VelocidadCintaMax: 0.5,
		DeltaTemp1MaxC: 10, DeltaVelMax: 0.05,
	}
	lote := &lote_sector.Lote{ToleranciaTempC: 0}
	lim := LimitesDe(prod, lote)
	if lim.ToleranciaTempC != 0 {
		t.Fatalf("ToleranciaTempC = %v, want 0", lim.ToleranciaTempC)
	}

	o := NewOptimizer(constPredictor{})
	estado := Estado{TempObjetivo: 200, VelObjetivo: 0.2}
	pesos := Pesos{DT: 1.0}
	// |ΔT| = 10 con tolerancia 0 → sólo la base = 10, sin penalty extra.
	if got := o.objetivo(estado, lim, pesos, PrediccionSalida{}, 210, 0.2); got != 10.0 {
		t.Fatalf("Z(actual+10) = %.3f, want 10 (sin penalty extra)", got)
	}
}
