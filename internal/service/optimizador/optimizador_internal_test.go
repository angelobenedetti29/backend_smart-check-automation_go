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

	zBajo := o.objetivo(estado, lim, pesos, zOk0, 200, nil, 0.2)
	zAlto := o.objetivo(estado, lim, pesos, zOk80, 200, nil, 0.2)

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

	zCentro := o.objetivo(estado, lim, pesos, constante, 200, nil, 0.2)
	zCerca := o.objetivo(estado, lim, pesos, constante, 201, nil, 0.2)
	zLejos := o.objetivo(estado, lim, pesos, constante, 205, nil, 0.2)

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

	if got := o.objetivo(estado, lim, pesos, constante, 205, nil, 0.2); got != 5.0 {
		t.Fatalf("sin tolerancia Z(actual+5) = %.3f, want 5 (sólo base)", got)
	}
}

// TestLimitesDeConHorno2Habilitado valida que LimitesDe propaga el rango T2 y el
// ΔT2 máximo cuando el producto habilita el horno 2.
func TestLimitesDeConHorno2Habilitado(t *testing.T) {
	temp2Min, temp2Max, deltaTemp2 := 150.0, 200.0, 8.0
	prod := &producto.Producto{
		TempMin: 100, TempMax: 300,
		VelocidadCintaMin: 0.05, VelocidadCintaMax: 0.5,
		DeltaTemp1MaxC: 10, DeltaVelMax: 0.05,
		Horno2Habilitado: true,
		Temp2Min:         &temp2Min,
		Temp2Max:         &temp2Max,
		DeltaTemp2MaxC:   &deltaTemp2,
	}
	lim := LimitesDe(prod, nil)
	if !lim.Horno2Hab {
		t.Fatalf("Horno2Hab = false, want true")
	}
	if lim.Temp2Min == nil || *lim.Temp2Min != 150 {
		t.Fatalf("Temp2Min = %v, want 150", lim.Temp2Min)
	}
	if lim.Temp2Max == nil || *lim.Temp2Max != 200 {
		t.Fatalf("Temp2Max = %v, want 200", lim.Temp2Max)
	}
	if lim.DeltaTemp2 == nil || *lim.DeltaTemp2 != 8 {
		t.Fatalf("DeltaTemp2 = %v, want 8", lim.DeltaTemp2)
	}
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
	if got := o.objetivo(estado, lim, pesos, PrediccionSalida{}, 210, nil, 0.2); got != 10.0 {
		t.Fatalf("Z(actual+10) = %.3f, want 10 (sin penalty extra)", got)
	}
}
