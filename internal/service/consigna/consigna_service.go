package service

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

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

// MetricasLookup lee la telemetría real del horno para auditar la lectura
// previa a la consigna.
type MetricasLookup interface {
	GetUltimasParaLote(ctx context.Context, loteID string, limite int) ([]metricashorno.MetricaHorno, error)
}

// ConsignaService implementa consigna.Service. El backend no simula ni aplica
// consignas: la única actuadora es la Pi ENTRADA, que hace pull por lote. La
// consigna manual se encola como decisión aprobada para que la Pi la levante;
// el registro en historial_consignas audita que quedó encolada.
type ConsignaService struct {
	consignaRepo   consigna.Repository
	productoRepo   producto.Repository
	broker         *sse.Broker
	decisionesRepo OverrideRepo
	loteLookup     LoteLookup
	metricas       MetricasLookup
}

// NewConsignaService instancia el servicio inyectando repositorios y el broker
// SSE para reflejar el encolado en el panel.
func NewConsignaService(
	consignaRepo consigna.Repository,
	productoRepo producto.Repository,
	broker *sse.Broker,
	decisionesRepo OverrideRepo,
	loteLookup LoteLookup,
	metricas MetricasLookup,
) *ConsignaService {
	return &ConsignaService{
		consignaRepo:   consignaRepo,
		productoRepo:   productoRepo,
		broker:         broker,
		decisionesRepo: decisionesRepo,
		loteLookup:     loteLookup,
		metricas:       metricas,
	}
}

// DispatchManual valida la consigna contra el lote abierto MANUAL y el rango
// seguro del producto, y si es válida la encola para la Pi (SCA-320).
func (s *ConsignaService) DispatchManual(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
	if req.LoteID == nil || strings.TrimSpace(*req.LoteID) == "" {
		return nil, consigna.ErrLoteRequerido
	}

	lote, err := s.loteLookup.GetByID(ctx, *req.LoteID)
	if err != nil || lote == nil {
		return nil, consigna.ErrLoteNoExiste
	}
	if lote.Estado != lote_sector.EstadoAbierto {
		return nil, consigna.ErrLoteNoAbierto
	}
	if lote.Modo != lote_sector.ModoManual {
		return nil, consigna.ErrLoteNoManual
	}

	if ovenID := req.OvenID(); ovenID != "" && ovenID != lote.SectorID {
		return nil, consigna.ErrSectorMismatch
	}
	hornoID := lote.SectorID
	if hornoID == "" {
		return nil, consigna.ErrSectorRequerido
	}

	p, err := s.productoRepo.GetByID(ctx, req.ProductoID)
	if err != nil {
		return nil, consigna.ErrParametrosNoExiste
	}
	if req.TemperaturaObjetivo < p.TempMin || req.TemperaturaObjetivo > p.TempMax ||
		req.VelocidadCintaObjetivo < p.VelocidadCintaMin || req.VelocidadCintaObjetivo > p.VelocidadCintaMax {
		return nil, consigna.ErrFueraDeRango
	}

	// Lectura previa auditada: telemetría real del lote; nil si todavía no hay
	// muestras (el registro manual no inventa un valor).
	var prevTemp, prevVel *float64
	if s.metricas != nil {
		if muestras, merr := s.metricas.GetUltimasParaLote(ctx, lote.ID, 1); merr == nil && len(muestras) > 0 {
			prevTemp = muestras[0].TempHorno1
			prevVel = muestras[0].VelocidadCinta
		}
	}

	var usuario *string
	if req.Usuario != "" {
		usuario = &req.Usuario
	}

	prod := req.ProductoID
	rec := &consigna.Consigna{
		HornoID:                hornoID,
		LoteID:                 req.LoteID,
		ProductoID:             &prod,
		TemperaturaObjetivo:    req.TemperaturaObjetivo,
		VelocidadCintaObjetivo: req.VelocidadCintaObjetivo,
		Origen:                 consigna.OrigenManual,
		Usuario:                usuario,
		Exitosa:                true,
		TemperaturaPrevia:      prevTemp,
		VelocidadCintaPrevia:   prevVel,
	}
	if principal, ok := deviceauth.PrincipalFromContext(ctx); ok {
		rec.DispositivoID = &principal.DeviceID
	}

	decisionID, err := s.encolarOverrideManual(ctx, lote.ID, req.TemperaturaObjetivo, req.VelocidadCintaObjetivo, prevTemp, prevVel, usuario)
	if err != nil {
		motivo := "no se pudo encolar la consigna para la Pi"
		rec.Estado = consigna.EstadoConsignaRechazada
		rec.Exitosa = false
		rec.MotivoError = &motivo
		if serr := s.consignaRepo.Save(ctx, rec); serr != nil {
			log.Printf("[ERROR] Error al auditar consigna no encolada de horno %s: %v", hornoID, serr)
		}
		go s.broadcastConsigna(rec)
		return rec, consigna.ErrEncoladoFallido
	}
	rec.DecisionID = &decisionID
	rec.Estado = consigna.EstadoConsignaPendiente

	if err := s.consignaRepo.Save(ctx, rec); err != nil {
		log.Printf("[ERROR] Error al auditar consigna encolada de horno %s: %v", hornoID, err)
	}

	go s.broadcastConsigna(rec)
	return rec, nil
}

// GetHistorialByLote devuelve el historial de auditoría de consignas de un lote.
func (s *ConsignaService) GetHistorialByLote(ctx context.Context, loteID string) ([]consigna.Consigna, error) {
	return s.consignaRepo.GetByLoteID(ctx, loteID)
}

// encolarOverrideManual deja una consigna manual como decisión de control ya
// aprobada, para que la Pi la levante al pollear consigna-propuesta en modo
// MANUAL y luego ackee. Si la persistencia o la aprobación fallan, devuelve
// error: sin la fila aprobada la Pi nunca ve la consigna.
func (s *ConsignaService) encolarOverrideManual(
	ctx context.Context,
	loteID string,
	temperatura, velocidad float64,
	prevTemp, prevVel *float64,
	usuario *string,
) (string, error) {
	ahora := time.Now().UTC()
	tempActual := 0.0
	if prevTemp != nil {
		tempActual = *prevTemp
	}
	velActual := 0.0
	if prevVel != nil {
		velActual = *prevVel
	}
	d := &decisiones_control.DecisionControl{
		LoteID:         loteID,
		VentanaInicio:  ahora,
		VentanaFin:     ahora,
		Muestras:       0,
		TempActual:     tempActual,
		VelActual:      &velActual,
		PctCrudosObs:   0,
		PctOkObs:       0,
		PctQuemadosObs: 0,
		ConfPromedio:   0,
		TempPropuesto:  temperatura,
		VelPropuesto:   velocidad,
		AplicadoPor:    decisiones_control.AplicadoPorNadie,
	}
	if err := s.decisionesRepo.Save(ctx, d); err != nil {
		log.Printf("[ERROR] Error al encolar override manual del lote %s: %v", loteID, err)
		return "", err
	}
	aprobador := "operario"
	if usuario != nil && *usuario != "" {
		aprobador = *usuario
	}
	if err := s.decisionesRepo.Aprobar(ctx, d.ID, aprobador); err != nil {
		log.Printf("[ERROR] Error al aprobar override manual del lote %s: %v", loteID, err)
		return "", err
	}
	log.Printf("[AUDIT] Override manual encolado y aprobado para lote %s (temp=%.2f vel=%.2f por %s)",
		loteID, temperatura, velocidad, aprobador)
	return d.ID, nil
}

// broadcastConsigna emite el evento SSE horno.consigna con el resultado del
// encolado, para que el panel lo refleje sin pollear.
// Se ejecuta en una goroutine separada: un fallo de broadcast no afecta la respuesta HTTP.
func (s *ConsignaService) broadcastConsigna(c *consigna.Consigna) {
	payload := map[string]interface{}{
		"success": c.Exitosa,
		"message": "Consigna encolada para la Pi",
		"data":    c,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[SSE] Error al serializar horno.consigna: %v", err)
		return
	}

	s.broker.Broadcast("horno.consigna", jsonData)
}
