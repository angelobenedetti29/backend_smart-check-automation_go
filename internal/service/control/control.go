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
	hornoID        string // horno físico destino del despacho (MPC_HORNO_ID, default "horno-1")
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
// conoce ni el modo ni las ventanas del lote. `hornoID` es el horno físico
// destino del despacho (provisto por la config del MPC, no por el lote).
func (loop *Loop) Configurar(
	productoID string,
	modo string,
	horno2Hab bool,
	hornoID string,
	ventanaEvalSeg int,
	ventanaActSeg int,
	pesos optimizador.Pesos,
	limites optimizador.Limites,
) {
	loop.productoID = productoID
	loop.modo = modo
	loop.horno2Hab = horno2Hab
	loop.hornoID = hornoID
	loop.ventanaEvalSeg = ventanaEvalSeg
	loop.ventanaActSeg = ventanaActSeg
	loop.pesos = pesos
	loop.limites = limites
}

// Start lanza el loop en una goroutine y devuelve. El contexto cancela
// el loop al cerrarse (lote cerrado, shutdown, etc.).
func (loop *Loop) Start(ctx context.Context) {
	go loop.run(ctx)
}

func (loop *Loop) run(ctx context.Context) {
	if loop.ventanaActSeg <= 0 {
		loop.ventanaActSeg = 300
	}
	if loop.ventanaEvalSeg <= 0 {
		loop.ventanaEvalSeg = 600
	}
	// Esperar la primera ventana antes de la primera evaluación: queremos
	// histórico suficiente para una decisión informada. Se interrumpe si
	// cambian el modo a MANUAL (ctx no se cancela en ese caso: lo relee
	// `tick` al inicio del bucle).
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(loop.ventanaEvalSeg) * time.Second):
	}

	for {
		// Chequeo temprano: si el lote ya pasó a MANUAL antes de este tick,
		// salimos sin gastar el sleep entero. El próximo operador que
		// reanude el control debe llamar a /control/start de nuevo.
		if loop.modo == lote_sector.ModoManual {
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		loop.tick(ctx)

		if loop.modo == lote_sector.ModoManual {
			// El tick ya detectó el cambio a MANUAL y abortó; salimos sin
			// dormir más para liberar la goroutine inmediatamente.
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(loop.ventanaActSeg) * time.Second):
		}
	}
}

// tick ejecuta un ciclo completo de evaluación → optimización → decisión.
// Antes de cada tick relee el modo del lote: si el operador cambió a MANUAL,
// el loop se autodesactiva (deja de despachar consignas) para no consumir
// telemetría innecesariamente.
func (loop *Loop) tick(ctx context.Context) {
	if loop.modo == lote_sector.ModoManual {
		return
	}
	if loop.productoID == "" {
		log.Printf("[control] tick sin productoID configurado")
		return
	}

	// Releer el modo vigente: si pasó a MANUAL desde el panel, este loop ya
	// no aplica y termina la goroutine silenciosamente.
	if lote, err := loop.clienteLote.GetByID(ctx, loop.loteID); err == nil && lote != nil {
		if lote.Modo == lote_sector.ModoManual {
			loop.modo = lote_sector.ModoManual
			log.Printf("[control] lote %s pasó a MANUAL; loop termina", loop.loteID)
			return
		}
	}

	// 1. leer métricas + eventos de la ventana.
	now := loop.now().UTC()
	ventanaInicio := now.Add(-time.Duration(loop.ventanaEvalSeg) * time.Second)
	metricas, err := loop.clienteMet.GetUltimasParaLote(ctx, loop.loteID, 200)
	if err != nil {
		log.Printf("[control] GetUltimasParaLote: %v", err)
		return
	}
	// Filtrar a la ventana real (el repo devuelve las últimas N, no por rango).
	filtradas := filtrarPorVentana(metricas, ventanaInicio, now)
	if len(filtradas) == 0 {
		loop.skipSinMuestras(ctx, ventanaInicio, now)
		return
	}

	// 2. contar eventos del lote en la misma ventana: es la observación real
	//    que alimenta z_observado y los % del audit. Si la ventana está
	//    vacía de eventos, z_observado se persiste como NULL.
	var pctCrudos, pctOk, pctQuemados float64
	zObs, zObsSet := loop.calcularZObservado(ctx, ventanaInicio, now, &pctCrudos, &pctOk, &pctQuemados)

	estado := loop.estadoDesdeMetricas(filtradas)
	predXGB, err := loop.optimizer.Resolver(ctx, loop.productoID, estado, loop.limites, loop.pesos)
	if err != nil {
		loop.skipError(ctx, ventanaInicio, now, err, filtradas)
		return
	}

	// 5. persistir y broadcast.
	decision := &decisiones_control.DecisionControl{
		LoteID:         loop.loteID,
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

	if loop.modo == lote_sector.ModoAutomatico {
		loop.aplicarComoMPC(ctx, predXGB, decision, estado)
	} else {
		// SUPERVISION: sólo publica por SSE para que el operador apruebe.
		loop.muPropuesta.Lock()
		loop.propuestaPendiente = predXGB
		loop.muPropuesta.Unlock()
		loop.broker.Broadcast("control.propuesta", mustJSON(map[string]interface{}{
			"lote_id":   loop.loteID,
			"propuesta": predXGB,
			"ventana":   ventanaInicio,
		}))
		decision.AplicadoPor = decisiones_control.AplicadoPorNadie
		if err := loop.decisiones.Save(ctx, decision); err != nil {
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
func (loop *Loop) calcularZObservado(
	ctx context.Context, ventanaInicio, ventanaFin time.Time,
	pctCrudos, pctOk, pctQuemado *float64,
) (*float64, bool) {
	if loop.eventos == nil {
		return nil, false
	}
	okN, crudoN, quemadoN, err := loop.eventos.CountEventosEnVentana(ctx, loop.loteID, ventanaInicio, ventanaFin)
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
	z := loop.pesos.WC*cPct + loop.pesos.WQ*qPct
	return &z, true
}

// AplicarPropuesta aplica manualmente (operario) la propuesta pendiente del
// modo SUPERVISION. Devuelve ErrLoteNoAbierto si ya no hay propuesta.
// `usuario` es el nombre/identificador del operario que aprobó (se persiste
// en la auditoría para distinguirlo del despacho automático).
func (loop *Loop) AplicarPropuesta(ctx context.Context, usuario string) (*consigna.Consigna, error) {
	loop.muPropuesta.Lock()
	prop := loop.propuestaPendiente
	loop.propuestaPendiente = nil
	loop.muPropuesta.Unlock()
	if prop == nil {
		return nil, ErrLoteNoAbierto
	}
	return loop.aplicarConsigna(ctx, prop, consigna.OrigenManual, usuario)
}

func (loop *Loop) aplicarComoMPC(ctx context.Context, prop *optimizador.Propuesta, decision *decisiones_control.DecisionControl, estado optimizador.Estado) {
	cons, err := loop.aplicarConsigna(ctx, prop, consigna.OrigenAutomatico, "")
	if err != nil {
		// Si el controlador físico rechaza, registramos la decisión como no
		// aplicada con motivo. El horno pasa a CONTROL_MANUAL por el flujo
		// existente (handleFalloDeEnlace).
		skipped := fmt.Sprintf("dispatch_fallido: %v", err)
		decision.AplicadoPor = decisiones_control.AplicadoPorNadie
		decision.RazonSkipped = &skipped
		if err := loop.decisiones.Save(ctx, decision); err != nil {
			log.Printf("[control] Save decision (mpc fail): %v", err)
		}
		return
	}
	temp := cons.TemperaturaObjetivo
	vel := cons.VelocidadCintaObjetivo
	decision.TempAplicado = &temp
	decision.VelAplicado = &vel
	decision.AplicadoPor = decisiones_control.AplicadoPorMPC
	if err := loop.decisiones.Save(ctx, decision); err != nil {
		log.Printf("[control] Save decision (mpc ok): %v", err)
	}
	_ = estado
}

// aplicarConsigna despacha la propuesta con el origen y usuario correctos.
// `usuario` puede estar vacío cuando el origen es AUTOMATICO (MPC despacha
// solo); en modo MANUAL el handler lo carga desde el JWT del operario.
//
// HornoID proviene de la env MPC_HORNO_ID (default "horno-1") y representa
// el horno físico destino del despacho. Si el lote tiene horno2_habilitado
// pero el horno configurado no lo soporta físicamente, se loggea warning y se
// despacha sólo T1 (el horno receptor rechazará silenciosamente T2 si no
// corresponde).
func (loop *Loop) aplicarConsigna(ctx context.Context, prop *optimizador.Propuesta, origen consigna.Origen, usuario string) (*consigna.Consigna, error) {
	hornoID := loop.hornoID
	if hornoID == "" {
		hornoID = "horno-1"
	}
	if loop.horno2Hab && prop.Temp2 != nil {
		// Defensa preventiva: el horno configurado podría no tener T2 físico.
		// El log ayuda a diagnosticar; el backend rechazará el setpoint si
		// el horno no lo tiene cargado (ConsignaService.ErrHorno2NoHabilitado).
		log.Printf("[control] despacho T2 a horno %s (producto %s); verificar soporte físico", hornoID, loop.productoID)
	}
	req := consigna.ConsignaManualRequest{
		HornoID:                hornoID,
		ProductoID:             loop.productoID,
		TemperaturaObjetivo:    prop.Temp1,
		VelocidadCintaObjetivo: prop.Velocidad,
		TempHorno2Objetivo:     prop.Temp2,
	}
	var u *string
	if usuario != "" {
		u = &usuario
	}
	return loop.consigna.Dispatch(ctx, req, origen, u)
}

func (loop *Loop) skipSinMuestras(ctx context.Context, ventanaInicio, ventanaFin time.Time) {
	skipped := "ventana_sin_muestras"
	d := &decisiones_control.DecisionControl{
		LoteID:        loop.loteID,
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
	if err := loop.decisiones.Save(ctx, d); err != nil {
		log.Printf("[control] Save skip sin muestras: %v", err)
	}
}

func (loop *Loop) skipError(ctx context.Context, ventanaInicio, ventanaFin time.Time, err error, filtradas []metricashorno.MetricaHorno) {
	skipped := fmt.Sprintf("optimizer_error: %v", err)
	d := &decisiones_control.DecisionControl{
		LoteID:        loop.loteID,
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
	if err := loop.decisiones.Save(ctx, d); err != nil {
		log.Printf("[control] Save skip error: %v", err)
	}
}

// estadoDesdeMetricas toma las últimas métricas y devuelve el estado del
// horno. La temp_objetivo / vel_objetivo del estado son las últimas
// consignas aplicadas que viajaban en el heartbeat (lo que la Pi ENTRADA
// observó del variador/PLC).
func (loop *Loop) estadoDesdeMetricas(m []metricashorno.MetricaHorno) optimizador.Estado {
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