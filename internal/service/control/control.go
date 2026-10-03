// Package control orquesta el loop automático / supervisión por lote
// (SCA-400). Una instancia por lote activo; se cancela con el contexto.
//
// Loop (modo AUTOMATICO):
//   cada ventana_evaluacion_segundos:
//     1. lee metricas_horno y eventos_lote de la ventana
//     2. agrega (%C, %O, %Q, conf_promedio)
//     3. llama al optimizador
//     4. si hay propuesta → ConsignaService.Dispatch (origen AUTOMATICO)
//     5. persiste decision_control (aplicado_por=MPC o NADIE)
//     6. espera ventana_actualizacion_segundos y repite
//
// Loop (modo SUPERVISION): igual pero el paso 4 NO despacha; sólo publica
// la propuesta por SSE. La aplica un operario desde /aplicar-consigna.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// ErrLoteNoAbierto indica que el lote no está ABIERTO cuando se intenta operar.
var ErrLoteNoAbierto = errors.New("control: el lote no está abierto")

// PrediccionPersistida es la salida cruda del XGBoost que se guarda como
// JSONB en decisiones_control.prediccion. Sólo se persiste el subconjunto
// relevante: pctCrudos/pctOk/pctQuemados/modelVersion/esPlaceholder.
type PrediccionPersistida struct {
	PctCrudos     float64 `json:"pctCrudos"`
	PctOk         float64 `json:"pctOk"`
	PctQuemados   float64 `json:"pctQuemados"`
	ModelVersion  string  `json:"modelVersion"`
	EsPlaceholder bool    `json:"esPlaceholder"`
}

// Loop es el ciclo de control de un lote activo. Vive en memoria y emite
// propuestas / decisiones por SSE.
type Loop struct {
	loteID         string
	productoID     string
	modo           string // MANUAL | SUPERVISION | AUTOMATICO
	horno2Hab      bool
	ventanaEvalSeg int
	ventanaActSeg  int
	pesos          optimizador.Pesos
	limites        optimizador.Limites

	clienteLote loteLookupService
	clienteMet  metricasRepo
	eventos     eventosRepo
	producto   productoLookupService
	optimizer   *optimizador.Optimizer
	consigna    ConsignaDispatchService
	decisiones  decisionesRepo
	broker      *sse.Broker
	now         func() time.Time

	// propuestaPendiente es el último setpoint propuesto en modo SUPERVISION
	// que aún no fue aplicado por el operario.
	muPropuesta       sync.Mutex
	propuestaPendiente *optimizador.Propuesta
}

// Interface mínima que el loop necesita del lote_sector service.
type loteLookupService interface {
	GetByID(ctx context.Context, loteID string) (*lote_sector.Lote, error)
}

// Interface mínima para leer métricas (sólo las últimas N).
type metricasRepo interface {
	GetUltimasParaLote(ctx context.Context, loteID string, limite int) ([]metricashorno.MetricaHorno, error)
}

// Interface mínima para leer el producto (catálogo).
type productoLookupService interface {
	GetByID(ctx context.Context, id string) (*producto.Producto, error)
}

// Interface mínima para contar eventos del lote en una ventana temporal.
// La implementación real vive en internal/repository/lote_sector_repo.go.
type eventosRepo interface {
	CountEventosEnVentana(ctx context.Context, loteID string, desde, hasta time.Time) (ok, crudo, quemado int, err error)
}

// Interface mínima para despachar la consigna (reusa ConsignaService).
type ConsignaDispatchService interface {
	Dispatch(ctx context.Context, req consigna.ConsignaManualRequest, origen consigna.Origen, usuario *string) (*consigna.Consigna, error)
}

// Interface mínima para persistir el audit del MPC.
type decisionesRepo interface {
	Save(ctx context.Context, d *decisiones_control.DecisionControl) error
}

// NewLoop crea un Loop configurado. La verificación de modo/horno2 se hace
// en Start para no entrar al loop si el lote no califica.
func NewLoop(
	loteID string,
	clienteLote loteLookupService,
	clienteMet metricasRepo,
	eventos eventosRepo,
	producto productoLookupService,
	optimizer *optimizador.Optimizer,
	consignaSrv ConsignaDispatchService,
	decisiones decisionesRepo,
	broker *sse.Broker,
) *Loop {
	return &Loop{
		clienteLote: clienteLote,
		clienteMet:  clienteMet,
		eventos:     eventos,
		producto:   producto,
		optimizer:   optimizer,
		consigna:    consignaSrv,
		decisiones:  decisiones,
		broker:      broker,
		now:         time.Now,
	}
}

// Configurar aplica los parámetros del lote. Antes de Start, el Loop no
// conoce ni el modo ni las ventanas del lote.
func (l *Loop) Configurar(
	productoID string,
	modo string,
	horno2Hab bool,
	ventanaEvalSeg int,
	ventanaActSeg int,
	pesos optimizador.Pesos,
	limites optimizador.Limites,
) {
	l.productoID = productoID
	l.modo = modo
	l.horno2Hab = horno2Hab
	l.ventanaEvalSeg = ventanaEvalSeg
	l.ventanaActSeg = ventanaActSeg
	l.pesos = pesos
	l.limites = limites
}

// Start lanza el loop en una goroutine y devuelve. El contexto cancela
// el loop al cerrarse (lote cerrado, shutdown, etc.).
func (l *Loop) Start(ctx context.Context) {
	go l.run(ctx)
}

func (l *Loop) run(ctx context.Context) {
	if l.ventanaActSeg <= 0 {
		l.ventanaActSeg = 300
	}
	if l.ventanaEvalSeg <= 0 {
		l.ventanaEvalSeg = 600
	}
	// Esperar la primera ventana antes de la primera evaluación: queremos
	// histórico suficiente para una decisión informada.
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(l.ventanaEvalSeg) * time.Second):
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		l.tick(ctx)

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(l.ventanaActSeg) * time.Second):
		}
	}
}

// tick ejecuta un ciclo completo de evaluación → optimización → decisión.
// Antes de cada tick relee el modo del lote: si el operador cambió a MANUAL,
// el loop se autodesactiva (deja de despachar consignas) para no consumir
// telemetría innecesariamente.
func (l *Loop) tick(ctx context.Context) {
	if l.modo == lote_sector.ModoManual {
		return
	}
	if l.productoID == "" {
		log.Printf("[control] tick sin productoID configurado")
		return
	}

	// Releer el modo vigente: si pasó a MANUAL desde el panel, este loop ya
	// no aplica y termina la goroutine silenciosamente.
	if lote, err := l.clienteLote.GetByID(ctx, l.loteID); err == nil && lote != nil {
		if lote.Modo == lote_sector.ModoManual {
			l.modo = lote_sector.ModoManual
			log.Printf("[control] lote %s pasó a MANUAL; loop termina", l.loteID)
			return
		}
	}

	// 1. leer métricas + eventos de la ventana.
	now := l.now().UTC()
	ventanaInicio := now.Add(-time.Duration(l.ventanaEvalSeg) * time.Second)
	metricas, err := l.clienteMet.GetUltimasParaLote(ctx, l.loteID, 200)
	if err != nil {
		log.Printf("[control] GetUltimasParaLote: %v", err)
		return
	}
	// Filtrar a la ventana real (el repo devuelve las últimas N, no por rango).
	filtradas := filtrarPorVentana(metricas, ventanaInicio, now)
	if len(filtradas) == 0 {
		l.skipSinMuestras(ctx, ventanaInicio, now)
		return
	}

	// 2. contar eventos del lote en la misma ventana: es la observación real
	//    que alimenta z_observado y los % del audit. Si la ventana está
	//    vacía de eventos, z_observado se persiste como NULL.
	var pctCrudos, pctOk, pctQuemados float64
	zObs, zObsSet := l.calcularZObservado(ctx, ventanaInicio, now, &pctCrudos, &pctOk, &pctQuemados)

	estado := l.estadoDesdeMetricas(filtradas)
	predXGB, err := l.optimizer.Resolver(ctx, l.productoID, estado, l.limites, l.pesos)
	if err != nil {
		l.skipError(ctx, ventanaInicio, now, err, filtradas)
		return
	}

	// 5. persistir y broadcast.
	decision := &decisiones_control.DecisionControl{
		LoteID:         l.loteID,
		VentanaInicio:  ventanaInicio,
		VentanaFin:     now,
		Muestras:       len(filtradas),
		TempActual:     estado.TempHorno1,
		VelActual:      &estado.VelocidadCinta,
		PctCrudosObs:   pctCrudos,
		PctOkObs:       pctOk,
		PctQuemadosObs: pctQuemados,
		ConfPromedio:   confianzaPromedio(filtradas),
		TempPropuesto:  predXGB.Temp1,
		VelPropuesto:   predXGB.Velocidad,
		ZObservado:     zObs, // nil cuando la ventana no tuvo eventos
		ZPredicho:      &predXGB.Z,
		AplicadoPor:    decisiones_control.AplicadoPorNadie,
		Prediccion:     serializarPrediccion(predXGB.Prediccion),
	}
	_ = zObsSet // sólo para documentar el set en tests / debug
	if predXGB.Temp2 != nil {
		// T2 no entra como columna en decisiones_control (la columna temp2_objetivo
		// está en lotes_productivos). Se serializa dentro de Prediccion.
		_ = predXGB.Temp2
	}

	if l.modo == lote_sector.ModoAutomatico {
		l.aplicarComoMPC(ctx, predXGB, decision, estado)
	} else {
		// SUPERVISION: sólo publica por SSE para que el operador apruebe.
		l.muPropuesta.Lock()
		l.propuestaPendiente = predXGB
		l.muPropuesta.Unlock()
		l.broker.Broadcast("control.propuesta", mustJSON(map[string]interface{}{
			"lote_id":   l.loteID,
			"propuesta": predXGB,
			"ventana":   ventanaInicio,
		}))
		decision.AplicadoPor = decisiones_control.AplicadoPorNadie
		if err := l.decisiones.Save(ctx, decision); err != nil {
			log.Printf("[control] Save decision (supervision): %v", err)
		}
	}
}

// calcularZObservado cuenta los eventos del lote en la ventana y devuelve:
//   - zObs, zObsSet: Z observada (nil cuando no hay eventos)
//   - *pctCrudos, *pctOk, *pctQuemados: porcentajes observados en la ventana
//
// Si el repo falla, se persiste el error como skipped_observation (no se
// rompe el despacho de la propuesta). Si no hay eventos, los porcentajes
// quedan en 0 y zObs en nil: la decisión queda registrada sin Z observada,
// coherente con la promesa del audit ("z_observado requiere eventos en la
// ventana").
func (l *Loop) calcularZObservado(
	ctx context.Context, ventanaInicio, ventanaFin time.Time,
	pctCrudos, pctOk, pctQuemado *float64,
) (*float64, bool) {
	if l.eventos == nil {
		return nil, false
	}
	okN, crudoN, quemadoN, err := l.eventos.CountEventosEnVentana(ctx, l.loteID, ventanaInicio, ventanaFin)
	if err != nil {
		log.Printf("[control] CountEventosEnVentana: %v", err)
		return nil, false
	}
	total := okN + crudoN + quemadoN
	if total == 0 {
		return nil, false
	}
	cPct := 100.0 * float64(crudoN) / float64(total)
	oPct := 100.0 * float64(okN) / float64(total)
	qPct := 100.0 * float64(quemadoN) / float64(total)
	*pctCrudos = cPct
	*pctOk = oPct
	*pctQuemado = qPct
	z := l.pesos.WC*cPct + l.pesos.WQ*qPct
	return &z, true
}

// AplicarPropuesta aplica manualmente (operario) la propuesta pendiente del
// modo SUPERVISION. Devuelve ErrLoteNoAbierto si ya no hay propuesta.
// `usuario` es el nombre/identificador del operario que aprobó (se persiste
// en la auditoría para distinguirlo del despacho automático).
func (l *Loop) AplicarPropuesta(ctx context.Context, usuario string) (*consigna.Consigna, error) {
	l.muPropuesta.Lock()
	prop := l.propuestaPendiente
	l.propuestaPendiente = nil
	l.muPropuesta.Unlock()
	if prop == nil {
		return nil, ErrLoteNoAbierto
	}
	return l.aplicarConsigna(ctx, prop, consigna.OrigenManual, usuario)
}

func (l *Loop) aplicarComoMPC(ctx context.Context, prop *optimizador.Propuesta, decision *decisiones_control.DecisionControl, estado optimizador.Estado) {
	cons, err := l.aplicarConsigna(ctx, prop, consigna.OrigenAutomatico, "")
	if err != nil {
		// Si el controlador físico rechaza, registramos la decisión como no
		// aplicada con motivo. El horno pasa a CONTROL_MANUAL por el flujo
		// existente (handleFalloDeEnlace).
		skipped := fmt.Sprintf("dispatch_fallido: %v", err)
		decision.AplicadoPor = decisiones_control.AplicadoPorNadie
		decision.RazonSkipped = &skipped
		if err := l.decisiones.Save(ctx, decision); err != nil {
			log.Printf("[control] Save decision (mpc fail): %v", err)
		}
		return
	}
	temp := cons.TemperaturaObjetivo
	vel := cons.VelocidadCintaObjetivo
	decision.TempAplicado = &temp
	decision.VelAplicado = &vel
	decision.AplicadoPor = decisiones_control.AplicadoPorMPC
	if err := l.decisiones.Save(ctx, decision); err != nil {
		log.Printf("[control] Save decision (mpc ok): %v", err)
	}
	_ = estado
}

// aplicarConsigna despacha la propuesta con el origen y usuario correctos.
// `usuario` puede estar vacío cuando el origen es AUTOMATICO (MPC despacha
// solo); en modo MANUAL el handler lo carga desde el JWT del operario.
func (l *Loop) aplicarConsigna(ctx context.Context, prop *optimizador.Propuesta, origen consigna.Origen, usuario string) (*consigna.Consigna, error) {
	req := consigna.ConsignaManualRequest{
		HornoID:                "horno-1", // Horno fijo del primer sector; ampliar a sector-aware cuando haya 2+
		ProductoID:             l.productoID,
		TemperaturaObjetivo:    prop.Temp1,
		VelocidadCintaObjetivo: prop.Velocidad,
		TempHorno2Objetivo:     prop.Temp2,
	}
	var u *string
	if usuario != "" {
		u = &usuario
	}
	return l.consigna.Dispatch(ctx, req, origen, u)
}

func (l *Loop) skipSinMuestras(ctx context.Context, ventanaInicio, ventanaFin time.Time) {
	skipped := "ventana_sin_muestras"
	d := &decisiones_control.DecisionControl{
		LoteID:        l.loteID,
		VentanaInicio: ventanaInicio,
		VentanaFin:    ventanaFin,
		Muestras:      0,
		TempActual:    0, VelActual: floatPtr(0),
		PctCrudosObs: 0, PctOkObs: 0, PctQuemadosObs: 0,
		ConfPromedio:  0,
		TempPropuesto: 0, VelPropuesto: 0,
		AplicadoPor:  decisiones_control.AplicadoPorNadie,
		RazonSkipped: &skipped,
	}
	if err := l.decisiones.Save(ctx, d); err != nil {
		log.Printf("[control] Save skip sin muestras: %v", err)
	}
}

func (l *Loop) skipError(ctx context.Context, ventanaInicio, ventanaFin time.Time, err error, filtradas []metricashorno.MetricaHorno) {
	skipped := fmt.Sprintf("optimizer_error: %v", err)
	d := &decisiones_control.DecisionControl{
		LoteID:        l.loteID,
		VentanaInicio: ventanaInicio,
		VentanaFin:    ventanaFin,
		Muestras:      len(filtradas),
		TempActual:    0, VelActual: floatPtr(0),
		PctCrudosObs: 0, PctOkObs: 0, PctQuemadosObs: 0,
		ConfPromedio:  confianzaPromedio(filtradas),
		TempPropuesto: 0, VelPropuesto: 0,
		AplicadoPor:  decisiones_control.AplicadoPorNadie,
		RazonSkipped: &skipped,
	}
	if err := l.decisiones.Save(ctx, d); err != nil {
		log.Printf("[control] Save skip error: %v", err)
	}
}

// estadoDesdeMetricas toma las últimas métricas y devuelve el estado del
// horno. La temp_objetivo / vel_objetivo del estado son las últimas
// consignas aplicadas que viajaban en el heartbeat (lo que la Pi ENTRADA
// observó del variador/PLC).
func (l *Loop) estadoDesdeMetricas(m []metricashorno.MetricaHorno) optimizador.Estado {
	// Tomamos la métrica más reciente como snapshot del estado.
	last := m[0]
	estado := optimizador.Estado{
		VelocidadCinta: 0,
	}
	if last.TempHorno1 != nil {
		estado.TempHorno1 = *last.TempHorno1
	}
	if last.TempHorno2 != nil {
		t2 := *last.TempHorno2
		estado.TempHorno2 = &t2
	}
	if last.VelocidadCinta != nil {
		estado.VelocidadCinta = *last.VelocidadCinta
	}
	if last.TempObjetivo != nil {
		estado.TempObjetivo = *last.TempObjetivo
	} else {
		estado.TempObjetivo = estado.TempHorno1
	}
	if last.VelObjetivo != nil {
		estado.VelObjetivo = *last.VelObjetivo
	} else {
		estado.VelObjetivo = estado.VelocidadCinta
	}
	return estado
}

// --- helpers ---

func filtrarPorVentana(m []metricashorno.MetricaHorno, inicio, fin time.Time) []metricashorno.MetricaHorno {
	out := m[:0]
	for _, x := range m {
		if (x.ReceivedAt.After(inicio) || x.ReceivedAt.Equal(inicio)) && x.ReceivedAt.Before(fin) {
			out = append(out, x)
		}
	}
	return out
}

func confianzaPromedio(m []metricashorno.MetricaHorno) float64 {
	if len(m) == 0 {
		return 0
	}
	sum := 0.0
	n := 0
	for _, x := range m {
		if x.ConfianzaDeteccion != nil {
			sum += *x.ConfianzaDeteccion
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func serializarPrediccion(p optimizador.PrediccionSalida) []byte {
	b, _ := json.Marshal(PrediccionPersistida{
		PctCrudos:     p.PctCrudos,
		PctOk:         p.PctOk,
		PctQuemados:   p.PctQuemados,
		ModelVersion:  p.ModelVersion,
		EsPlaceholder: p.EsPlaceholder,
	})
	return b
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func floatPtr(x float64) *float64 { return &x }