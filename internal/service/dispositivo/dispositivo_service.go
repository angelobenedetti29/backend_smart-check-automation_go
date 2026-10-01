package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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

// defaultRenamePushTimeout es el tiempo máximo de un PUT a la URL de renombre
// anunciada por una Raspberry. Una Pi local responde en milisegundos; detrás de
// un túnel (cloudflared, ngrok) puede tardar algunos segundos. Si vence, el
// rename desde el panel se cancela (la DB no se toca).
const defaultRenamePushTimeout = 10 * time.Second

// httpClientRenamePush es el cliente HTTP dedicado al push de renombre. Es
// paquete-local y de larga vida: reusa conexiones TLS y deja el timeout a
// cargo del contexto/request, no del http.Client (un cliente con Timeout
// impuesto cancela también los reintentos de keep-alive).
var httpClientRenamePush = &http.Client{}

// DispositivoService implementa dispositivo.Service orquestando el repositorio,
// el caché de estado en memoria y el broker SSE.
type DispositivoService struct {
	repo          dispositivo.Repository
	store         dispositivo.StateStore
	broker        *sse.Broker
	renameTimeout time.Duration
}

// NewDispositivoService instancia el servicio inyectando repositorio, state
// store, broker SSE y timeout del push de renombre. Si renameTimeout <= 0 se
// usa defaultRenamePushTimeout.
func NewDispositivoService(repo dispositivo.Repository, store dispositivo.StateStore, broker *sse.Broker, renameTimeout time.Duration) *DispositivoService {
	if renameTimeout <= 0 {
		renameTimeout = defaultRenamePushTimeout
	}
	return &DispositivoService{repo: repo, store: store, broker: broker, renameTimeout: renameTimeout}
}

// normalizeSectorID trata un sectorId vacío o en blanco como "sin sector" (nil).
func normalizeSectorID(sectorID *string) *string {
	if sectorID == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*sectorID)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// Create da de alta un dispositivo nuevo: persiste en el catálogo (PostgreSQL),
// lo registra en el caché de estado como offline para que aparezca de inmediato
// en GET /api/v1/dispositivos, y emite el evento SSE dispositivo.state.
func (s *DispositivoService) Create(ctx context.Context, req dispositivo.CreateDispositivoRequest) (*dispositivo.EstadoDispositivo, error) {
	tipo, ok := dispositivo.ParseTipoDispositivo(req.Tipo)
	if !ok {
		return nil, &dispositivo.ValidationError{Fields: []string{"type: debe ser ENTRADA_HORNO o SALIDA_HORNO"}}
	}
	tipoStr := string(tipo)

	d := dispositivo.Dispositivo{
		Nombre:   strings.TrimSpace(req.Nombre),
		SectorID: normalizeSectorID(req.SectorID),
		WhepURL:  req.WhepURL,
		Tipo:     &tipoStr,
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
			SectorID:      d.SectorID,
			WhepURL:       d.WhepURL,
			Tipo:          d.Tipo,
			Estado:        dispositivo.EstadoOffline,
		}
	}
	s.broadcastState(*estado)

	return estado, nil
}

// Update modifica el sector y el whepUrl de un dispositivo existente, preservando
// su nombre y tipo (el nombre solo puede cambiarlo el rename autenticado del nodo).
// Persiste en el catálogo (PostgreSQL), actualiza el caché de estado preservando
// salud/métrica/last_seen, y emite el evento SSE dispositivo.state para que el
// frontend en vivo vea el sector/whepUrl nuevos. Propaga ErrDispositivoNotFound si
// no existe, ErrSectorNotFound si el sector no existe y ErrSectorTipoDuplicado si
// el sector ya tiene un dispositivo de ese tipo.
func (s *DispositivoService) Update(ctx context.Context, req dispositivo.UpdateDispositivoRequest) (*dispositivo.EstadoDispositivo, error) {
	d := dispositivo.Dispositivo{
		ID:       strings.TrimSpace(req.DispositivoID),
		SectorID: normalizeSectorID(req.SectorID),
		WhepURL:  req.WhepURL,
	}

	// El tipo es inmutable: se preserva el existente y nunca se pisa con el body.
	existing, err := s.repo.GetDispositivoByID(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	d.Tipo = existing.Tipo
	// El nombre es inmutable desde el panel: se preserva el existente. Solo el
	// rename autenticado del nodo (Rename) puede cambiarlo.
	d.Nombre = existing.Nombre

	if err := s.repo.Update(ctx, &d); err != nil {
		return nil, err
	}

	s.store.UpdateDispositivo(d)

	estado, ok := s.store.Get(d.ID)
	if !ok {
		estado = &dispositivo.EstadoDispositivo{
			DispositivoID: d.ID,
			Nombre:        d.Nombre,
			SectorID:      d.SectorID,
			WhepURL:       d.WhepURL,
			Tipo:          d.Tipo,
			Estado:        dispositivo.EstadoOffline,
		}
	}
	s.broadcastState(*estado)

	return estado, nil
}

// Rename cambia solo el nombre de un dispositivo existente: carga el registro
// para preservar sector y whepUrl, persiste, actualiza el caché de estado y
// emite el evento SSE dispositivo.state. Propaga ErrDispositivoNotFound.
func (s *DispositivoService) Rename(ctx context.Context, deviceID, nombre string) (*dispositivo.EstadoDispositivo, error) {
	d, err := s.repo.GetDispositivoByID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	d.Nombre = strings.TrimSpace(nombre)
	if err := s.repo.Update(ctx, d); err != nil {
		return nil, err
	}
	s.store.UpdateDispositivo(*d)
	estado, ok := s.store.Get(d.ID)
	if !ok {
		estado = &dispositivo.EstadoDispositivo{
			DispositivoID: d.ID,
			Nombre:        d.Nombre,
			SectorID:      d.SectorID,
			WhepURL:       d.WhepURL,
			Tipo:          d.Tipo,
			Estado:        dispositivo.EstadoOffline,
		}
	}
	s.broadcastState(*estado)
	return estado, nil
}

// UpdateRenameURL persiste la URL de renombre anunciada por la propia Raspberry.
// No emite SSE ni toca el caché: es una reconfiguración, no un cambio de salud.
// Propaga ErrDispositivoNotFound.
func (s *DispositivoService) UpdateRenameURL(ctx context.Context, deviceID, renameURL string) error {
	return s.repo.UpdateRenameURL(ctx, deviceID, renameURL)
}

// RenameFromPanel cambia el nombre de un dispositivo desde el panel del
// operador. Aplica push-first para mantener consistencia estricta entre el
// backend y la Raspberry:
//
//  1. Carga el dispositivo. Si no anunció rename_url/rename_token (firmware
//     legacy), devuelve ErrRenameNotPushable sin tocar la DB — aceptar el
//     cambio haría que la Pi lo sobrescriba en su próximo polling.
//  2. PUT a {rename_url}/api/v1/dispositivos/{id}/nombre con Bearer
//     <rename_token>. Si la red falla, el nodo responde no-2xx o vence el
//     timeout, devuelve el error envuelto y NO persiste en la DB: la Pi
//     conserva su nombre viejo y el operador ve el fallo para reintentar.
//  3. Sólo si el push devuelve 2xx, delega en Rename (persiste en DB, refresca
//     caché, emite SSE). La Pi ya tiene el nuevo nombre aplicado por el push,
//     por lo que no lo va a re-empujar en su próximo polling.
//
// Caso degradado: si el push tiene éxito pero la DB falla, el operador verá
// el error y podrá reintentar — el push es idempotente en la Pi (su handler
// siempre aplica el nombre recibido), así que el reintento deja backend y Pi
// consistentes sin intervención adicional.
func (s *DispositivoService) RenameFromPanel(ctx context.Context, deviceID, nombre string) (*dispositivo.EstadoDispositivo, error) {
	d, err := s.repo.GetDispositivoByID(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	if d.RenameURL == nil || strings.TrimSpace(*d.RenameURL) == "" ||
		d.RenameToken == nil || strings.TrimSpace(*d.RenameToken) == "" {
		return nil, dispositivo.ErrRenameNotPushable
	}

	nombre = strings.TrimSpace(nombre)
	if err := s.pushRename(ctx, *d.RenameURL, *d.RenameToken, d.ID, nombre); err != nil {
		return nil, err
	}

	return s.Rename(ctx, deviceID, nombre)
}

// pushRename realiza el PUT del renombre a la URL local anunciada por la
// Raspberry. Devuelve error si la red falla, si el nodo responde no-2xx o si
// vence el timeout. El cuerpo de respuesta se descarta tras leerlo (necesario
// para reusar la conexión).
func (s *DispositivoService) pushRename(ctx context.Context, renameURL, renameToken, deviceID, nombre string) error {
	pushCtx, cancel := context.WithTimeout(ctx, s.renameTimeout)
	defer cancel()

	endpoint := strings.TrimRight(renameURL, "/") + dispositivo.RenamePushPath(deviceID)
	payload, err := json.Marshal(map[string]string{"nombre": nombre})
	if err != nil {
		return fmt.Errorf("no se pudo serializar el renombre: %w", err)
	}

	req, err := http.NewRequestWithContext(pushCtx, http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("no se pudo armar el request de renombre a %s: %w", endpoint, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+renameToken)

	resp, err := httpClientRenamePush.Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo contactar al dispositivo para el renombre: %w", err)
	}
	defer resp.Body.Close()
	// Drena y descarta el body para permitir reuse de conexión.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("el dispositivo respondió %d al renombre (esperaba 2xx)", resp.StatusCode)
	}

	log.Printf("[DISPOSITIVO] Renombre push OK a %s para dispositivo %s", endpoint, deviceID)
	return nil
}

// Revoke da de baja lógica un dispositivo: revoca su credencial, lo desasigna
// del sector y lo quita del caché. Conserva la fila y el historial de
// telemetría para no romper las FKs de auditoría/provenance. El nodo queda
// autenticando 401 con su secret viejo.
func (s *DispositivoService) Revoke(ctx context.Context, actorEmail, id string) error {
	if err := s.repo.Revoke(ctx, actorEmail, id); err != nil {
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
	if !ok {
		return nil, deviceauth.ErrInvalidToken
	}
	// El id del principal es la fuente de verdad; si el ping trae un id distinto,
	// se rechaza para evitar suplantación.
	if req.DispositivoID != "" && req.DispositivoID != principal.DeviceID {
		return nil, deviceauth.ErrInvalidToken
	}
	d, err := s.repo.GetDispositivoByID(ctx, principal.DeviceID)
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
