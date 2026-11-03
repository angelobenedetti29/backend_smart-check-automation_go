package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/horno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/oven_controller"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// OvenController abstrae el cliente que despacha setpoints al controlador del
// horno. *oven_controller.SimulatedOvenController es la implementación actual
// (stub interno del backend); la interfaz permite inyectar dobles de prueba y,
// el día que aparezca otro driver (MQTT/API local), una implementación real con
// la misma firma.
type OvenController interface {
	SendSetpoint(hornoID string, temperatura, velocidad float64) oven_controller.DispatchResult
}

// OverrideRepo es el mínimo del repo de decisiones_control que necesita el
// service para encolar un override manual como decisión ya aprobada.
type OverrideRepo interface {
	Save(ctx context.Context, d *decisiones_control.DecisionControl) error
	Aprobar(ctx context.Context, decisionID, usuario string) error
}

// LoteLookup resuelve el estado/modo del lote antes de encolar el override.
type LoteLookup interface {
	GetByID(ctx context.Context, loteID string) (*lote_sector.Lote, error)
}

// ConsignaService implementa consigna.Service. Es el mecanismo del envío
// manual de consigna (SCA-320): resuelve el setpoint, lo despacha al
// controlador del horno, actualiza el estado activo del horno y audita
// el resultado. Ante un fallo de enlace, genera una alerta crítica y fuerza al
// horno a modo CONTROL_MANUAL.
type ConsignaService struct {
	consignaRepo   consigna.Repository
	hornoRepo      horno.Repository
	productoRepo   producto.Repository
	alertaRepo     alerta.Repository
	controller     OvenController
	broker         *sse.Broker
	decisionesRepo OverrideRepo
	loteLookup     LoteLookup
	// overrideEnPi indica que el actuador es la Pi: la consigna manual se
	// encola para que la Pi la aplique (y luego ackee), en vez de asumir que
	// el backend ya llegó al controlador del horno.
	overrideEnPi bool
}

// NewConsignaService instancia el servicio inyectando repositorios, el
// cliente de hardware (real o simulado) y el broker SSE para reflejar
// cambios en el panel.
func NewConsignaService(
	consignaRepo consigna.Repository,
	hornoRepo horno.Repository,
	productoRepo producto.Repository,
	controller OvenController,
	broker *sse.Broker,
	alertaRepo alerta.Repository,
	decisionesRepo OverrideRepo,
	loteLookup LoteLookup,
	overrideEnPi bool,
) *ConsignaService {
	return &ConsignaService{
		consignaRepo:   consignaRepo,
		hornoRepo:      hornoRepo,
		productoRepo:   productoRepo,
		alertaRepo:     alertaRepo,
		controller:     controller,
		broker:         broker,
		decisionesRepo: decisionesRepo,
		loteLookup:     loteLookup,
		overrideEnPi:   overrideEnPi,
	}
}

// DispatchManual valida el request contra el rango seguro cargado en el
// producto (temp_min/temp_max y velocidad_cinta_min/max) y, si es válido,
// despacha la consigna solicitada por el operario desde el panel (SCA-320). No
// se bloquea si el horno está en CONTROL_MANUAL — es justamente la vía de
// escape para reactivarlo.
func (s *ConsignaService) DispatchManual(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
	var usuario *string
	if req.Usuario != "" {
		usuario = &req.Usuario
	}
	return s.Dispatch(ctx, req, consigna.OrigenManual, usuario)
}

// Dispatch es el punto de entrada unificado: panel (SCA-320) y control
// automático (SCA-400 / MPC) lo usan pasando el origen correspondiente. La
// validación contra el rango seguro del producto es idéntica en ambos casos.
func (s *ConsignaService) Dispatch(ctx context.Context, req consigna.ConsignaManualRequest, origen consigna.Origen, usuario *string) (*consigna.Consigna, error) {
	p, err := s.productoRepo.GetByID(ctx, req.ProductoID)
	if err != nil {
		return nil, consigna.ErrParametrosNoExiste
	}

	if req.TemperaturaObjetivo < p.TempMin || req.TemperaturaObjetivo > p.TempMax ||
		req.VelocidadCintaObjetivo < p.VelocidadCintaMin || req.VelocidadCintaObjetivo > p.VelocidadCintaMax {
		return nil, consigna.ErrFueraDeRango
	}

	prod := req.ProductoID
	return s.dispatch(ctx, req.HornoID, req.LoteID, &prod,
		req.TemperaturaObjetivo, req.VelocidadCintaObjetivo,
		origen, usuario)
}

// GetHistorialByLote devuelve el historial de auditoría de consignas de un lote.
func (s *ConsignaService) GetHistorialByLote(ctx context.Context, loteID string) ([]consigna.Consigna, error) {
	return s.consignaRepo.GetByLoteID(ctx, loteID)
}

// dispatch centraliza el flujo compartido: busca el horno, despacha al
// controlador del horno, y según el resultado:
//   - éxito: actualiza el estado activo del horno (y lo restaura a ACTIVO solo
//     si venía de un CONTROL_MANUAL previo) y audita.
//   - fallo: audita el intento, genera una alerta CRITICAL y fuerza al horno
//     a CONTROL_MANUAL, para que el control automático deje de insistir hasta
//     que un operario intervenga.
func (s *ConsignaService) dispatch(
	ctx context.Context,
	hornoID string,
	loteID, productoID *string,
	temperatura, velocidad float64,
	origen consigna.Origen,
	usuario *string,
) (*consigna.Consigna, error) {
	h, err := s.hornoRepo.GetByID(hornoID)
	if err != nil {
		return nil, consigna.ErrHornoNoExiste
	}

	prevTemp := h.Temperatura
	prevVel := h.VelocidadCinta

	result := s.controller.SendSetpoint(hornoID, temperatura, velocidad)

	rec := &consigna.Consigna{
		HornoID:                hornoID,
		LoteID:                 loteID,
		ProductoID:             productoID,
		TemperaturaObjetivo:    temperatura,
		VelocidadCintaObjetivo: velocidad,
		Origen:                 origen,
		Usuario:                usuario,
		Exitosa:                result.Aplicada,
		TemperaturaPrevia:      &prevTemp,
		VelocidadCintaPrevia:   &prevVel,
	}
	if principal, ok := deviceauth.PrincipalFromContext(ctx); ok {
		rec.DispositivoID = &principal.DeviceID
	}

	if !result.Aplicada {
		motivo := result.Motivo
		rec.MotivoError = &motivo
		if err := s.consignaRepo.Save(ctx, rec); err != nil {
			log.Printf("[ERROR] Error al auditar consigna fallida de horno %s: %v", hornoID, err)
		}
		s.handleFalloDeEnlace(h, motivo, origen)
		go s.broadcastConsigna(rec)
		return rec, consigna.ErrDispatchFallido
	}

	h.Temperatura = result.TemperaturaReal
	h.VelocidadCinta = result.VelocidadReal
	h.ProductoID = productoID
	h.LoteID = loteID
	if h.Estado == horno.EstadoControlManual {
		h.Estado = "ACTIVO" // éxito: restaura el control automático tras un fallo de enlace
	}
	if err := s.hornoRepo.Update(h); err != nil {
		log.Printf("[ERROR] Error al actualizar estado del horno %s tras despachar consigna: %v", hornoID, err)
	}

	if err := s.consignaRepo.Save(ctx, rec); err != nil {
		log.Printf("[ERROR] Error al auditar consigna exitosa de horno %s: %v", hornoID, err)
	}

	s.encolarOverrideManual(ctx, loteID, temperatura, velocidad, prevTemp, prevVel, usuario)

	go s.broadcastConsigna(rec)
	return rec, nil
}

// encolarOverrideManual deja una consigna manual como decisión de control ya
// aprobada, para que la Pi la levante al pollear consigna-propuesta en modo
// MANUAL y luego ackee (el ack actualiza los objetivos del lote).
//
// shortcut: manual override solo se encola en modo MANUAL; en AUTOMATICO/SUPERVISION manda el MPC.
func (s *ConsignaService) encolarOverrideManual(
	ctx context.Context,
	loteID *string,
	temperatura, velocidad, prevTemp, prevVel float64,
	usuario *string,
) {
	if !s.overrideEnPi || loteID == nil || s.decisionesRepo == nil || s.loteLookup == nil {
		return
	}
	lote, err := s.loteLookup.GetByID(ctx, *loteID)
	if err != nil || lote == nil {
		return
	}
	if lote.Estado != lote_sector.EstadoAbierto || lote.Modo != lote_sector.ModoManual {
		return
	}

	ahora := time.Now().UTC()
	d := &decisiones_control.DecisionControl{
		LoteID:         *loteID,
		VentanaInicio:  ahora,
		VentanaFin:     ahora,
		Muestras:       0,
		TempActual:     prevTemp,
		VelActual:      &prevVel,
		PctCrudosObs:   0,
		PctOkObs:       0,
		PctQuemadosObs: 0,
		ConfPromedio:   0,
		TempPropuesto:  temperatura,
		VelPropuesto:   velocidad,
		AplicadoPor:    decisiones_control.AplicadoPorNadie,
	}
	if err := s.decisionesRepo.Save(ctx, d); err != nil {
		log.Printf("[ERROR] Error al encolar override manual del lote %s: %v", *loteID, err)
		return
	}
	aprobador := "operario"
	if usuario != nil && *usuario != "" {
		aprobador = *usuario
	}
	if err := s.decisionesRepo.Aprobar(ctx, d.ID, aprobador); err != nil {
		log.Printf("[ERROR] Error al aprobar override manual del lote %s: %v", *loteID, err)
		return
	}
	log.Printf("[AUDIT] Override manual encolado y aprobado para lote %s (temp=%.2f vel=%.2f por %s)",
		*loteID, temperatura, velocidad, aprobador)
}

// handleFalloDeEnlace reacciona a un fallo de enlace con el controlador del
// horno: fuerza al horno a CONTROL_MANUAL y registra una alerta CRITICAL para
// que quede visible en el panel, además del registro de auditoría ya guardado.
func (s *ConsignaService) handleFalloDeEnlace(h *horno.Horno, motivo string, origen consigna.Origen) {
	h.Estado = horno.EstadoControlManual
	if err := s.hornoRepo.Update(h); err != nil {
		log.Printf("[ERROR] Error al forzar CONTROL_MANUAL en horno %s: %v", h.ID, err)
	}

	a := &alerta.Alerta{
		HornoID:   h.ID,
		Tipo:      alerta.TipoConsignaEnlace,
		Severidad: alerta.SeveridadCritical,
		Estado:    alerta.EstadoActiva,
		Mensaje:   fmt.Sprintf("Fallo de enlace con el controlador físico del horno (origen %s): %s. El horno pasó a modo CONTROL_MANUAL hasta que un operario confirme la reconexión.", origen, motivo),
	}
	if err := s.alertaRepo.Save(a); err != nil {
		log.Printf("[ERROR] Error al guardar alerta crítica de fallo de enlace en horno %s: %v", h.ID, err)
	}
}

// broadcastConsigna emite el evento SSE horno.consigna con el resultado del
// despacho, para que el panel refleje el cambio sin necesidad de pollear.
// Se ejecuta en una goroutine separada: un fallo de broadcast no afecta la respuesta HTTP.
func (s *ConsignaService) broadcastConsigna(c *consigna.Consigna) {
	payload := map[string]interface{}{
		"success": c.Exitosa,
		"message": "Consigna despachada al horno",
		"data":    c,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[SSE] Error al serializar horno.consigna: %v", err)
		return
	}

	s.broker.Broadcast("horno.consigna", jsonData)
}
