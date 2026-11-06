// Package control orquesta el loop automático / supervisión por lote
// (SCA-400). Una instancia por lote activo; se cancela con el contexto.
//
// Loop (modo AUTOMATICO): calienta hasta el setpoint de receta y después corre
// el MPC por ventana. Publica la propuesta ya autorizada para que la Raspberry
// Pi la haga pull; el backend nunca aplica consignas.
//
// Loop (modo SUPERVISION): publica la propuesta por SSE sin autorizarla. La
// aprueba un operario desde /aplicar-consigna y la Pi la aplica.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// ErrLoteNoAbierto indica que el lote no está ABIERTO cuando se intenta operar.
var ErrLoteNoAbierto = errors.New("control: el lote no está abierto")

// Fases del control automático: CALENTANDO espera que el horno alcance la
// temperatura de receta; ADAPTANDO corre el MPC normal.
const (
	FaseCalentando = "CALENTANDO"
	FaseAdaptando  = "ADAPTANDO"
)

// Loop es el ciclo de control de un lote activo. Vive en memoria y emite
// propuestas / decisiones por SSE.
type Loop struct {
	loteID         string
	productoID     string
	modo           string // MANUAL | SUPERVISION | AUTOMATICO
	sectorID       string // id del sector/horno del lote
	ventanaEvalSeg int
	ventanaActSeg  int
	pesos          optimizador.Pesos
	limites        optimizador.Limites

	clienteLote loteLookupService
	clienteMet  metricasRepo
	eventos     eventosRepo
	producto    productoLookupService
	optimizer   *optimizador.Optimizer
	decisiones  decisionesRepo
	broker      *sse.Broker
	now         func() time.Time
	alertas     alerta.Repository

	// shouldExit es la señal de salida natural (lote cerrado, etc.). Sólo la
	// toca la goroutine de run/tick, no necesita mutex.
	shouldExit bool

	// propuestaPendiente es el último setpoint propuesto que aún no fue
	// aplicado. Los metadatos de auditoría de la última decisión persistida
	// (id, ventana y timestamp) se guardan junto a ella para poder exponer un
	// snapshot no-destructivo por HTTP.
	muPropuesta        sync.Mutex
	propuestaPendiente *optimizador.Propuesta
	lastDecisionID     string
	lastVentanaInicio  time.Time
	lastVentanaFin     time.Time
	lastCreatedAt      time.Time
	propuestaAprobada  bool

	// fase y muestrasEnTolerancia sólo los toca la goroutine del loop.
	fase                 string // CALENTANDO | ADAPTANDO ("" hasta que run inicializa)
	faseInicial          string // fase forzada por el caller (reanudación); "" = derivarla del modo
	muestrasEnTolerancia int
	preheatInicio        time.Time
	preheatTimeoutSeg    int
	// alertaQuemadosActiva y alertaCrudosActiva recuerdan si ya se emitió una
	// alerta de calidad en el período continuo actual. Sólo las toca la
	// goroutine del loop.
	alertaQuemadosActiva bool
	alertaCrudosActiva   bool
	// alertaTemperaturaActiva recuerda si ya se emitió la alerta de T1 fuera de
	// objetivo. Sólo la toca la goroutine del loop.
	alertaTemperaturaActiva bool
	// sinMetricas evita esperar una ventana completa cuando el lote recién
	// arranca y la Pi todavía no publicó telemetría.
	sinMetricas    bool
	confianzaMin   float64 // umbral de confianza de detección; 0 = desconocida
	preheatPollSeg int
	// permitirPlaceholder habilita AUTOMATICO sin modelo entrenado. Default
	// false: el placeholder no debe mover el horno en producción. Se usa sólo
	// para demos/tesis con MPC_PERMITIR_PLACEHOLDER=true.
	permitirPlaceholder bool
}

// PropuestaSnapshot es una vista no-destructiva de la propuesta pendiente de
// SUPERVISION, apta para serializar en el endpoint consigna-propuesta.
type PropuestaSnapshot struct {
	DecisionID    string
	Temp1         float64
	Velocidad     float64
	VentanaInicio time.Time
	VentanaFin    time.Time
	CreatedAt     time.Time
	TTLSegundos   int
	Autorizada    bool
	Fase          string
}

// PropuestaPendiente devuelve un snapshot no-destructivo de la propuesta
// pendiente bajo mutex. Devuelve nil si no hay propuesta. NO modifica el
// estado del loop.
func (loop *Loop) PropuestaPendiente(ttlSegundos int) *PropuestaSnapshot {
	loop.muPropuesta.Lock()
	defer loop.muPropuesta.Unlock()
	if loop.propuestaPendiente == nil {
		return nil
	}
	p := loop.propuestaPendiente
	return &PropuestaSnapshot{
		DecisionID:    loop.lastDecisionID,
		Temp1:         p.Temp1,
		Velocidad:     p.Velocidad,
		VentanaInicio: loop.lastVentanaInicio,
		VentanaFin:    loop.lastVentanaFin,
		CreatedAt:     loop.lastCreatedAt,
		TTLSegundos:   ttlSegundos,
		Autorizada:    loop.propuestaAprobada,
		Fase:          loop.fase,
	}
}

// SetConfianzaMin ajusta el umbral de confianza de detección. Valores <= 0
// conservan el default (0.5): 0 significa "desconocida" y no debe gatear.
func (loop *Loop) SetConfianzaMin(v float64) {
	if v <= 0 {
		return
	}
	loop.confianzaMin = v
}

// SetPermitirPlaceholder define si AUTOMATICO puede operar con el predictor
// placeholder (sin modelo entrenado). Default false.
func (loop *Loop) SetPermitirPlaceholder(permitir bool) {
	loop.permitirPlaceholder = permitir
}

// SetAlertas inyecta el repositorio de alertas. Es nil-safe: sin repo, el loop
// omite la generación de alertas.
func (loop *Loop) SetAlertas(a alerta.Repository) {
	loop.alertas = a
}

// SetPreheatTimeout ajusta el máximo de espera de precalentamiento. Un valor
// <= 0 conserva el default (1800 s).
func (loop *Loop) SetPreheatTimeout(segundos int) {
	if segundos > 0 {
		loop.preheatTimeoutSeg = segundos
	}
}

// SetPreheatPoll ajusta cada cuántos segundos el loop reintenta mientras está
// en calentamiento o esperando la primera telemetría. Un valor <= 0 conserva el
// default (15 s). Bajarlo sólo tiene sentido en demos/E2E con ventanas cortas.
func (loop *Loop) SetPreheatPoll(segundos int) {
	if segundos > 0 {
		loop.preheatPollSeg = segundos
	}
}

// SetFaseInicial fuerza la fase con la que arranca run. Sólo acepta CALENTANDO
// o ADAPTANDO; cualquier otro valor se ignora. Debe llamarse antes de Start.
func (loop *Loop) SetFaseInicial(fase string) {
	if fase != FaseCalentando && fase != FaseAdaptando {
		return
	}
	loop.faseInicial = fase
}

// FaseInicial devuelve la fase forzada al arrancar ("" = derivada del modo).
func (loop *Loop) FaseInicial() string { return loop.faseInicial }

// AprobarPropuesta marca como aprobada la propuesta pendiente indicada.
func (loop *Loop) AprobarPropuesta(decisionID string) bool {
	loop.muPropuesta.Lock()
	defer loop.muPropuesta.Unlock()
	if decisionID == "" || loop.lastDecisionID != decisionID {
		return false
	}
	loop.propuestaAprobada = true
	return true
}

// ClearPropuesta descarta la propuesta pendiente indicada.
func (loop *Loop) ClearPropuesta(decisionID string) {
	loop.muPropuesta.Lock()
	defer loop.muPropuesta.Unlock()
	if decisionID == "" || loop.lastDecisionID != decisionID {
		return
	}
	loop.propuestaPendiente = nil
	loop.lastDecisionID = ""
	loop.propuestaAprobada = false
}

// publicarPropuesta guarda el snapshot y sus metadatos bajo mutex y emite el
// evento SSE de propuesta.
func (loop *Loop) publicarPropuesta(prop *optimizador.Propuesta, decision *decisiones_control.DecisionControl, autorizada bool) {
	loop.muPropuesta.Lock()
	loop.propuestaPendiente = prop
	loop.lastDecisionID = decision.ID
	loop.lastVentanaInicio = decision.VentanaInicio
	loop.lastVentanaFin = decision.VentanaFin
	loop.lastCreatedAt = decision.CreatedAt
	loop.propuestaAprobada = autorizada
	loop.muPropuesta.Unlock()
	loop.broker.Broadcast("control.propuesta", mustJSON(map[string]interface{}{
		"lote_id":     loop.loteID,
		"propuesta":   prop,
		"decision_id": decision.ID,
		"autorizada":  autorizada,
		"ventana":     decision.VentanaInicio,
	}))
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

// Interface mínima para contar eventos del lote en una ventana temporal y
// agregar la confianza de detección reportada por la Pi SALIDA.
// La implementación real vive en internal/repository/lote_sector_repo.go.
type eventosRepo interface {
	CountEventosEnVentana(ctx context.Context, loteID string, desde, hasta time.Time) (ok, crudo, quemado int, err error)
	ConfianzaPromedioEnVentana(ctx context.Context, loteID string, desde, hasta time.Time) (promedio float64, muestras int, err error)
}

// Interface mínima para persistir el audit del MPC.
type decisionesRepo interface {
	Save(ctx context.Context, d *decisiones_control.DecisionControl) error
}

// NewLoop crea un Loop configurado. La verificación de modo se hace
// en Start para no entrar al loop si el lote no califica.
func NewLoop(
	loteID string,
	clienteLote loteLookupService,
	clienteMet metricasRepo,
	eventos eventosRepo,
	producto productoLookupService,
	optimizer *optimizador.Optimizer,
	decisiones decisionesRepo,
	broker *sse.Broker,
) *Loop {
	return &Loop{
		loteID:            loteID,
		clienteLote:       clienteLote,
		clienteMet:        clienteMet,
		eventos:           eventos,
		producto:          producto,
		optimizer:         optimizer,
		decisiones:        decisiones,
		broker:            broker,
		now:               time.Now,
		confianzaMin:      0.5,
		preheatPollSeg:    15,
		preheatTimeoutSeg: 1800,
	}
}

// Configurar aplica los parámetros del lote. Antes de Start, el Loop no
// conoce ni el modo ni las ventanas del lote. `sectorID` es el id del
// sector/horno del lote.
func (loop *Loop) Configurar(
	productoID string,
	modo string,
	sectorID string,
	ventanaEvalSeg int,
	ventanaActSeg int,
	pesos optimizador.Pesos,
	limites optimizador.Limites,
) {
	loop.productoID = productoID
	loop.modo = modo
	loop.sectorID = sectorID
	loop.ventanaEvalSeg = ventanaEvalSeg
	loop.ventanaActSeg = ventanaActSeg
	loop.pesos = pesos
	loop.limites = limites
}

// Start lanza el loop en una goroutine y devuelve. Cuando run sale (por
// cambio de modo, lote cerrado, ctx cancelado, etc.), llama a cancel() para
// que el LoopRegistry limpie la entrada del lote.
func (loop *Loop) Start(ctx context.Context, cancel context.CancelFunc) {
	go func() {
		defer cancel()
		loop.run(ctx)
	}()
}

func (loop *Loop) run(ctx context.Context) {
	if loop.ventanaActSeg <= 0 {
		loop.ventanaActSeg = 300
	}
	if loop.ventanaEvalSeg <= 0 {
		loop.ventanaEvalSeg = 600
	}
	if loop.preheatPollSeg <= 0 {
		loop.preheatPollSeg = 15
	}
	if loop.preheatTimeoutSeg <= 0 {
		loop.preheatTimeoutSeg = 1800
	}
	// Si un tick previo ya marcó salida (lote cerrado), salimos de inmediato.
	if loop.shouldExit {
		return
	}
	// Fase inicial: un horno automático que arranca frío calienta primero; un
	// lote enganchado manualmente (o supervisado) ya está adaptando. Una
	// reanudación tras reinicio puede forzarla vía SetFaseInicial.
	if loop.fase == "" {
		loop.fase = FaseAdaptando
		if loop.modo == lote_sector.ModoAutomatico {
			loop.fase = FaseCalentando
		}
		if loop.clienteLote != nil {
			if lote, err := loop.clienteLote.GetByID(ctx, loop.loteID); err == nil && lote != nil {
				if loop.modo == lote_sector.ModoAutomatico && !lote.IniciadoManualmente {
					loop.fase = FaseCalentando
				} else {
					loop.fase = FaseAdaptando
				}
			}
		}
		if loop.faseInicial != "" {
			loop.fase = loop.faseInicial
		}
	}
	if loop.fase == FaseCalentando && loop.preheatInicio.IsZero() {
		loop.preheatInicio = loop.now()
	}

	for {
		// Chequeo temprano: si el lote ya pasó a MANUAL o se cerró antes de
		// este tick, salimos sin gastar el sleep entero. El próximo operador
		// que reanude el control debe llamar a /control/start de nuevo.
		if loop.debeTerminar(ctx) {
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		loop.tick(ctx)

		if loop.debeTerminar(ctx) {
			// El tick detectó cierre/MANUAL y abortó; salimos sin dormir más
			// para liberar la goroutine inmediatamente.
			return
		}

		espera := loop.ventanaActSeg
		if loop.fase == FaseCalentando || loop.sinMetricas {
			espera = loop.preheatPollSeg
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(espera) * time.Second):
		}
	}
}

// debeTerminar consulta el estado del lote; devuelve true si está cerrado
// o el lote ya pasó a MANUAL desde el arranque del loop.
func (loop *Loop) debeTerminar(ctx context.Context) bool {
	if loop.shouldExit {
		return true
	}
	if loop.modo == lote_sector.ModoManual {
		return true
	}
	if loop.clienteLote == nil {
		return false
	}
	lote, err := loop.clienteLote.GetByID(ctx, loop.loteID)
	if err != nil || lote == nil {
		return false
	}
	return lote.Estado != lote_sector.EstadoAbierto || lote.Modo == lote_sector.ModoManual
}

// tick ejecuta un ciclo completo de evaluación → optimización → decisión.
// Antes de cada tick relee el modo del lote: si el operador cambió a MANUAL,
// el loop se autodesactiva (deja de despachar consignas) para no consumir
// telemetría innecesariamente.
// limitesVigentes aplica el tuning del lote sobre los límites del arranque.
// No relee el producto: los rangos de la receta quedan fijados al abrir el
// lote y el panel sólo puede mover tolerancia, umbrales y deltas. Así el
// tuning aplica en el tick siguiente sin reiniciar el loop (reiniciarlo en
// ADAPTANDO durante el precalentamiento podría arrancar la cinta antes de
// temperatura).
func (loop *Loop) limitesVigentes(lote *lote_sector.Lote) optimizador.Limites {
	lim := loop.limites
	if lote == nil {
		return lim
	}
	lim.ToleranciaTempC = lote.ToleranciaTempC
	lim.UmbralCrudo = lote.UmbralAceptacionCrudo
	lim.UmbralQuem = lote.UmbralAceptacionQuemado
	if lote.DeltaTemp1MaxC != nil {
		lim.DeltaTemp1 = *lote.DeltaTemp1MaxC
	}
	if lote.DeltaVelMax != nil {
		lim.DeltaVel = *lote.DeltaVelMax
	}
	return lim
}

func (loop *Loop) tick(ctx context.Context) {
	if loop.modo == lote_sector.ModoManual {
		return
	}
	if loop.productoID == "" {
		log.Printf("[control] tick sin productoID configurado")
		return
	}

	// Releer el modo y estado vigentes: si pasó a MANUAL desde el panel o el
	// lote se cerró, este loop ya no aplica y termina la goroutine.
	var loteActual *lote_sector.Lote
	if loop.clienteLote != nil {
		if lote, err := loop.clienteLote.GetByID(ctx, loop.loteID); err == nil && lote != nil {
			loteActual = lote
			if lote.Modo == lote_sector.ModoManual {
				loop.modo = lote_sector.ModoManual
				log.Printf("[control] lote %s pasó a MANUAL; loop termina", loop.loteID)
				return
			}
			if lote.Estado != lote_sector.EstadoAbierto {
				log.Printf("[control] lote %s estado=%s; loop termina", loop.loteID, lote.Estado)
				loop.shouldExit = true
				return
			}
			// Si el operario arrancó el horno manualmente, ya está caliente:
			// saltamos la espera de precalentamiento.
			if lote.IniciadoManualmente && loop.fase == FaseCalentando {
				loop.fase = FaseAdaptando
			}
		}
	}

	// 1. leer métricas + eventos de la ventana.
	now := loop.now().UTC()
	ventanaInicio := now.Add(-time.Duration(loop.ventanaEvalSeg) * time.Second)
	metricas, err := loop.clienteMet.GetUltimasParaLote(ctx, loop.loteID, 200)
	if err != nil {
		log.Printf("[control] GetUltimasParaLote: %v", err)
		loop.sinMetricas = true
		return
	}
	// Reintentar rápido mientras no haya telemetría (arranque manual o
	// backend recién levantado); con métricas se vuelve a la ventana normal.
	loop.sinMetricas = len(metricas) == 0

	if loop.fase == FaseCalentando {
		loop.tickPreheat(ctx, metricas, loteActual)
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
	zObs, huboEventos := loop.calcularZObservado(ctx, ventanaInicio, now, &pctCrudos, &pctOk, &pctQuemados)

	// Antes del gate de confianza y del optimizador: las alertas de calidad no
	// dependen del predictor.
	loop.evaluarCalidad(loteActual, pctCrudos, pctQuemados, huboEventos)
	loop.evaluarTemperatura(ctx, loteActual, filtradas)

	estado := loop.estadoDesdeMetricas(filtradas)
	// La confianza de detección llega con los eventos de la Pi SALIDA; la
	// telemetría del horno queda como fallback cuando la ventana no trae
	// muestras de confianza.
	conf := confianzaPromedio(filtradas)
	if loop.eventos != nil {
		prom, muestras, cerr := loop.eventos.ConfianzaPromedioEnVentana(ctx, loop.loteID, ventanaInicio, now)
		if cerr != nil {
			log.Printf("[control] ConfianzaPromedioEnVentana: %v", cerr)
		} else if muestras > 0 {
			conf = prom
		}
	}

	// Confianza desconocida (0) no gatea: la Pi todavía no la reporta.
	if conf > 0 && conf < loop.confianzaMin {
		skipped := "confianza_baja"
		d := &decisiones_control.DecisionControl{
			LoteID:         loop.loteID,
			VentanaInicio:  ventanaInicio,
			VentanaFin:     now,
			Muestras:       len(filtradas),
			TempActual:     estado.TempHorno1,
			VelActual:      &estado.VelocidadCinta,
			PctCrudosObs:   pctCrudos,
			PctOkObs:       pctOk,
			PctQuemadosObs: pctQuemados,
			ConfPromedio:   conf,
			TempPropuesto:  estado.TempObjetivo,
			VelPropuesto:   estado.VelObjetivo,
			ZObservado:     zObs,
			AplicadoPor:    decisiones_control.AplicadoPorNadie,
			RazonSkipped:   &skipped,
		}
		if err := loop.decisiones.Save(ctx, d); err != nil {
			log.Printf("[control] Save decision (confianza_baja): %v", err)
		}
		return
	}

	predXGB, err := loop.optimizer.Resolver(ctx, loop.productoID, estado, loop.limitesVigentes(loteActual), loop.pesos)
	if err != nil {
		loop.skipError(ctx, ventanaInicio, now, err, filtradas)
		return
	}

	// En AUTOMATICO no se aplica una propuesta del placeholder: primero hay que
	// entrenar el modelo. En SUPERVISION se publica igual para que el operario
	// vea el warning.
	if predXGB.Prediccion.EsPlaceholder && loop.modo == lote_sector.ModoAutomatico && !loop.permitirPlaceholder {
		skipped := "modelo_no_entrenado"
		d := &decisiones_control.DecisionControl{
			LoteID:         loop.loteID,
			VentanaInicio:  ventanaInicio,
			VentanaFin:     now,
			Muestras:       len(filtradas),
			TempActual:     estado.TempHorno1,
			VelActual:      &estado.VelocidadCinta,
			PctCrudosObs:   pctCrudos,
			PctOkObs:       pctOk,
			PctQuemadosObs: pctQuemados,
			ConfPromedio:   conf,
			TempPropuesto:  estado.TempObjetivo,
			VelPropuesto:   estado.VelObjetivo,
			ZObservado:     zObs,
			AplicadoPor:    decisiones_control.AplicadoPorNadie,
			Prediccion:     serializarPrediccion(predXGB.Prediccion, predXGB.RestriccionRelajada),
			RazonSkipped:   &skipped,
		}
		if err := loop.decisiones.Save(ctx, d); err != nil {
			log.Printf("[control] Save decision (modelo_no_entrenado): %v", err)
		}
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
		ConfPromedio:   conf,
		TempPropuesto:  predXGB.Temp1,
		VelPropuesto:   predXGB.Velocidad,
		ZObservado:     zObs, // nil cuando la ventana no tuvo eventos
		ZPredicho:      &predXGB.Z,
		AplicadoPor:    decisiones_control.AplicadoPorNadie,
		Prediccion:     serializarPrediccion(predXGB.Prediccion, predXGB.RestriccionRelajada),
	}

	decision.AplicadoPor = decisiones_control.AplicadoPorNadie
	if err := loop.decisiones.Save(ctx, decision); err != nil {
		log.Printf("[control] Save decision (modo %s): %v", loop.modo, err)
		return
	}
	loop.publicarPropuesta(predXGB, decision, loop.modo == lote_sector.ModoAutomatico)
}

// evaluarCalidad genera una alerta WARNING cuando el porcentaje observado en la
// ventana supera el umbral de aceptación del lote. El loop sólo corre para
// lotes no MANUAL. La alerta se ata al lote (HornoID vacío) para que el índice
// único (lote_id, tipo) deduplique sin bloquear el lote siguiente.
func (loop *Loop) evaluarCalidad(lote *lote_sector.Lote, pctCrudos, pctQuemados float64, huboEventos bool) {
	if loop.alertas == nil || !huboEventos {
		// Sin eventos no se puede afirmar que la condición se normalizó: no
		// rearmar las alertas por una ventana vacía.
		return
	}
	lim := loop.limitesVigentes(lote)
	loop.evaluarCalidadUmbral(alerta.TipoPorcentajeQuemadosAlto, "quemados", pctQuemados, lim.UmbralQuem, &loop.alertaQuemadosActiva)
	loop.evaluarCalidadUmbral(alerta.TipoPorcentajeCrudosAlto, "crudos", pctCrudos, lim.UmbralCrudo, &loop.alertaCrudosActiva)
}

// evaluarCalidadUmbral mantiene una sola alerta por período continuo sobre el
// umbral: al normalizarse el porcentaje se rearma y un nuevo pico vuelve a
// alertar (el repo descarta duplicados si la ACTIVA sigue existiendo).
func (loop *Loop) evaluarCalidadUmbral(tipo, etiqueta string, pct, umbral float64, activa *bool) {
	if umbral <= 0 {
		umbral = 5.0
	}
	if pct <= umbral {
		*activa = false
		return
	}
	if *activa {
		return
	}
	a := &alerta.Alerta{
		SectorID:  &loop.sectorID,
		LoteID:    &loop.loteID,
		Tipo:      tipo,
		Severidad: alerta.SeveridadWarning,
		Estado:    alerta.EstadoActiva,
		Mensaje: fmt.Sprintf("Porcentaje de %s %.1f%% supera el umbral de aceptación %.1f%% en la ventana de %ds",
			etiqueta, pct, umbral, loop.ventanaEvalSeg),
	}
	if err := loop.alertas.Save(a); err != nil {
		log.Printf("[control] Save alerta (%s) lote %s: %v", tipo, loop.loteID, err)
		return
	}
	*activa = true
}

// evaluarTemperatura genera una alerta WARNING cuando el promedio de las
// últimas 3 muestras de T1 se aleja del objetivo de receta más allá de la
// tolerancia del lote. Corre sólo en ADAPTANDO (el precalentamiento ya tiene su
// propio timeout). La alerta queda atada al lote, no al horno.
func (loop *Loop) evaluarTemperatura(ctx context.Context, lote *lote_sector.Lote, metricas []metricashorno.MetricaHorno) {
	if loop.alertas == nil {
		return
	}
	promedio, n := PromedioUltimasTemperaturas(metricas, 3)
	if n == 0 {
		return
	}
	objetivo, _, ok := loop.objetivoReceta(ctx, lote)
	if !ok {
		return
	}
	tolerancia := 2.0
	if lote != nil && lote.ToleranciaTempC > 0 {
		tolerancia = lote.ToleranciaTempC
	}
	if absFloat(promedio-objetivo) <= tolerancia {
		loop.alertaTemperaturaActiva = false
		return
	}
	if loop.alertaTemperaturaActiva {
		return
	}
	sufijo := "muestras"
	if n == 1 {
		sufijo = "muestra"
	}
	a := &alerta.Alerta{
		SectorID:  &loop.sectorID,
		LoteID:    &loop.loteID,
		Tipo:      alerta.TipoTemperaturaHorno,
		Severidad: alerta.SeveridadWarning,
		Estado:    alerta.EstadoActiva,
		Mensaje: fmt.Sprintf("Temperatura T1 %.1f°C fuera del objetivo %.1f°C ±%.1f°C (promedio de %d %s)",
			promedio, objetivo, tolerancia, n, sufijo),
	}
	if err := loop.alertas.Save(a); err != nil {
		log.Printf("[control] Save alerta (temperatura) lote %s: %v", loop.loteID, err)
		return
	}
	loop.alertaTemperaturaActiva = true
}

// PromedioUltimasTemperaturas promedia las últimas n muestras con TempHorno1
// presente. Las muestras llegan ordenadas de la más reciente a la más antigua.
func PromedioUltimasTemperaturas(metricas []metricashorno.MetricaHorno, n int) (float64, int) {
	suma := 0.0
	cant := 0
	for i := range metricas {
		if cant == n {
			break
		}
		if metricas[i].TempHorno1 == nil {
			continue
		}
		suma += *metricas[i].TempHorno1
		cant++
	}
	if cant == 0 {
		return 0, 0
	}
	return suma / float64(cant), cant
}

// tickPreheat mantiene T1 en el setpoint de receta sin correr el MPC. Cuando
// la temperatura se mantiene en tolerancia durante 3 muestras, arranca la
// adaptación y entrega un único setpoint de transición (T + V de receta).
func (loop *Loop) tickPreheat(ctx context.Context, metricas []metricashorno.MetricaHorno, lote *lote_sector.Lote) {
	now := loop.now().UTC()

	var ultima *metricashorno.MetricaHorno
	for i := range metricas {
		if metricas[i].TempHorno1 != nil {
			ultima = &metricas[i]
			break
		}
	}
	if !loop.preheatInicio.IsZero() &&
		now.Sub(loop.preheatInicio) > time.Duration(loop.preheatTimeoutSeg)*time.Second {
		skipped := "precalentamiento_timeout"
		d := &decisiones_control.DecisionControl{
			LoteID:        loop.loteID,
			VentanaInicio: now,
			VentanaFin:    now,
			Muestras:      0,
			TempActual:    0,
			VelActual:     floatPtr(0),
			AplicadoPor:   decisiones_control.AplicadoPorNadie,
			RazonSkipped:  &skipped,
		}
		if ultima != nil {
			if ultima.TempHorno1 != nil {
				d.TempActual = *ultima.TempHorno1
			}
			if ultima.VelocidadCinta != nil {
				d.VelActual = ultima.VelocidadCinta
			}
			d.ConfPromedio = confianzaPromedio(metricas)
		}
		if err := loop.decisiones.Save(ctx, d); err != nil {
			log.Printf("[control] Save decision (precalentamiento_timeout): %v", err)
		}
		loop.alertarPreheatTimeout(ctx, lote, ultima, now)
		log.Printf("[control] precalentamiento agotó %ds; loop abortado", loop.preheatTimeoutSeg)
		loop.shouldExit = true
		return
	}

	if ultima == nil {
		loop.skipSinMuestras(ctx, now, now)
		return
	}

	target, velReceta, ok := loop.objetivoReceta(ctx, lote)
	if !ok {
		skipped := "receta_sin_setpoint"
		d := &decisiones_control.DecisionControl{
			LoteID:        loop.loteID,
			VentanaInicio: now,
			VentanaFin:    now,
			Muestras:      1,
			TempActual:    *ultima.TempHorno1,
			VelActual:     ultima.VelocidadCinta,
			ConfPromedio:  confianzaPromedio(metricas),
			AplicadoPor:   decisiones_control.AplicadoPorNadie,
			RazonSkipped:  &skipped,
		}
		if err := loop.decisiones.Save(ctx, d); err != nil {
			log.Printf("[control] Save decision (receta_sin_setpoint): %v", err)
		}
		return
	}

	actual := *ultima.TempHorno1
	if absFloat(actual-target) <= lote.ToleranciaTempC {
		loop.muestrasEnTolerancia++
	} else {
		loop.muestrasEnTolerancia = 0
	}

	inicio := ultima.ReceivedAt
	if inicio.IsZero() {
		inicio = now
	}
	prop := &optimizador.Propuesta{Temp1: target, Velocidad: 0}
	skipped := "precalentando"
	decision := &decisiones_control.DecisionControl{
		LoteID:        loop.loteID,
		VentanaInicio: inicio,
		VentanaFin:    inicio,
		Muestras:      1,
		TempActual:    actual,
		VelActual:     ultima.VelocidadCinta,
		ConfPromedio:  confianzaPromedio(metricas),
		TempPropuesto: target,
		VelPropuesto:  0,
		AplicadoPor:   decisiones_control.AplicadoPorNadie,
		Prediccion:    serializarPrediccion(optimizador.PrediccionSalida{}, false),
		RazonSkipped:  &skipped,
	}
	if err := loop.decisiones.Save(ctx, decision); err != nil {
		log.Printf("[control] Save decision (precalentando): %v", err)
		return
	}
	loop.entregar(ctx, prop, decision)

	if loop.muestrasEnTolerancia < 3 {
		return
	}

	loop.fase = FaseAdaptando
	loop.muestrasEnTolerancia = 0
	vel := velReceta
	trans := &optimizador.Propuesta{Temp1: target, Velocidad: vel}
	transDecision := &decisiones_control.DecisionControl{
		LoteID:        loop.loteID,
		VentanaInicio: inicio,
		VentanaFin:    inicio,
		Muestras:      1,
		TempActual:    actual,
		VelActual:     ultima.VelocidadCinta,
		ConfPromedio:  confianzaPromedio(metricas),
		TempPropuesto: target,
		VelPropuesto:  vel,
		AplicadoPor:   decisiones_control.AplicadoPorNadie,
		Prediccion:    serializarPrediccion(optimizador.PrediccionSalida{}, false),
	}
	if err := loop.decisiones.Save(ctx, transDecision); err != nil {
		log.Printf("[control] Save decision (transicion): %v", err)
		return
	}
	loop.entregar(ctx, trans, transDecision)
}

// alertarPreheatTimeout genera una alerta CRITICAL por lote cuando el horno no
// alcanza la temperatura de receta a tiempo. Es best-effort: un fallo al
// guardar sólo se loguea y nunca altera el aborto del loop. El índice único
// parcial garantiza una sola alerta ACTIVA por lote+tipo.
func (loop *Loop) alertarPreheatTimeout(ctx context.Context, lote *lote_sector.Lote, ultima *metricashorno.MetricaHorno, now time.Time) {
	if loop.alertas == nil {
		return
	}

	transcurrido := int(now.Sub(loop.preheatInicio).Seconds())
	mensaje := fmt.Sprintf("Precalentamiento agotado tras %ds sin alcanzar la temperatura objetivo", transcurrido)
	if target, _, ok := loop.objetivoReceta(ctx, lote); ok {
		mensaje += fmt.Sprintf(" (objetivo %.1f°C)", target)
	}
	if ultima != nil && ultima.TempHorno1 != nil {
		mensaje += fmt.Sprintf("; última temperatura real %.1f°C", *ultima.TempHorno1)
	}

	loteID := loop.loteID
	a := &alerta.Alerta{
		SectorID:  &loop.sectorID,
		LoteID:    &loteID,
		HornoID:   loop.sectorID,
		Tipo:      alerta.TipoPrecalentamientoTimeout,
		Severidad: alerta.SeveridadCritical,
		Estado:    alerta.EstadoActiva,
		Mensaje:   mensaje,
	}
	if err := loop.alertas.Save(a); err != nil {
		log.Printf("[control] Save alerta (precalentamiento_timeout) lote %s: %v", loop.loteID, err)
	}
}

// objetivoReceta resuelve la temperatura/velocidad de arranque. Prefiere los
// setpoints persistidos del lote y cae a la receta del producto cuando el lote
// se abrió en MANUAL y luego se cambió a AUTOMATICO (en ese caso el lote no
// tiene temp1_objetivo/vel_objetivo sembrados).
func (loop *Loop) objetivoReceta(ctx context.Context, lote *lote_sector.Lote) (temp, vel float64, ok bool) {
	if lote != nil && lote.Temp1Objetivo != nil {
		vel = 0
		if lote.VelObjetivo != nil {
			vel = *lote.VelObjetivo
		}
		return *lote.Temp1Objetivo, vel, true
	}
	if loop.producto == nil {
		return 0, 0, false
	}
	prod, err := loop.producto.GetByID(ctx, loop.productoID)
	if err != nil || prod == nil || prod.TempSetpoint == nil {
		return 0, 0, false
	}
	vel = 0
	if prod.VelocidadCintaSetpoint != nil {
		vel = *prod.VelocidadCintaSetpoint
	}
	return *prod.TempSetpoint, vel, true
}

// entregar publica la propuesta para que la Pi la haga pull. Las consignas de
// precalentamiento/transición van autorizadas=true.
func (loop *Loop) entregar(ctx context.Context, prop *optimizador.Propuesta, decision *decisiones_control.DecisionControl) {
	loop.publicarPropuesta(prop, decision, true)
}

// calcularZObservado cuenta los eventos del lote en la ventana y devuelve:
//   - zObs: Z observada (nil cuando no hay eventos); el segundo retorno indica
//     si se pudo calcular
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
	z := optimizador.ZCalidad(loop.pesos, cPct, oPct, qPct)
	return &z, true
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
	estado.TempComb1 = last.TempComb1
	estado.HumedadHorno = last.HumedadHorno
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

func serializarPrediccion(p optimizador.PrediccionSalida, restriccionRelajada bool) []byte {
	b, _ := json.Marshal(decisiones_control.PrediccionResumen{
		PctCrudos:           p.PctCrudos,
		PctOk:               p.PctOk,
		PctQuemados:         p.PctQuemados,
		ModelVersion:        p.ModelVersion,
		EsPlaceholder:       p.EsPlaceholder,
		RestriccionRelajada: restriccionRelajada,
	})
	return b
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func mustJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

func floatPtr(x float64) *float64 { return &x }
