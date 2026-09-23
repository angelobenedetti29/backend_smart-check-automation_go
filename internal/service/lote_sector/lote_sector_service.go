// Package service implementa la lógica de negocio del ciclo de vida de un lote
// coordinado entre los dispositivos de un sector: apertura idempotente, reporte
// de eventos en vivo, cierre autoritativo e historial paginado por cursor.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"regexp"
	"strings"
	"time"

	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

const (
	// maxEventosPorBatch es el máximo de eventos admitidos en un request de §6.
	maxEventosPorBatch = 100
	// defaultHistorialLimite es el límite de página por defecto de §8.
	defaultHistorialLimite = 20
	// maxHistorialLimite es el tope de página admitido de §8.
	maxHistorialLimite = 100
)

// uuidPattern valida que un evento_id sea un UUID canónico (8-4-4-4-12 hex).
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// motivosCierreValidos es el conjunto admitido por el CHECK de
// lotes_productivos.motivo_cierre, ampliado con "seguridad".
var motivosCierreValidos = map[string]struct{}{
	"sin_detecciones": {},
	"manual":          {},
	"apagado":         {},
	"seguridad":       {},
}

// SectorInfo es la vista de GET /api/v1/dispositivos/sector: el sector del
// dispositivo autenticado junto con sus compañeros.
type SectorInfo struct {
	SectorID   string             `json:"sector_id"`
	Nombre     string             `json:"nombre"`
	Tipo       string             `json:"tipo"`
	Companeros []sector.Companero `json:"companeros"`
}

// Service orquesta repositorios, broker SSE y reglas de negocio del ciclo de
// lote por sector.
type Service struct {
	loteRepo             lotesector.Repository
	sectorRepo           sector.Repository
	productoRepo         producto.Repository
	broker               *sse.Broker
	minInactividadCierre time.Duration
	// now es el reloj del servidor, inyectable en tests. Por defecto time.Now.
	now func() time.Time
}

// NewService instancia el servicio inyectando repositorios, broker SSE y el
// umbral de inactividad para reforzar el cierre (0 = no enforce).
func NewService(loteRepo lotesector.Repository, sectorRepo sector.Repository, productoRepo producto.Repository, broker *sse.Broker, minInactividadCierre time.Duration) *Service {
	return &Service{
		loteRepo:             loteRepo,
		sectorRepo:           sectorRepo,
		productoRepo:         productoRepo,
		broker:               broker,
		minInactividadCierre: minInactividadCierre,
		now:                  time.Now,
	}
}

// resolveSectorID resuelve el sector del dispositivo autenticado. Devuelve
// sector.ErrSinSector si el dispositivo no existe o no tiene sector asignado.
func (s *Service) resolveSectorID(ctx context.Context, deviceID string) (string, error) {
	info, err := s.sectorRepo.GetDeviceInfo(ctx, deviceID)
	if err != nil {
		return "", err
	}
	if info.SectorID == nil || *info.SectorID == "" {
		return "", sector.ErrSinSector
	}
	return *info.SectorID, nil
}

// SectorDelDispositivo devuelve el sector del dispositivo y sus compañeros.
func (s *Service) SectorDelDispositivo(ctx context.Context, deviceID string) (*SectorInfo, error) {
	info, err := s.sectorRepo.GetDeviceInfo(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if info.SectorID == nil || *info.SectorID == "" {
		return nil, sector.ErrSinSector
	}
	sectorID := *info.SectorID

	sec, err := s.sectorRepo.GetByID(ctx, sectorID)
	if err != nil {
		return nil, err
	}
	companeros, err := s.sectorRepo.ListCompaneros(ctx, sectorID, deviceID)
	if err != nil {
		return nil, err
	}
	if companeros == nil {
		companeros = []sector.Companero{}
	}
	return &SectorInfo{
		SectorID:   sectorID,
		Nombre:     sec.Nombre,
		Tipo:       info.Tipo,
		Companeros: companeros,
	}, nil
}

// Abrir abre (o se adjunta a) el lote abierto del sector del dispositivo. El
// producto debe existir en el catálogo, aunque esté inactivo. Emite
// lote.creado sólo cuando el lote se crea realmente.
func (s *Service) Abrir(ctx context.Context, deviceID, productoID, idempotencyKey string) (*lotesector.Lote, bool, error) {
	sectorID, err := s.resolveSectorID(ctx, deviceID)
	if err != nil {
		return nil, false, err
	}
	if _, err := s.productoRepo.GetByID(ctx, productoID); err != nil {
		return nil, false, err
	}

	lote, creado, err := s.loteRepo.Abrir(ctx, lotesector.AbrirParams{
		SectorID:       sectorID,
		ProductoID:     productoID,
		AbiertoPor:     deviceID,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, false, err
	}
	s.applyInactividad(lote)
	if creado {
		s.broadcast("lote.creado", lote)
	}
	return lote, creado, nil
}

// Abierto devuelve el lote abierto del sector, o nil si no hay ninguno.
func (s *Service) Abierto(ctx context.Context, sectorID string) (*lotesector.Lote, error) {
	lote, err := s.loteRepo.GetAbiertoBySector(ctx, sectorID)
	if err != nil {
		return nil, err
	}
	if lote == nil {
		return nil, nil
	}
	s.applyInactividad(lote)
	return lote, nil
}

// RegistrarEventos valida el batch (1..100 eventos; cada evento_id y
// producto_id UUID, estado en el dominio y confianza en [0,1]) y lo aplica al
// lote del sector. Emite lote.actualizado sólo cuando el batch acepta al menos
// un evento (un reintento sólo-duplicados no genera evento SSE).
func (s *Service) RegistrarEventos(ctx context.Context, deviceID, loteID string, eventos []lotesector.Evento) (int, int, *lotesector.Lote, error) {
	sectorID, err := s.resolveSectorID(ctx, deviceID)
	if err != nil {
		return 0, 0, nil, err
	}
	if len(eventos) == 0 {
		return 0, 0, nil, lotesector.ErrPayloadInvalido
	}
	if len(eventos) > maxEventosPorBatch {
		return 0, 0, nil, lotesector.ErrDemasiadosEventos
	}
	for i := range eventos {
		if err := validateEvento(eventos[i]); err != nil {
			return 0, 0, nil, err
		}
	}

	aceptados, duplicados, lote, err := s.loteRepo.RegistrarEventos(ctx, loteID, sectorID, deviceID, eventos)
	if err != nil {
		return 0, 0, nil, err
	}
	s.applyInactividad(lote)
	if aceptados > 0 {
		s.broadcast("lote.actualizado", lote)
	}
	return aceptados, duplicados, lote, nil
}

// validateEvento aplica las reglas de §6/§10 que de otro modo llegarían a un
// CHECK de la base (23514) y se traducirían en un 500 reintentable. Devuelve
// ErrPayloadInvalido ante cualquier violación.
func validateEvento(ev lotesector.Evento) error {
	if !uuidPattern.MatchString(ev.EventoID) || !uuidPattern.MatchString(ev.ProductoID) {
		return lotesector.ErrPayloadInvalido
	}
	if ev.Estado != nil {
		switch *ev.Estado {
		case lotesector.EstadoOK, lotesector.EstadoCrudo, lotesector.EstadoQuemado:
		default:
			return lotesector.ErrPayloadInvalido
		}
	}
	if ev.Confianza != nil && (*ev.Confianza < 0 || *ev.Confianza > 1) {
		return lotesector.ErrPayloadInvalido
	}
	return nil
}

// Cerrar fija el conteo final autoritativo del lote. Valida motivo y coherencia
// de conteos, y si hay umbral configurado rechaza con ErrSectorActivo mientras
// el sector siga activo. Emite lote.cerrado sólo en la transición real a
// CERRADO (un reintento idempotente no vuelve a emitir).
func (s *Service) Cerrar(ctx context.Context, deviceID, loteID, motivo string, conteos lotesector.Conteos, idempotencyKey string) (*lotesector.Lote, error) {
	sectorID, err := s.resolveSectorID(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	if _, ok := motivosCierreValidos[motivo]; !ok {
		return nil, lotesector.ErrPayloadInvalido
	}
	if !conteosConsistentes(conteos) {
		return nil, lotesector.ErrPayloadInvalido
	}

	if s.minInactividadCierre > 0 {
		actual, err := s.loteRepo.GetByID(ctx, loteID)
		if err != nil {
			return nil, err
		}
		// Un lote de otro sector es 403 antes que cualquier gate de negocio.
		if actual.SectorID != sectorID {
			return nil, lotesector.ErrAjeno
		}
		// El gate de inactividad sólo aplica a un lote ABIERTO: un reintento
		// del mismo cierre sobre un lote ya CERRADO debe ser 200 idempotente
		// (§7), nunca 409.
		if actual.Estado == lotesector.EstadoAbierto && s.computeInactividad(actual) < s.minInactividadCierre.Seconds() {
			return nil, lotesector.ErrSectorActivo
		}
	}

	lote, yaCerrado, err := s.loteRepo.Cerrar(ctx, lotesector.CerrarParams{
		LoteID:         loteID,
		SectorID:       sectorID,
		Conteos:        conteos,
		Motivo:         motivo,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	s.applyInactividad(lote)
	if !yaCerrado {
		s.broadcast("lote.cerrado", lote)
	}
	return lote, nil
}

// Historial devuelve una página de lotes del sector (más nuevos primero) y el
// total que matchea los filtros. El cursor es opaco: base64url de
// "<inicio_at RFC3339Nano>|<id>". Un cursor malformado se rechaza con
// ErrPayloadInvalido, nunca con un error interno.
func (s *Service) Historial(ctx context.Context, sectorID, productoID string, limite int, cursor string) ([]lotesector.Lote, int, error) {
	if limite <= 0 {
		limite = defaultHistorialLimite
	}
	if limite > maxHistorialLimite {
		limite = maxHistorialLimite
	}

	params := lotesector.HistorialParams{
		SectorID:   sectorID,
		ProductoID: productoID,
		Limite:     limite,
	}
	if cursor != "" {
		antesDe, antesDeID, err := DecodeCursor(cursor)
		if err != nil {
			return nil, 0, lotesector.ErrPayloadInvalido
		}
		params.AntesDe = &antesDe
		params.AntesDeID = antesDeID
	}

	lotes, total, err := s.loteRepo.Historial(ctx, params)
	if err != nil {
		return nil, 0, err
	}
	for i := range lotes {
		s.applyInactividad(&lotes[i])
	}
	return lotes, total, nil
}

// EncodeCursor serializa el par (inicio_at, id) en el cursor opaco base64url
// que consume Historial.
func EncodeCursor(inicioAt time.Time, id string) string {
	raw := inicioAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor revierte EncodeCursor. Acepta base64url sin padding y con
// padding; cualquier violación devuelve error.
func DecodeCursor(cursor string) (time.Time, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(cursor)
		if err != nil {
			return time.Time{}, "", err
		}
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return time.Time{}, "", errors.New("cursor malformado")
	}
	// El id debe ser un UUID: un cursor forjado no debe llegar a compararse
	// contra la columna uuid (22P02 → 500).
	if !uuidPattern.MatchString(parts[1]) {
		return time.Time{}, "", errors.New("cursor id inválido")
	}
	inicioAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return inicioAt, parts[1], nil
}

// conteosConsistentes valida la invariante de §10: total == suma de los
// buckets no nulos. Además exige que ningún bucket sea negativo (el CHECK de la
// base lo rechazaría con 23514 → 500).
func conteosConsistentes(c lotesector.Conteos) bool {
	sum := 0
	for _, n := range []*int{c.OK, c.Crudo, c.Quemado} {
		if n == nil {
			continue
		}
		if *n < 0 {
			return false
		}
		sum += *n
	}
	return sum == c.Total
}

// computeInactividad calcula los segundos transcurridos desde el último evento
// (de cualquiera de los dos dispositivos del sector) o desde la apertura,
// según el reloj del servidor. Nunca devuelve un valor negativo.
func (s *Service) computeInactividad(lote *lotesector.Lote) float64 {
	if lote == nil {
		return 0
	}
	ref := lote.AbiertoEn
	if lote.UltimoEventoEn != nil {
		ref = *lote.UltimoEventoEn
	}
	secs := s.now().Sub(ref).Seconds()
	if secs < 0 {
		return 0
	}
	return secs
}

// applyInactividad refresca InactividadSegundos del lote con el reloj actual.
func (s *Service) applyInactividad(lote *lotesector.Lote) {
	if lote == nil {
		return
	}
	lote.InactividadSegundos = s.computeInactividad(lote)
}

// broadcast emite el objeto lote de §4 por el broker SSE. Un fallo de
// serialización sólo se loguea: nunca afecta la respuesta HTTP.
func (s *Service) broadcast(eventType string, lote *lotesector.Lote) {
	if s.broker == nil || lote == nil {
		return
	}
	jsonData, err := json.Marshal(lote)
	if err != nil {
		log.Printf("[SSE] Error al serializar %s: %v", eventType, err)
		return
	}
	s.broker.Broadcast(eventType, jsonData)
}
