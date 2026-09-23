// Package controller expone los endpoints HTTP del ciclo de lote por sector:
// consulta de sector, apertura, lote abierto, reporte de eventos, cierre e
// historial paginado.
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/requestjson"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
	loteSectorService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// maxRequestBodyBytes limita el body de los requests device a 1 MB.
const maxRequestBodyBytes = 1 << 20

// errSectorIDRequerido señala que una lectura OAuth no trajo sector_id.
var errSectorIDRequerido = errors.New("sector_id es requerido para credenciales OAuth")

// uuidPattern valida ids de path/query antes de tocar columnas uuid.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isUUID indica si s es un UUID canónico.
func isUUID(s string) bool { return uuidPattern.MatchString(s) }

// LoteSectorService es el contrato que consume el handler.
type LoteSectorService interface {
	SectorDelDispositivo(ctx context.Context, deviceID string) (*loteSectorService.SectorInfo, error)
	Sectores(ctx context.Context) ([]sector.Sector, error)
	Abrir(ctx context.Context, deviceID, productoID, idempotencyKey string) (*lotesector.Lote, bool, error)
	Abierto(ctx context.Context, sectorID string) (*lotesector.Lote, error)
	RegistrarEventos(ctx context.Context, deviceID, loteID string, eventos []lotesector.Evento) (int, int, *lotesector.Lote, error)
	Cerrar(ctx context.Context, deviceID, loteID, motivo string, conteos lotesector.Conteos, idempotencyKey string) (*lotesector.Lote, error)
	Historial(ctx context.Context, sectorID, productoID string, limite int, cursor string) ([]lotesector.Lote, int, error)
}

// LoteSectorHandler maneja los endpoints de /api/v1/lotes y
// /api/v1/dispositivos/sector.
type LoteSectorHandler struct {
	service LoteSectorService
}

// NewLoteSectorHandler instancia el handler inyectando el servicio.
func NewLoteSectorHandler(svc LoteSectorService) *LoteSectorHandler {
	return &LoteSectorHandler{service: svc}
}

// inicioRequest es el payload de POST /api/v1/lotes/inicio. Momento es
// informativo: el servidor usa su propio reloj.
type inicioRequest struct {
	IdempotencyKey string     `json:"idempotency_key"`
	ProductoID     string     `json:"producto_id"`
	Momento        *time.Time `json:"momento"`
}

// eventoDTO es una detección reportada por la salida.
type eventoDTO struct {
	EventoID   string     `json:"evento_id"`
	ProductoID string     `json:"producto_id"`
	Estado     *string    `json:"estado"`
	Confianza  *float64   `json:"confianza"`
	Pista      *int       `json:"pista"`
	Frame      *int64     `json:"frame"`
	ModeloID   *string    `json:"modelo_id"`
	Momento    *time.Time `json:"momento"`
}

// eventosRequest es el payload de POST /api/v1/lotes/{id}/eventos.
type eventosRequest struct {
	Eventos []eventoDTO `json:"eventos"`
}

// conteosDTO son los buckets finales del cierre.
type conteosDTO struct {
	OK      *int `json:"ok"`
	Crudo   *int `json:"crudo"`
	Quemado *int `json:"quemado"`
	Total   int  `json:"total"`
}

// cierreRequest es el payload de POST /api/v1/lotes/{id}/cierre. Conteos es
// puntero: su ausencia debe rechazarse en vez de pisar los conteos vivos con
// NULL/0.
type cierreRequest struct {
	IdempotencyKey string      `json:"idempotency_key"`
	Motivo         string      `json:"motivo"`
	Conteos        *conteosDTO `json:"conteos"`
	Momento        *time.Time  `json:"momento"`
}

// historialResponse es el envelope paginado de GET /api/v1/lotes. Extiende el
// PaginatedResponse estándar con siguiente_cursor (omitempty) porque el
// contrato usa paginación por cursor opaco pero no reserva un campo para él.
type historialResponse struct {
	Success         bool              `json:"success"`
	Message         string            `json:"message,omitempty"`
	Data            []lotesector.Lote `json:"data"`
	Total           int               `json:"total"`
	Page            int               `json:"page"`
	PageSize        int               `json:"pageSize"`
	SiguienteCursor string            `json:"siguiente_cursor,omitempty"`
	Errors          any               `json:"errors"`
}

// HandleSector maneja GET /api/v1/dispositivos/sector (device-only).
func (h *LoteSectorHandler) HandleSector(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	principal, ok := deviceauth.PrincipalFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Token de dispositivo inválido", map[string]string{"code": "invalid_device_token"})
		return
	}

	info, err := h.service.SectorDelDispositivo(r.Context(), principal.DeviceID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, "Sector del dispositivo obtenido exitosamente", info)
}

// HandleSectores maneja GET /api/v1/sectores (cualquier usuario autenticado vía
// OAuth): devuelve el listado de sectores ordenado por nombre.
func (h *LoteSectorHandler) HandleSectores(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	sectores, err := h.service.Sectores(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, "Sectores obtenidos exitosamente", sectores)
}

// HandleInicio maneja POST /api/v1/lotes/inicio (device-only): abre o se
// adjunta al lote abierto del sector. No dispara consigna.
func (h *LoteSectorHandler) HandleInicio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	principal, ok := deviceauth.PrincipalFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Token de dispositivo inválido", map[string]string{"code": "invalid_device_token"})
		return
	}

	var req inicioRequest
	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
		return
	}

	lote, creado, err := h.service.Abrir(r.Context(), principal.DeviceID, req.ProductoID, req.IdempotencyKey)
	if err != nil {
		writeError(w, err)
		return
	}

	status := http.StatusOK
	message := "Lote abierto existente"
	if creado {
		status = http.StatusCreated
		message = "Lote creado exitosamente"
	}
	response.JSON(w, status, true, message, map[string]interface{}{
		"creado": creado,
		"lote":   lote,
	}, nil)
}

// HandleAbierto maneja GET /api/v1/lotes/abierto (device u OAuth). La ausencia
// de lote es 200 con lote:null, nunca 404.
func (h *LoteSectorHandler) HandleAbierto(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	sectorID, err := h.resolveSector(r)
	if err != nil {
		writeError(w, err)
		return
	}

	lote, err := h.service.Abierto(r.Context(), sectorID)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, "Lote abierto consultado", map[string]interface{}{"lote": lote})
}

// HandleEventos maneja POST /api/v1/lotes/{id}/eventos (device-only).
func (h *LoteSectorHandler) HandleEventos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	principal, ok := deviceauth.PrincipalFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Token de dispositivo inválido", map[string]string{"code": "invalid_device_token"})
		return
	}
	loteID := extractLoteID(r.URL.Path, "/eventos")
	if loteID == "" || !isUUID(loteID) {
		// §6/§7: un lote_id inexistente o no-UUID es 404, nunca 500 por 22P02.
		writeError(w, lotesector.ErrNotFound)
		return
	}

	var req eventosRequest
	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
		return
	}
	eventos := make([]lotesector.Evento, 0, len(req.Eventos))
	for _, e := range req.Eventos {
		eventos = append(eventos, lotesector.Evento{
			EventoID:   e.EventoID,
			ProductoID: e.ProductoID,
			Estado:     e.Estado,
			Confianza:  e.Confianza,
			Pista:      e.Pista,
			Frame:      e.Frame,
			ModeloID:   e.ModeloID,
			Momento:    e.Momento,
		})
	}

	aceptados, duplicados, lote, err := h.service.RegistrarEventos(r.Context(), principal.DeviceID, loteID, eventos)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, "Eventos procesados exitosamente", map[string]interface{}{
		"aceptados":  aceptados,
		"duplicados": duplicados,
		"lote":       lote,
	})
}

// HandleCierre maneja POST /api/v1/lotes/{id}/cierre (device-only). El cierre
// es idempotente: un reintento devuelve 200 con el mismo estado.
func (h *LoteSectorHandler) HandleCierre(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	principal, ok := deviceauth.PrincipalFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Token de dispositivo inválido", map[string]string{"code": "invalid_device_token"})
		return
	}
	loteID := extractLoteID(r.URL.Path, "/cierre")
	if loteID == "" || !isUUID(loteID) {
		writeError(w, lotesector.ErrNotFound)
		return
	}

	var req cierreRequest
	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
		return
	}
	// §7: conteos es el conteo final autoritativo; sin él no se pisa el lote.
	if req.Conteos == nil {
		writeError(w, lotesector.ErrPayloadInvalido)
		return
	}
	conteos := lotesector.Conteos{
		OK:      req.Conteos.OK,
		Crudo:   req.Conteos.Crudo,
		Quemado: req.Conteos.Quemado,
		Total:   req.Conteos.Total,
	}

	lote, err := h.service.Cerrar(r.Context(), principal.DeviceID, loteID, req.Motivo, conteos, req.IdempotencyKey)
	if err != nil {
		writeError(w, err)
		return
	}
	response.OK(w, "Lote cerrado exitosamente", map[string]interface{}{
		"cerrado": true,
		"lote":    lote,
	})
}

// HandleHistorial maneja GET /api/v1/lotes (device u OAuth). Con OAuth,
// sector_id es obligatorio.
func (h *LoteSectorHandler) HandleHistorial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	sectorID, err := h.resolveSector(r)
	if err != nil {
		writeError(w, err)
		return
	}

	productoID := strings.TrimSpace(r.URL.Query().Get("producto_id"))
	if productoID != "" && !isUUID(productoID) {
		// Filtro no-UUID contra columna uuid → 400, nunca 500 por 22P02.
		writeError(w, lotesector.ErrPayloadInvalido)
		return
	}
	limite := parseLimite(r.URL.Query().Get("limite"))
	cursor := strings.TrimSpace(r.URL.Query().Get("antes_de"))

	lotes, total, err := h.service.Historial(r.Context(), sectorID, productoID, limite, cursor)
	if err != nil {
		writeError(w, err)
		return
	}

	// El contrato no reserva un campo de cursor en la respuesta; se agrega
	// siguiente_cursor (omitempty) para permitir paginar hacia atrás.
	siguiente := ""
	if len(lotes) == limite && len(lotes) > 0 {
		last := lotes[len(lotes)-1]
		siguiente = loteSectorService.EncodeCursor(last.AbiertoEn, last.ID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(historialResponse{
		Success:         true,
		Message:         "Lotes obtenidos exitosamente",
		Data:            lotes,
		Total:           total,
		Page:            1,
		PageSize:        limite,
		SiguienteCursor: siguiente,
	})
}

// resolveSector resuelve el sector objetivo: para credencial de dispositivo lo
// deriva del principal; para OAuth exige el query param sector_id.
func (h *LoteSectorHandler) resolveSector(r *http.Request) (string, error) {
	if principal, ok := deviceauth.PrincipalFromContext(r.Context()); ok {
		info, err := h.service.SectorDelDispositivo(r.Context(), principal.DeviceID)
		if err != nil {
			return "", err
		}
		return info.SectorID, nil
	}

	claims := authController.GetClaimsFromContext(r.Context())
	if claims == nil {
		return "", errSectorIDRequerido
	}
	sectorID := strings.TrimSpace(r.URL.Query().Get("sector_id"))
	if sectorID == "" {
		return "", errSectorIDRequerido
	}
	return sectorID, nil
}

// extractLoteID obtiene el {id} de /api/v1/lotes/{id}/<suffix> decodificando el
// path manualmente: las rutas se registran en el subtree /api/v1/lotes/ y
// PathValue no está disponible.
func extractLoteID(path, suffix string) string {
	const prefix = "/api/v1/lotes/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	mid = strings.Trim(mid, "/")
	if mid == "" || strings.Contains(mid, "/") {
		return ""
	}
	return mid
}

// parseLimite lee el query param limite, aplicando default 20 y tope 100.
func parseLimite(raw string) int {
	limite := 20
	if raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limite = v
		}
	}
	if limite > 100 {
		limite = 100
	}
	return limite
}

// writeError mapea los errores centinela del dominio a códigos HTTP y códigos
// de negocio del contrato. Cualquier error desconocido es 500.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lotesector.ErrNotFound):
		response.Error(w, http.StatusNotFound, "Lote no encontrado", map[string]string{"code": "lote_no_encontrado"})
	case errors.Is(err, lotesector.ErrCerrado):
		response.Error(w, http.StatusConflict, "El lote ya está cerrado", map[string]string{"code": "lote_cerrado"})
	case errors.Is(err, lotesector.ErrAjeno):
		response.Error(w, http.StatusForbidden, "El lote pertenece a otro sector", map[string]string{"code": "lote_ajeno"})
	case errors.Is(err, lotesector.ErrProductoInconsistente):
		response.Error(w, http.StatusUnprocessableEntity, "El producto del evento no coincide con el lote", map[string]string{"code": "producto_inconsistente"})
	case errors.Is(err, lotesector.ErrDemasiadosEventos):
		response.Error(w, http.StatusRequestEntityTooLarge, "Demasiados eventos en el batch", map[string]string{"code": "demasiados_eventos"})
	case errors.Is(err, lotesector.ErrIdempotencyKeyConflicto):
		response.Error(w, http.StatusConflict, "La clave de idempotencia ya fue usada", map[string]string{"code": "idempotency_key_conflicto"})
	case errors.Is(err, lotesector.ErrPayloadInvalido):
		response.Error(w, http.StatusBadRequest, "Payload inválido", map[string]string{"code": "payload_invalido"})
	case errors.Is(err, lotesector.ErrSectorActivo):
		response.Error(w, http.StatusConflict, "El sector todavía está activo", map[string]string{"code": "sector_activo"})
	case errors.Is(err, producto.ErrDesconocido):
		response.Error(w, http.StatusUnprocessableEntity, "Producto desconocido", map[string]string{"code": "producto_desconocido"})
	case errors.Is(err, sector.ErrSinSector):
		response.Error(w, http.StatusNotFound, "El dispositivo no pertenece a un sector", map[string]string{"code": "sin_sector"})
	case errors.Is(err, errSectorIDRequerido):
		response.Error(w, http.StatusBadRequest, "sector_id es requerido", map[string]string{"code": "payload_invalido"})
	default:
		response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
	}
}
