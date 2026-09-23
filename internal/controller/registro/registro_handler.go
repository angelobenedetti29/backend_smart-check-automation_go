// Package controller expone los endpoints HTTP del flujo de registro de nodos:
// alta pública, listado/panel, aprobación/rechazo y consulta (pickup) pública.
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/requestjson"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/registro"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// registrationRequestPrefix es el prefijo constante de las rutas de solicitudes.
const registrationRequestPrefix = "/api/v1/registration-requests/"

// maxRegistrationBodyBytes limita el body del alta de un nodo a 8 KB.
const maxRegistrationBodyBytes = 8192

// registry es la superficie del service que consume el handler. Definida como
// interfaz privada para permitir dobles de prueba en los tests.
type registry interface {
	Issue(ctx context.Context, req registro.CreateRequest) (*registro.IssueResponse, error)
	List(ctx context.Context) ([]registro.RegistrationRequest, error)
	Approve(ctx context.Context, actorEmail, requestID string) (*registro.Approval, error)
	Reject(ctx context.Context, actorEmail, requestID string) error
	Pickup(ctx context.Context, requestID string) (*registro.Pickup, error)
}

// Handler maneja los endpoints del flujo de registro de dispositivos.
type Handler struct {
	svc registry
}

// NewHandler instancia el handler inyectando el servicio de registro.
func NewHandler(svc registry) *Handler {
	return &Handler{svc: svc}
}

// HandleCreate procesa POST /api/v1/registration-requests (público, JSON plano).
func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeFlat(w, http.StatusMethodNotAllowed, map[string]string{"error": "Método no permitido"})
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		writeFlat(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type debe ser application/json"})
		return
	}

	var req registro.CreateRequest
	if !requestjson.Decode(w, r, maxRegistrationBodyBytes, &req) {
		return
	}
	if err := req.Validate(); err != nil {
		log.Printf("[AUDIT] Validación de solicitud de registro fallida: %v", err)
		writeFlat(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	res, err := h.svc.Issue(r.Context(), req)
	if err != nil {
		if errors.Is(err, registro.ErrHostnameDuplicate) {
			writeFlat(w, http.StatusConflict, map[string]string{"error": "Ya existe una solicitud pendiente para este hostname"})
			return
		}
		log.Printf("[ERROR] Error al emitir solicitud de registro: %v", err)
		writeFlat(w, http.StatusInternalServerError, map[string]string{"error": "Error interno del servidor"})
		return
	}
	writeFlat(w, http.StatusCreated, res)
}

// HandleList procesa GET /api/v1/registration-requests (panel, envelope estándar).
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	list, err := h.svc.List(r.Context())
	if err != nil {
		log.Printf("[ERROR] Error al listar solicitudes de registro: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al obtener las solicitudes de registro", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response.OK(w, "Solicitudes pendientes obtenidas", list)
}

// HandlePickup procesa GET /api/v1/registration-requests/{id} (público, JSON plano).
func (h *Handler) HandlePickup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeFlat(w, http.StatusMethodNotAllowed, map[string]string{"error": "Método no permitido"})
		return
	}

	id := requestIDFromPath(r.URL.Path, "")
	if id == "" {
		writeFlat(w, http.StatusNotFound, map[string]string{"error": "Solicitud de registro no encontrada"})
		return
	}

	pickup, err := h.svc.Pickup(r.Context(), id)
	if err != nil {
		if errors.Is(err, registro.ErrRequestNotFound) {
			writeFlat(w, http.StatusNotFound, map[string]string{"error": "Solicitud de registro no encontrada"})
			return
		}
		log.Printf("[ERROR] Error en pickup de registro: %v", err)
		writeFlat(w, http.StatusInternalServerError, map[string]string{"error": "Error interno del servidor"})
		return
	}
	writeFlat(w, http.StatusOK, pickup)
}

// HandleApprove procesa POST /api/v1/registration-requests/{id}/approve (panel).
func (h *Handler) HandleApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	id := requestIDFromPath(r.URL.Path, "/approve")
	if id == "" {
		response.Error(w, http.StatusBadRequest, "Solicitud de registro inválida", nil)
		return
	}
	claims := authController.GetClaimsFromContext(r.Context())
	if claims == nil {
		response.Error(w, http.StatusUnauthorized, "Sesión requerida", nil)
		return
	}

	approval, err := h.svc.Approve(r.Context(), claims.Email, id)
	if err != nil {
		h.writeResolutionError(w, err, "aprobar")
		return
	}
	response.OK(w, "Solicitud de registro aprobada", approval)
}

// HandleReject procesa POST /api/v1/registration-requests/{id}/reject (panel).
func (h *Handler) HandleReject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	id := requestIDFromPath(r.URL.Path, "/reject")
	if id == "" {
		response.Error(w, http.StatusBadRequest, "Solicitud de registro inválida", nil)
		return
	}
	claims := authController.GetClaimsFromContext(r.Context())
	if claims == nil {
		response.Error(w, http.StatusUnauthorized, "Sesión requerida", nil)
		return
	}

	if err := h.svc.Reject(r.Context(), claims.Email, id); err != nil {
		h.writeResolutionError(w, err, "rechazar")
		return
	}
	response.OK(w, "Solicitud de registro rechazada", map[string]string{"requestId": id, "status": registro.StatusRejected})
}

// writeResolutionError traduce los errores de aprobación/rechazo al envelope.
func (h *Handler) writeResolutionError(w http.ResponseWriter, err error, accion string) {
	switch {
	case errors.Is(err, registro.ErrRequestNotFound):
		response.Error(w, http.StatusNotFound, "Solicitud de registro no encontrada", nil)
	case errors.Is(err, registro.ErrRequestNotPending):
		response.Error(w, http.StatusConflict, "La solicitud de registro ya fue resuelta", nil)
	case errors.Is(err, registro.ErrRequestExpired):
		response.Error(w, http.StatusGone, "La solicitud de registro expiró", nil)
	default:
		log.Printf("[ERROR] Error al %s solicitud de registro: %v", accion, err)
		response.Error(w, http.StatusInternalServerError, "Error al resolver la solicitud de registro", nil)
	}
}

// writeFlat escribe una respuesta JSON plana, sin envelope, siempre no-store.
func writeFlat(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// requestIDFromPath extrae el request_id de una ruta con prefijo constante y
// sufijo opcional ("", "/approve", "/reject"). Rechaza segmentos vacíos o con
// barras extra.
func requestIDFromPath(path, suffix string) string {
	if !strings.HasPrefix(path, registrationRequestPrefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, registrationRequestPrefix)
	if suffix != "" {
		if !strings.HasSuffix(rest, suffix) {
			return ""
		}
		rest = strings.TrimSuffix(rest, suffix)
	}
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}
