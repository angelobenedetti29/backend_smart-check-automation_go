package controller

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/angelobenedetti29/smart-check-automation/internal/controller/deviceproof"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/requestjson"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote"
	loteProductivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_productivo"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// maxRequestBodyBytes limita el tamaño del body a 1 MB para evitar DoS por memoria.
const maxRequestBodyBytes = 1 << 20 // 1 MB

// LoteFetcher allows retrieving a full LoteProductivo (with JOIN) by ID.
type LoteFetcher interface {
	GetByID(id string) (*loteProductivo.LoteProductivo, error)
}

// LoteHandler handles HTTP requests for productive batch operations.
type LoteHandler struct {
	repo        lote.Repository
	broker      *sse.Broker
	fetcher     LoteFetcher
	consignaSvc consigna.Service
}

// NewLoteHandler creates a new LoteHandler with the given repository, SSE broker,
// fetcher, and consigna service (usado por el inicio automático de lote, SCA-142).
func NewLoteHandler(repo lote.Repository, broker *sse.Broker, fetcher LoteFetcher, consignaSvc consigna.Service) *LoteHandler {
	return &LoteHandler{repo: repo, broker: broker, fetcher: fetcher, consignaSvc: consignaSvc}
}

// newCorrelationID genera un UUID v4 usado como identificador de correlación
// para consignas automáticas disparadas antes de que el lote se persista en
// lotes_productivos (ver HandleIniciarLote).
func newCorrelationID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// HandleCreateLote processes POST /api/v1/lotes.
// It validates the API key, decodes the JSON body, applies domain business rules,
// and persists the batch via the repository.
func (h *LoteHandler) HandleCreateLote(w http.ResponseWriter, r *http.Request) {
	// 0. Method check
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	// 1. Validate Content-Type
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return
	}

	// 2. The device-proof middleware has installed the trusted principal.
	if principal, ok := deviceproof.PrincipalFromContext(r.Context()); !ok || principal.Enrollment {
		response.Error(w, http.StatusUnauthorized, "Prueba de dispositivo inválida", map[string]string{"code": "invalid_device_proof"})
		return
	}

	// 3. Decode JSON body into LoteRequest (strict: bounded, single object,
	// no duplicate members or case aliases)
	var req lote.LoteRequest
	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
		return
	}

	// 5. Apply domain validation rules (all fields, business constraints)
	if err := req.Validate(); err != nil {
		log.Printf("[AUDIT] Validación de lote fallida: %v", err)
		response.Error(w, http.StatusUnprocessableEntity, "Datos del lote inválidos", err.Error())
		return
	}

	// 6. Map request to domain entity
	domainLote := lote.MapLoteRequestToLote(req)
	if principal, ok := deviceproof.PrincipalFromContext(r.Context()); ok {
		domainLote.DispositivoID = principal.DeviceID
	}

	// 7. Persist via repository
	if err := h.repo.Create(r.Context(), &domainLote); err != nil {
		log.Printf("[ERROR] Error al insertar lote en la base de datos: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al guardar el lote en la base de datos", nil)
		return
	}

	// 8. Broadcast SSE event (fire-and-forget: if broadcast fails, POST still returns 201)
	go h.broadcastCreation(domainLote.ID)

	// 10. Respond 201 Created
	response.JSON(w, http.StatusCreated, true, "Lote creado exitosamente", domainLote, nil)
}

// InicioLoteRequest es el payload JSON entrante de POST /api/v1/lotes/inicio:
// el nodo Raspberry Pi lo envía apenas la IA identifica y valida la variedad
// de producto que ingresa a la línea, antes de que el lote termine.
type InicioLoteRequest struct {
	HornoID    string `json:"hornoId"`
	ProductoID string `json:"productoId"`
}

// HandleIniciarLote processes POST /api/v1/lotes/inicio (SCA-142).
// Es el punto de disparo del envío automático de consigna: cuando la IA del
// nodo de entrada identifica la variedad de producto, este endpoint despacha
// automáticamente los valores objetivo de temperatura y velocidad al
// controlador físico del horno, sin intervención del operario. No crea una
// fila en lotes_productivos (eso ocurre después, vía POST /api/v1/lotes,
// cuando el lote termina) — sólo genera un ID de correlación para auditoría.
func (h *LoteHandler) HandleIniciarLote(w http.ResponseWriter, r *http.Request) {
	// 0. Method check
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	// 1. Validate Content-Type
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return
	}

	// 2. The device-proof middleware has installed the trusted principal.
	if principal, ok := deviceproof.PrincipalFromContext(r.Context()); !ok || principal.Enrollment {
		response.Error(w, http.StatusUnauthorized, "Prueba de dispositivo inválida", map[string]string{"code": "invalid_device_proof"})
		return
	}

	// 3. Decode JSON body (strict: bounded, single object, no duplicate
	// members or case aliases)
	var req InicioLoteRequest
	if !requestjson.Decode(w, r, maxRequestBodyBytes, &req) {
		return
	}
	if req.HornoID == "" || req.ProductoID == "" {
		response.Error(w, http.StatusUnprocessableEntity, "Datos de inicio de lote inválidos", "hornoId y productoId son requeridos")
		return
	}

	// 5. Generar ID de correlación (el lote real todavía no existe en lotes_productivos)
	loteID, err := newCorrelationID()
	if err != nil {
		log.Printf("[ERROR] Error al generar ID de correlación de lote: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error interno al iniciar el lote", nil)
		return
	}

	// 6. Despachar consigna automática al horno
	rec, err := h.consignaSvc.DispatchAutomatico(r.Context(), req.HornoID, loteID, req.ProductoID)
	if err != nil {
		switch {
		case errors.Is(err, deviceauth.ErrInvalidProof):
			response.Error(w, http.StatusUnauthorized, "Prueba de dispositivo inválida", map[string]string{"code": "invalid_device_proof"})
		case errors.Is(err, consigna.ErrHornoNoExiste):
			response.Error(w, http.StatusNotFound, "El horno indicado no existe", nil)
		case errors.Is(err, consigna.ErrParametrosNoExiste):
			response.Error(w, http.StatusUnprocessableEntity, "El producto no tiene setpoints de cocción cargados", nil)
		case errors.Is(err, consigna.ErrHornoEnControlManual):
			response.Error(w, http.StatusConflict, "El horno está en modo CONTROL_MANUAL: requiere intervención de un operario (envío manual) antes de reanudar el control automático", nil)
		case errors.Is(err, consigna.ErrDispatchFallido):
			log.Printf("[AUDIT] Consigna automática rechazada por el controlador físico: horno=%s lote=%s producto=%s", req.HornoID, loteID, req.ProductoID)
			response.JSON(w, http.StatusBadGateway, false, "El controlador físico del horno rechazó la consigna", rec, nil)
		default:
			log.Printf("[ERROR] Error al despachar consigna automática: %v", err)
			response.Error(w, http.StatusInternalServerError, "Error al despachar la consigna al horno", nil)
		}
		return
	}

	// 7. Respond 200 OK con el resultado del despacho
	response.JSON(w, http.StatusOK, true, "Lote iniciado: consigna despachada al horno", map[string]interface{}{
		"loteId":   loteID,
		"consigna": rec,
	}, nil)
}

// broadcastCreation fetches the full lot info and broadcasts it via SSE.
// It runs in a separate goroutine so failures don't affect the HTTP response.
func (h *LoteHandler) broadcastCreation(loteID string) {
	loteProductivo, err := h.fetcher.GetByID(loteID)
	if err != nil {
		log.Printf("[SSE] Error al obtener lote %s para broadcast: %v", loteID, err)
		return
	}

	payload := map[string]interface{}{
		"success": true,
		"message": "Lote creado exitosamente",
		"data":    loteProductivo,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[SSE] Error al serializar payload para broadcast: %v", err)
		return
	}

	h.broker.Broadcast("lote.created", jsonData)
	log.Printf("[SSE] Broadcast lote.created para lote %s", loteID)
}
