// Package optimizador implementa el MPC (Model Predictive Control) que
// propone nuevos setpoints del horno (SCA-400).
//
// Horizonte: 1 paso (estático). No usamos solver convexo (gonum): la
// función de respuesta (XGBoost) es no-convexa, así que el beneficio de
// un LP/QP es marginal. En su lugar, muestreamos una grilla fina dentro
// de los Δmáx por ciclo alrededor del estado actual, evaluamos el
// predictor XGBoost en cada candidato, computamos el objetivo y
// elegimos el mínimo respetando las restricciones. Con Δtemp1=10 °C y
// paso=2 °C son 11 valores de T1; idem V → ≤ 121 candidatos por decisión.
// Con XGBoost a <1 ms por eval, la decisión tarda <1 s.
//
// Variables: T1 y V.
package optimizador

import (
	"context"
	"errors"
	"log"
	"math"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
)

// Predictor abstrae el sidecar XGBoost. La implementación real vive en
// internal/service/predictor y delega vía HTTP al sidecar Python.
type Predictor interface {
	Predict(ctx context.Context, in EntradasPredictor) (PrediccionSalida, error)
}

// EntradasPredictor son las features que consume el XGBoost por producto.
// TempMin/TempMax/VelMin/VelMax viajan en el payload para que el modelo se
// entrene y sirva con las mismas features (evita train/serve skew).
type EntradasPredictor struct {
	ProductoID     string
	TempHorno1     float64
	VelocidadCinta float64
	TempMin        float64
	TempMax        float64
	VelMin         float64
	VelMax         float64
	TempComb1      *float64
	HumedadHorno   *float64
}

// PrediccionSalida es la triplete que devuelve el XGBoost (o el placeholder).
type PrediccionSalida struct {
	PctCrudos     float64 `json:"pctCrudos"`
	PctOk         float64 `json:"pctOk"`
	PctQuemados   float64 `json:"pctQuemados"`
	ModelVersion  string  `json:"modelVersion"`
	EsPlaceholder bool    `json:"esPlaceholder"`
}

// Pesos son los coeficientes de la función objetivo. WC = peso de crudos,
// WQ = peso de quemados, WO = peso de %OK como recompensa (se suma -wo·predO
// en Z; defaults WO=1.0 de modo que Z baja cuando el candidato maximiza
// producto OK), DT = penalización por ΔT por °C, DC = penalización por ΔV por
// m/s.
type Pesos struct {
	WC float64
	WQ float64
	WO float64
	DT float64
	DC float64
}

// ZCalidad es la parte de Z que depende de los porcentajes observados o
// predichos (crudos/ok/quemados). Es la única fuente de verdad: objetivo()
// (predicción) y control.go (observación) deben llamarla con los mismos
// argumentos para que z_predicho y z_observado sean comparables.
func ZCalidad(p Pesos, pctCrudos, pctOk, pctQuemados float64) float64 {
	return p.WC*pctCrudos + p.WQ*pctQuemados - p.WO*pctOk/100
}

// Estado es la foto del horno al momento de optimizar.
type Estado struct {
	TempHorno1     float64
	VelocidadCinta float64
	TempObjetivo   float64 // setpoint activo de T1 (último aplicado)
	VelObjetivo    float64 // setpoint activo de V (último aplicado)
	TempComb1      *float64
	HumedadHorno   *float64
}

// Limites del producto: rangos y Δmáx por ciclo.
type Limites struct {
	TempMin         float64
	TempMax         float64
	VelMin          float64
	VelMax          float64
	DeltaTemp1      float64 // |ΔT1| máximo por ciclo
	DeltaVel        float64
	UmbralCrudo     float64 // % crudo aceptable
	UmbralQuem      float64 // % quemado aceptable
	ToleranciaTempC float64 // |ΔT| permitido vs TempObjetivo; candidatos fuera se penalizan
}

// Propuesta es el resultado del optimizador: los nuevos setpoints y los
// valores predichos. Aplicado queda en false hasta que el caller
// confirme el despacho.
type Propuesta struct {
	Temp1         float64          `json:"temp1"`
	Velocidad     float64          `json:"velocidad"`
	Z             float64          `json:"z"`
	Prediccion    PrediccionSalida `json:"prediccion"`
	VentanaInicio string           `json:"ventana_inicio,omitempty"`
	VentanaFin    string           `json:"ventana_fin,omitempty"`
	// RestriccionRelajada indica que ningún candidato cumplió los umbrales de
	// aceptación del producto y se devolvió el mejor Z absoluto.
	RestriccionRelajada bool `json:"restriccion_relajada,omitempty"`
}

// ErrSinPrediccion se retorna cuando el predictor falla de manera
// persistente y no se puede proponer un setpoint con confianza.
var ErrSinPrediccion = errors.New("optimizador: el predictor no devolvió predicciones válidas")

// ErrSinLimites se retorna cuando los límites del producto no son consistentes.
var ErrSinLimites = errors.New("optimizador: los límites del producto son inválidos")

// Optimizer aplica el MPC con horizonte 1.
type Optimizer struct {
	predictor Predictor
	// Paso de la grilla en °C (T) y m/s (V). Defaults sensatos si no se
	// configuran.
	pasoTemp float64
	pasoVel  float64
}

// NewOptimizer instancia el optimizador.
func NewOptimizer(p Predictor) *Optimizer {
	return &Optimizer{
		predictor: p,
		pasoTemp:  2.0,  // °C
		pasoVel:   0.02, // m/s
	}
}

// Resolver es el punto de entrada principal. Devuelve la mejor propuesta
// (o un error si no hay nada razonable que proponer).
func (o *Optimizer) Resolver(
	ctx context.Context,
	productoID string,
	estado Estado,
	lim Limites,
	pesos Pesos,
) (*Propuesta, error) {
	if !lim.consistente() {
		return nil, ErrSinLimites
	}

	// Grilla acotada al Δmáx del producto. Las variables son T1 y V; cada
	// una varía en [-Δ, +Δ] alrededor del estado actual, discretizada al
	// paso configurado.
	t1Inicio := o.grillaTemp(estado.TempObjetivo, lim.DeltaTemp1, lim.TempMin, lim.TempMax)
	vInicio := o.grillaVel(estado.VelObjetivo, lim.DeltaVel, lim.VelMin, lim.VelMax)

	// Candidatos: producto cartesiano de las grillas.
	mejor := (*Propuesta)(nil)
	var mejorZ float64
	mejorFactible := (*Propuesta)(nil)
	var mejorFactibleZ float64
	for _, t1 := range t1Inicio {
		for _, v := range vInicio {
			pred, err := o.predictor.Predict(ctx, EntradasPredictor{
				ProductoID:     productoID,
				TempHorno1:     t1,
				VelocidadCinta: v,
				TempMin:        lim.TempMin,
				TempMax:        lim.TempMax,
				VelMin:         lim.VelMin,
				VelMax:         lim.VelMax,
				TempComb1:      estado.TempComb1,
				HumedadHorno:   estado.HumedadHorno,
			})
			if err != nil {
				log.Printf("[MPC] predictor.Predict error: %v", err)
				continue
			}
			z := o.objetivo(estado, lim, pesos, pred, t1, v)
			if mejor == nil || z < mejorZ {
				mejor = &Propuesta{
					Temp1:      t1,
					Velocidad:  v,
					Z:          z,
					Prediccion: pred,
				}
				mejorZ = z
			}
			if cumpleUmbrales(pred, lim) && (mejorFactible == nil || z < mejorFactibleZ) {
				mejorFactible = &Propuesta{
					Temp1:      t1,
					Velocidad:  v,
					Z:          z,
					Prediccion: pred,
				}
				mejorFactibleZ = z
			}
		}
	}

	if mejor == nil {
		return nil, ErrSinPrediccion
	}
	if mejorFactible != nil {
		return mejorFactible, nil
	}
	mejor.RestriccionRelajada = true
	return mejor, nil
}

// cumpleUmbrales indica si la predicción respeta los umbrales de aceptación.
// Un umbral <= 0 desactiva esa restricción.
func cumpleUmbrales(pred PrediccionSalida, lim Limites) bool {
	if lim.UmbralCrudo > 0 && pred.PctCrudos > lim.UmbralCrudo {
		return false
	}
	if lim.UmbralQuem > 0 && pred.PctQuemados > lim.UmbralQuem {
		return false
	}
	return true
}

// grillaTemp devuelve los valores de T a evaluar entre [centro-Δ, centro+Δ],
// recortados al rango del producto, discretizados al paso configurado.
func (o *Optimizer) grillaTemp(centro, delta, minimo, maximo float64) (valores []float64) {
	lo := math.Max(minimo, centro-delta)
	hi := math.Min(maximo, centro+delta)
	if lo > hi {
		// Sin espacio para mover: caemos al centro (un solo candidato).
		return []float64{centro}
	}
	// Cuántos pasos entran en [lo, hi] con paso=o.pasoTemp.
	n := int(math.Floor((hi-lo)/o.pasoTemp)) + 1
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		v := lo + float64(i)*o.pasoTemp
		if v > hi {
			break
		}
		// Cuantización al paso (para que coincida con el centro si lo entra).
		v = math.Round(v/o.pasoTemp) * o.pasoTemp
		out = append(out, clampTemp(v, minimo, maximo))
	}
	// Asegurar el centro como candidato (puede haber quedado fuera por cuantización).
	if !contains(out, centro) {
		out = append(out, clampTemp(centro, minimo, maximo))
	}
	return dedupe(out)
}

// grillaVel análoga a grillaTemp para velocidad.
func (o *Optimizer) grillaVel(centro, delta, minimo, maximo float64) (valores []float64) {
	lo := math.Max(minimo, centro-delta)
	hi := math.Min(maximo, centro+delta)
	if lo > hi {
		return []float64{centro}
	}
	n := int(math.Floor((hi-lo)/o.pasoVel)) + 1
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		v := lo + float64(i)*o.pasoVel
		if v > hi {
			break
		}
		v = math.Round(v/o.pasoVel) * o.pasoVel
		out = append(out, clamp(v, minimo, maximo))
	}
	if !contains(out, centro) {
		out = append(out, clamp(centro, minimo, maximo))
	}
	return dedupe(out)
}

// objetivo calcula Z = wc·predC + wq·predQ - wo·predO + dt·|ΔT1| + dc·|ΔV|.
// Las predicciones vienen en [0, 100] (porcentajes). WO entra como recompensa
// aditiva sobre %OK (F-B4, opción A): el término -wo·predO baja Z cuando el
// candidato maximiza producto OK.
//
// Sobre la tolerancia de temperatura (F-B3): además de la penalización base
// |ΔT|·DT, si el candidato excede la tolerancia del lote se agrega una
// penalización suave max(0, |ΔT|-tol)·DT. Se usa soft (no hard-filter) para no
// vaciar el dominio de búsqueda cuando el horno está lejos del setpoint: el
// centro de la grilla siempre queda disponible y el optimizador no se queda sin
// candidatos. Con ToleranciaTempC<=0 (ej. Limites literales viejos) no se aplica
// el término extra y el comportamiento es idéntico al anterior.
func (o *Optimizer) objetivo(
	estado Estado,
	lim Limites,
	pesos Pesos,
	pred PrediccionSalida,
	t1 float64,
	v float64,
) float64 {
	z := ZCalidad(pesos, pred.PctCrudos, pred.PctOk, pred.PctQuemados)

	deltaT1 := math.Abs(t1 - estado.TempObjetivo)
	dt := deltaT1 * pesos.DT
	if lim.ToleranciaTempC > 0 {
		if exceso := deltaT1 - lim.ToleranciaTempC; exceso > 0 {
			dt += exceso * pesos.DT
		}
	}
	z += dt
	dv := math.Abs(v-estado.VelObjetivo) * pesos.DC
	z += dv
	return z
}

// PesosDesdeLote deriva los pesos desde el campo JSONB del lote. Si
// PesosObjetivo es nil o no parsea, devuelve defaults conservadores.
func PesosDesdeLote(p *lote_sector.PesosObjetivo) Pesos {
	if p == nil {
		return Pesos{WC: 1.0, WQ: 1.0, WO: 1.0, DT: 0.05, DC: 0.5}
	}
	return Pesos{WC: p.WC, WQ: p.WQ, WO: p.WO, DT: p.DT, DC: p.DC}
}

// LimitesDe deriva los límites del MPC desde el producto y el lote. El lote
// aporta los umbrales de aceptación, la tolerancia de temperatura y, cuando los
// define, los Δmáx por ciclo (override de tuning); si es nil se usan defaults
// conservadores (umbrales 5.0, tolerancia 2.0 °C) y los Δmáx del producto.
//
// Nota: la spec pedía llamarla Limites(), pero Go no permite que una función y
// el tipo Limites compartan identificador en el mismo paquete.
func LimitesDe(p *producto.Producto, l *lote_sector.Lote) Limites {
	umbralCrudo := 5.0
	umbralQuem := 5.0
	tolerancia := 2.0
	deltaTemp1 := p.DeltaTemp1MaxC
	deltaVel := p.DeltaVelMax
	if l != nil {
		umbralCrudo = l.UmbralAceptacionCrudo
		umbralQuem = l.UmbralAceptacionQuemado
		tolerancia = l.ToleranciaTempC
		if l.DeltaTemp1MaxC != nil {
			deltaTemp1 = *l.DeltaTemp1MaxC
		}
		if l.DeltaVelMax != nil {
			deltaVel = *l.DeltaVelMax
		}
	}
	lim := Limites{
		TempMin:         p.TempMin,
		TempMax:         p.TempMax,
		VelMin:          p.VelocidadCintaMin,
		VelMax:          p.VelocidadCintaMax,
		DeltaTemp1:      deltaTemp1,
		DeltaVel:        deltaVel,
		UmbralCrudo:     umbralCrudo,
		UmbralQuem:      umbralQuem,
		ToleranciaTempC: tolerancia,
	}
	return lim
}

// --- helpers ---

func (l Limites) consistente() bool {
	if l.TempMin >= l.TempMax || l.VelMin >= l.VelMax {
		return false
	}
	if l.DeltaTemp1 <= 0 || l.DeltaVel <= 0 {
		return false
	}
	return true
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampTemp(v, lo, hi float64) float64 { return clamp(v, lo, hi) }

func contains(xs []float64, v float64) bool {
	for _, x := range xs {
		if math.Abs(x-v) < 1e-9 {
			return true
		}
	}
	return false
}

func dedupe(xs []float64) []float64 {
	if len(xs) <= 1 {
		return xs
	}
	out := xs[:1]
	for _, x := range xs[1:] {
		if !contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}
