package service

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

const (
	defaultPage     = 1
	defaultPageSize = 10
	maxPageSize     = 100
)

// insertMetricaTimeout limita el tiempo de una escritura de historial fire-and-forget.
const insertMetricaTimeout = 5 * time.Second

// DispositivoService implementa dispositivo.Service orquestando el repositorio,
// el caché de estado en memoria y el broker SSE.
type DispositivoService struct {
	repo   dispositivo.Repository
	store  dispositivo.StateStore
	broker *sse.Broker
}

// NewDispositivoService instancia el servicio inyectando repositorio, state store y broker SSE.
func NewDispositivoService(repo dispositivo.Repository, store dispositivo.StateStore, broker *sse.Broker) *DispositivoService {
	return &DispositivoService{repo: repo, store: store, broker: broker}
}

// Create da de alta un dispositivo nuevo: persiste en el catálogo (PostgreSQL),
// lo registra en el caché de estado como offline para que aparezca de inmediato
// en GET /api/v1/dispositivos, y emite el evento SSE dispositivo.state.
func (s *DispositivoService) Create(ctx context.Context, req dispositivo.CreateDispositivoRequest) (*dispositivo.EstadoDispositivo, error) {
	d := dispositivo.Dispositivo{
		Nombre:    strings.TrimSpace(req.Nombre),
		Ubicacion: strings.TrimSpace(req.Ubicacion),
		WhepURL:   req.WhepURL,
	}

	if err := s.repo.Create(ctx, &d); err != nil {
		return nil, err
	}

	s.store.Register(d)

	estado, ok := s.store.Get(d.ID)
	if !ok {
		estado = &dispositivo.EstadoDispositivo{
			DispositivoID: d.ID,
			Nombre:        d.Nombre,
			Ubicacion:     d.Ubicacion,
			WhepURL:       d.WhepURL,
			Estado:        dispositivo.EstadoOffline,
		}
	}
	s.broadcastState(*estado)

	return estado, nil
}

// Update modifica nombre y ubicación de un dispositivo existente: persiste en el
// catálogo (PostgreSQL), actualiza el caché de estado preservando salud/métrica/
// last_seen, y emite el evento SSE dispositivo.state para que el frontend en vivo
// vea el nombre/ubicación nuevos. Propaga ErrDispositivoNotFound si no existe.
func (s *DispositivoService) Update(ctx context.Context, req dispositivo.UpdateDispositivoRequest) (*dispositivo.EstadoDispositivo, error) {
	d := dispositivo.Dispositivo{
		ID:        strings.TrimSpace(req.DispositivoID),
		Nombre:    strings.TrimSpace(req.Nombre),
		Ubicacion: strings.TrimSpace(req.Ubicacion),
		WhepURL:   req.WhepURL,
	}

	if err := s.repo.Update(ctx, &d); err != nil {
		return nil, err
	}

	s.store.UpdateDispositivo(d)

	estado, ok := s.store.Get(d.ID)
	if !ok {
		estado = &dispositivo.EstadoDispositivo{
			DispositivoID: d.ID,
			Nombre:        d.Nombre,
			Ubicacion:     d.Ubicacion,
			WhepURL:       d.WhepURL,
			Estado:        dispositivo.EstadoOffline,
		}
	}
	s.broadcastState(*estado)

	return estado, nil
}

// Delete elimina un dispositivo del catálogo y de la caché de estado. Sin evento
// SSE. Propaga ErrDispositivoNotFound si el dispositivo no existe.
func (s *DispositivoService) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	s.store.Remove(id)
	return nil
}

// ProcessPing procesa un heartbeat recibido de un dispositivo: valida su
// existencia en el catálogo, persiste la métrica en el historial (fire-and-forget),
// actualiza el estado actual a online y emite los eventos SSE correspondientes.
func (s *DispositivoService) ProcessPing(ctx context.Context, req dispositivo.PingRequest) (*dispositivo.EstadoDispositivo, error) {
	principal, ok := deviceauth.PrincipalFromContext(ctx)
	if !ok || principal.Enrollment || principal.DeviceID != req.DispositivoID {
		return nil, deviceauth.ErrInvalidProof
	}
	d, err := s.repo.GetDispositivoByID(ctx, req.DispositivoID)
	if err != nil {
		return nil, err
	}

	metrica := dispositivo.MetricaDispositivo{
		DispositivoID:              d.ID,
		CpuPct:                     req.CpuPct,
		MemRamDisponibleMb:         req.MemRamDisponibleMb,
		MemRamTotalMb:              req.MemRamTotalMb,
		AlmacenamientoDisponibleMb: req.AlmacenamientoDisponibleMb,
		AlmacenamientoTotalMb:      req.AlmacenamientoTotalMb,
		TempChip:                   req.TempChip,
		AiProcessorPct:             req.AiProcessorPct,
		ReceivedAt:                 time.Now().UTC(),
	}

	// Persistir historial de forma asíncrona: el estado online no depende de la DB.
	// Se pasa una copia para que la goroutine pueda mutar m.ID sin correr contra el
	// metrica original que store.Update lee a continuación.
	go func(m dispositivo.MetricaDispositivo) { s.insertMetrica(&m) }(metrica)

	prevState, currState, estado := s.store.Update(*d, metrica)

	s.broadcastMetric(*estado)

	// Solo se emite dispositivo.state cuando hay una transición real offline→online.
	if prevState != currState {
		s.broadcastState(*estado)
	}

	return estado, nil
}

// GetAllEstados devuelve el estado actual de todos los dispositivos desde el caché en memoria.
func (s *DispositivoService) GetAllEstados() []dispositivo.EstadoDispositivo {
	return s.store.GetAll()
}

// GetMetricas devuelve una página del historial de métricas de un dispositivo,
// aplicando límites de paginación. Propaga ErrDispositivoNotFound si el
// dispositivo no existe en el catálogo.
func (s *DispositivoService) GetMetricas(ctx context.Context, dispositivoID string, page, pageSize int) (*dispositivo.PaginatedResult, error) {
	if _, err := s.repo.GetDispositivoByID(ctx, dispositivoID); err != nil {
		return nil, err
	}

	if page < 1 {
		page = defaultPage
	}
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	return s.repo.GetMetricasByDispositivo(ctx, dispositivoID, page, pageSize)
}

// StartReaper ejecuta un ciclo periódico que marca como offline a los
// dispositivos cuyo last_seen supera el umbral y emite el evento SSE
// dispositivo.state por cada transición online→offline.
func (s *DispositivoService) StartReaper(ctx context.Context, interval, umbral time.Duration) {
	log.Printf("[DISPOSITIVO] Reaper iniciado: verifica offline cada %s con umbral de %s", interval, umbral)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case now := <-ticker.C:
			offlineIDs := s.store.MarkOfflineIfStale(umbral, now)
			for _, id := range offlineIDs {
				if estado, ok := s.store.Get(id); ok {
					s.broadcastState(*estado)
					log.Printf("[DISPOSITIVO] Dispositivo %s pasó a offline", id)
				}
			}

		case <-ctx.Done():
			log.Println("[DISPOSITIVO] Reaper detenido")
			return
		}
	}
}

// insertMetrica persiste una métrica en el historial con timeout propio.
// Se ejecuta en una goroutine separada; los errores solo se loguean.
func (s *DispositivoService) insertMetrica(m *dispositivo.MetricaDispositivo) {
	ctx, cancel := context.WithTimeout(context.Background(), insertMetricaTimeout)
	defer cancel()

	if err := s.repo.InsertMetrica(ctx, m); err != nil {
		log.Printf("[ERROR] Error al insertar métrica de dispositivo %s en el historial: %v", m.DispositivoID, err)
	}
}

// broadcastMetric emite el evento SSE dispositivo.metric con la última métrica recibida.
func (s *DispositivoService) broadcastMetric(estado dispositivo.EstadoDispositivo) {
	payload := map[string]interface{}{
		"success": true,
		"message": "Métricas de dispositivo recibidas",
		"data":    estado,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[SSE] Error al serializar dispositivo.metric: %v", err)
		return
	}

	s.broker.Broadcast("dispositivo.metric", jsonData)
}

// broadcastState emite el evento SSE dispositivo.state ante un cambio de estado.
func (s *DispositivoService) broadcastState(estado dispositivo.EstadoDispositivo) {
	payload := map[string]interface{}{
		"success": true,
		"message": "El estado del dispositivo cambió a " + estado.Estado,
		"data":    estado,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[SSE] Error al serializar dispositivo.state: %v", err)
		return
	}

	s.broker.Broadcast("dispositivo.state", jsonData)
}
