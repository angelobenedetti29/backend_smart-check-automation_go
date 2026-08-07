package service

import (
	"context"
	"encoding/json"
	"log"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/horno"
	parametrosproducto "github.com/angelobenedetti29/smart-check-automation/internal/domain/parametros_producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/oven_controller"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// ConsignaService implementa consigna.Service. Es el mecanismo compartido por
// el envío automático de consigna (SCA-142) y el envío manual (SCA-320):
// resuelve el setpoint, lo despacha al controlador físico simulado del
// horno, actualiza el estado activo del horno y audita el resultado.
type ConsignaService struct {
	consignaRepo consigna.Repository
	hornoRepo    horno.Repository
	paramRepo    parametrosproducto.Repository
	controller   *oven_controller.OvenControllerClient
	broker       *sse.Broker
}

// NewConsignaService instancia el servicio inyectando repositorios, el
// cliente simulado de hardware y el broker SSE para reflejar cambios en el panel.
func NewConsignaService(
	consignaRepo consigna.Repository,
	hornoRepo horno.Repository,
	paramRepo parametrosproducto.Repository,
	controller *oven_controller.OvenControllerClient,
	broker *sse.Broker,
) *ConsignaService {
	return &ConsignaService{
		consignaRepo: consignaRepo,
		hornoRepo:    hornoRepo,
		paramRepo:    paramRepo,
		controller:   controller,
		broker:       broker,
	}
}

// DispatchAutomatico resuelve el setpoint puntual cargado en parametros_producto
// para productoID y lo despacha automáticamente al horno. Se usa cuando la IA
// del nodo de entrada identifica la variedad de producto al iniciar un lote (SCA-142).
func (s *ConsignaService) DispatchAutomatico(ctx context.Context, hornoID, loteID, productoID string) (*consigna.Consigna, error) {
	p, err := s.paramRepo.GetByProductoID(ctx, productoID)
	if err != nil {
		return nil, consigna.ErrParametrosNoExiste
	}
	if p.TempSetpoint == nil || p.VelocidadCintaSetpoint == nil {
		return nil, consigna.ErrParametrosNoExiste
	}

	lote, prod := loteID, productoID
	return s.dispatch(ctx, hornoID, &lote, &prod, *p.TempSetpoint, *p.VelocidadCintaSetpoint, consigna.OrigenAutomatico, nil)
}

// DispatchManual valida el request contra el rango seguro cargado en
// parametros_producto para req.ProductoID y, si es válido, despacha la
// consigna solicitada por el operario desde el panel (SCA-320).
func (s *ConsignaService) DispatchManual(ctx context.Context, req consigna.ConsignaManualRequest) (*consigna.Consigna, error) {
	p, err := s.paramRepo.GetByProductoID(ctx, req.ProductoID)
	if err != nil {
		return nil, consigna.ErrParametrosNoExiste
	}

	if req.TemperaturaObjetivo < p.TempMin || req.TemperaturaObjetivo > p.TempMax ||
		req.VelocidadCintaObjetivo < p.VelocidadCintaMin || req.VelocidadCintaObjetivo > p.VelocidadCintaMax {
		return nil, consigna.ErrFueraDeRango
	}

	var usuario *string
	if req.Usuario != "" {
		usuario = &req.Usuario
	}
	prod := req.ProductoID
	return s.dispatch(ctx, req.HornoID, req.LoteID, &prod, req.TemperaturaObjetivo, req.VelocidadCintaObjetivo, consigna.OrigenManual, usuario)
}

// GetHistorialByLote devuelve el historial de auditoría de consignas de un lote.
func (s *ConsignaService) GetHistorialByLote(ctx context.Context, loteID string) ([]consigna.Consigna, error) {
	return s.consignaRepo.GetByLoteID(ctx, loteID)
}

// dispatch centraliza el flujo compartido: busca el horno, despacha al
// controlador físico (simulado), actualiza el estado activo del horno si la
// consigna fue aplicada, y audita el resultado (éxito o fallo) en el historial.
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

	if !result.Aplicada {
		motivo := result.Motivo
		rec.MotivoError = &motivo
		if err := s.consignaRepo.Save(ctx, rec); err != nil {
			log.Printf("[ERROR] Error al auditar consigna fallida de horno %s: %v", hornoID, err)
		}
		go s.broadcastConsigna(rec)
		return rec, consigna.ErrDispatchFallido
	}

	h.Temperatura = result.TemperaturaReal
	h.VelocidadCinta = result.VelocidadReal
	h.ProductoID = productoID
	h.LoteID = loteID
	h.Estado = "ACTIVO"
	if err := s.hornoRepo.Update(h); err != nil {
		log.Printf("[ERROR] Error al actualizar estado del horno %s tras despachar consigna: %v", hornoID, err)
	}

	if err := s.consignaRepo.Save(ctx, rec); err != nil {
		log.Printf("[ERROR] Error al auditar consigna exitosa de horno %s: %v", hornoID, err)
	}

	go s.broadcastConsigna(rec)
	return rec, nil
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
