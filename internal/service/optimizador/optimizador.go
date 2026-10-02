// Package optimizador implementa el MPC (Model Predictive Control) que
// propone nuevos setpoints del horno (SCA-400).
//
// Horizonte: 1 paso (estático). No usamos solver convexo (gonum): la
// función de respuesta (XGBoost) es no-convexa, así que el beneficio de
// un LP/QP es marginal. En su lugar, muestreamos una grilla fina dentro
// de los Δmáx por ciclo alrededor del estado actual, evaluamos el
// predictor XGBoost en cada candidato, computamos el objetivo y
// elegimos el mínimo respetando las restricciones. Con Δtemp1=10 °C y
// paso=2 °C son 11 valores de T1; idem T2 y V → ≤ 1331 candidatos por
// decisión. Con XGBoost a <1 ms por eval, la decisión tarda <1 s.
//
// Branching por horno2_habilitado:
//   - true  → 3 variables (T1, T2, V).
//   - false → 2 variables (T1, V).
package optimizador

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
type EntradasPredictor struct {
	ProductoID     string
	TempHorno1     float64
	TempHorno2     *float64
	VelocidadCinta float64
	TempComb1      *float64
	TempComb2      *float64
	HumedadHorno   *float64
}

// PrediccionSalida es la triplete que devuelve el XGBoost (o el placeholder).
type PrediccionSalida struct {
	PctCrudos    float64 `json:"pctCrudos"`
	PctOk        float64 `json:"pctOk"`
	PctQuemados  float64 `json:"pctQuemados"`
	ModelVersion string  `json:"modelVersion"`
	EsPlaceholder bool  `json:"esPlaceholder"`
}

// Pesos son los coeficientes de la función objetivo. WC = peso de crudos,
// WQ = peso de quemados, WO = peso de OK (no se usa directamente en la
// versión actual del spec; conservado para futuro), DT = penalización por
// ΔT por °C, DC = penalización por ΔV por m/s.
type Pesos struct {
	WC float64
	WQ float64
	WO float64
	DT float64
	DC float64
}

// Estado es la foto del horno al momento de optimizar.
type Estado struct {
	TempHorno1    float64
	TempHorno2    *float64 // nil cuando la línea tiene un solo horno
	VelocidadCinta float64
	TempObjetivo  float64 // setpoint activo de T1 (último aplicado)
	VelObjetivo   float64 // setpoint activo de V (último aplicado)
}

// Limites del producto: rangos y Δmáx por ciclo.
type Limites struct {
	TempMin      float64
	TempMax      float64
	Temp2Min     *float64
	Temp2Max     *float64
	VelMin       float64
	VelMax       float64
	DeltaTemp1   float64 // |ΔT1| máximo por ciclo
	DeltaTemp2   *float64 // |ΔT2| máximo por ciclo (nil si no hay horno 2)
	DeltaVel     float64
	Horno2Hab    bool
	UmbralCrudo  float64 // % crudo aceptable
	UmbralQuem   float64 // % quemado aceptable
}

// Propuesta es el resultado del optimizador: los nuevos setpoints y los
// valores predichos. Aplicado queda en false hasta que el caller
// confirme el despacho.
type Propuesta struct {
	Temp1          float64
	Temp2          *float64
	Velocidad      float64
	Z              float64
	Prediccion     PrediccionSalida
	VentanaInicio  string `json:"ventana_inicio,omitempty"`
	VentanaFin     string `json:"ventana_fin,omitempty"`
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
		pasoTemp:  2.0, // °C
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

	// Grilla acotada al Δmáx del producto. Las variables son T1, T2 (si
	// horno2), V; cada una varía en [-Δ, +Δ] alrededor del estado actual,
	// discretizada al paso configurado.
	t1Inicio := o.grillaTemp(estado.TempObjetivo, lim.DeltaTemp1, lim.TempMin, lim.TempMax)
	vInicio := o.grillaVel(estado.VelObjetivo, lim.DeltaVel, lim.VelMin, lim.VelMax)

	var t2Inicio []float64
	t2Habilitado := lim.Horno2Hab && lim.Temp2Min != nil && lim.Temp2Max != nil && lim.DeltaTemp2 != nil
	if t2Habilitado {
		base := estado.TempObjetivo
		if estado.TempHorno2 != nil {
			base = *estado.TempHorno2
		}
		t2Inicio = o.grillaTemp(base, *lim.DeltaTemp2, *lim.Temp2Min, *lim.Temp2Max)
	}

	// Candidatos: producto cartesiano de las grillas.
	mejor := (*Propuesta)(nil)
	var mejorZ float64
	for _, t1 := range t1Inicio {
		for _, v := range vInicio {
if t2Habilitado {
			for _, t2 := range t2Inicio {
					pred, err := o.predictor.Predict(ctx, EntradasPredictor{
						ProductoID:     productoID,
						TempHorno1:     t1,
						TempHorno2:     &t2,
						VelocidadCinta: v,
					})
					if err != nil {
						log.Printf("[MPC] predictor.Predict error: %v", err)
						continue
					}
					z := o.objetivo(estado, lim, pesos, pred, t1, &t2, v)
					if mejor == nil || z < mejorZ {
						p := &Propuesta{
							Temp1:      t1,
							Temp2:      &t2,
							Velocidad:  v,
							Z:          z,
							Prediccion: pred,
						}
						mejor = p
						mejorZ = z
					}
				}
			} else {
				pred, err := o.predictor.Predict(ctx, EntradasPredictor{
					ProductoID:     productoID,
					TempHorno1:     t1,
					VelocidadCinta: v,
				})
				if err != nil {
					log.Printf("[MPC] predictor.Predict error: %v", err)
					continue
				}
				z := o.objetivo(estado, lim, pesos, pred, t1, nil, v)
				if mejor == nil || z < mejorZ {
					p := &Propuesta{
						Temp1:      t1,
						Velocidad:  v,
						Z:          z,
						Prediccion: pred,
					}
					mejor = p
					mejorZ = z
				}
			}
		}
	}

	if mejor == nil {
		return nil, ErrSinPrediccion
	}
	return mejor, nil
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

// objetivo calcula Z = wc·predC + wq·predQ + dt·(|ΔT1|+|ΔT2|) + dc·|ΔV|.
// Las predicciones vienen en [0, 100] (porcentajes). El WO no entra en el
// spec actual; se conserva como peso futuro (sumar -wo·predO si se quiere).
func (o *Optimizer) objetivo(
	estado Estado,
	lim Limites,
	pesos Pesos,
	pred PrediccionSalida,
	t1 float64,
	t2 *float64,
	v float64,
) float64 {
	z := pesos.WC*pred.PctCrudos + pesos.WQ*pred.PctQuemados
	dt := math.Abs(t1-estado.TempObjetivo) * pesos.DT
	if t2 != nil && estado.TempHorno2 != nil {
		dt += math.Abs(*t2-*estado.TempHorno2) * pesos.DT
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

// LimitesDesdeProducto deriva los límites del MPC desde el producto.
func LimitesDesdeProducto(p *producto.Producto) Limites {
	lim := Limites{
		TempMin:     p.TempMin,
		TempMax:     p.TempMax,
		VelMin:      p.VelocidadCintaMin,
		VelMax:      p.VelocidadCintaMax,
		DeltaTemp1:  p.DeltaTemp1MaxC,
		DeltaVel:    p.DeltaVelMax,
		Horno2Hab:   p.Horno2Habilitado,
		UmbralCrudo: 5.0,
		UmbralQuem:  5.0,
	}
	if p.Horno2Habilitado {
		lim.Temp2Min = p.Temp2Min
		lim.Temp2Max = p.Temp2Max
		lim.DeltaTemp2 = p.DeltaTemp2MaxC
	}
	return lim
}

// PropuestaToJSON serializa la propuesta para guardarla como JSONB en
// decisiones_control.prediccion (campo crudo con la salida XGBoost).
func PropuestaToJSON(p *Propuesta) []byte {
	b, _ := json.Marshal(p)
	return b
}

// --- helpers ---

func (l Limites) consistente() bool {
	if l.TempMin >= l.TempMax || l.VelMin >= l.VelMax {
		return false
	}
	if l.DeltaTemp1 <= 0 || l.DeltaVel <= 0 {
		return false
	}
	if l.Horno2Hab {
		if l.Temp2Min == nil || l.Temp2Max == nil || l.DeltaTemp2 == nil {
			return false
		}
		if *l.Temp2Min >= *l.Temp2Max || *l.DeltaTemp2 <= 0 {
			return false
		}
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

// FormatResumen es un helper para logging/tests.
func FormatResumen(p *Propuesta) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("T1=%.2f V=%.3f Z=%.4f predC=%.2f predO=%.2f predQ=%.2f",
		p.Temp1, p.Velocidad, p.Z,
		p.Prediccion.PctCrudos, p.Prediccion.PctOk, p.Prediccion.PctQuemados,
	)
}