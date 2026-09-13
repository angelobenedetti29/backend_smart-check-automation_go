package controller

import (
	"context"
	"errors"
	"net/http"
	"strings"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/deviceproof"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/requestjson"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

type enrollmentActions interface {
	Issue(context.Context, string, dispositivo.EnrollmentCreateRequest) (*dispositivo.EnrollmentInvitation, error)
	List(context.Context) ([]dispositivo.EnrollmentInvitation, error)
	Cancel(context.Context, string, string) error
	Redeem(context.Context, string, dispositivo.PublicJWK) (*dispositivo.DeviceIdentity, error)
	Recover(context.Context, dispositivo.PublicJWK) (*dispositivo.DeviceIdentity, error)
	Lifecycle(context.Context, string, string, string) (*dispositivo.DeviceRead, error)
	Reprovision(context.Context, string, string) (*dispositivo.EnrollmentInvitation, error)
	Reads(context.Context) ([]dispositivo.DeviceRead, error)
}

// EnrollmentHandler exposes human management and public redemption endpoints.
type EnrollmentHandler struct {
	svc       enrollmentActions
	telemetry func() []dispositivo.EstadoDispositivo
}

// NewEnrollmentHandler creates the enrollment controller and optionally wires
// the in-memory heartbeat projection used by lifecycle response reads.
func NewEnrollmentHandler(s enrollmentActions, telemetry ...func() []dispositivo.EstadoDispositivo) *EnrollmentHandler {
	h := &EnrollmentHandler{svc: s}
	if len(telemetry) > 0 {
		h.telemetry = telemetry[0]
	}
	return h
}

// HandleCollection handles POST/GET /dispositivos/enrollments.
func (h *EnrollmentHandler) HandleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		x, err := h.svc.List(r.Context())
		if err != nil {
			serverErr(w, err)
			return
		}
		noStore(w)
		response.OK(w, "Invitaciones pendientes obtenidas", x)
	case http.MethodPost:
		claims := authController.GetClaimsFromContext(r.Context())
		if claims == nil {
			response.Error(w, 401, "Sesión requerida", map[string]string{"code": "human_auth_required"})
			return
		}
		var req dispositivo.EnrollmentCreateRequest
		if !decodeJSON(w, r, 8192, &req) {
			return
		}
		if err := req.Validate(); err != nil {
			response.Error(w, 422, "Datos de invitación inválidos", map[string]string{"code": "validation_error"})
			return
		}
		x, err := h.svc.Issue(r.Context(), claims.Email, req)
		if err != nil {
			serverErr(w, err)
			return
		}
		noStore(w)
		response.JSON(w, 201, true, "Invitación creada", x, nil)
	default:
		response.Error(w, 405, "Método no permitido", nil)
	}
}

// HandleCancel handles POST /dispositivos/enrollments/{id}/cancel.
func (h *EnrollmentHandler) HandleCancel(w http.ResponseWriter, r *http.Request) {
	id := pathPart(r.URL.Path, "/api/v1/dispositivos/enrollments/", "/cancel")
	if id == "" {
		response.Error(w, 400, "Invitación inválida", map[string]string{"code": "invalid_enrollment_id"})
		return
	}
	if r.Method != http.MethodPost {
		response.Error(w, 405, "Método no permitido", nil)
		return
	}
	claims := authController.GetClaimsFromContext(r.Context())
	if claims == nil {
		response.Error(w, http.StatusUnauthorized, "Sesión requerida", nil)
		return
	}
	var empty dispositivo.LifecycleRequest
	if !decodeJSON(w, r, 8192, &empty) {
		return
	}
	err := h.svc.Cancel(r.Context(), claims.Email, id)
	if errors.Is(err, dispositivo.ErrEnrollmentConsumed) {
		response.Error(w, 409, "La invitación ya fue consumida", map[string]string{"code": "enrollment_consumed"})
		return
	}
	if err != nil {
		response.Error(w, 400, "La invitación no está disponible", map[string]string{"code": "enrollment_unavailable"})
		return
	}
	noStore(w)
	response.JSON(w, 200, true, "Invitación cancelada", map[string]interface{}{"enrollmentId": id, "status": "cancelled"}, nil)
}

// HandleLifecycle handles disable, enable and revoke actions.
func (h *EnrollmentHandler) HandleLifecycle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, 405, "Método no permitido", nil)
		return
	}
	claims := authController.GetClaimsFromContext(r.Context())
	if claims == nil {
		response.Error(w, 401, "Sesión requerida", nil)
		return
	}
	action := ""
	for _, a := range []string{"disable", "enable", "revoke"} {
		if strings.HasSuffix(r.URL.Path, "/"+a) {
			action = a
		}
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/dispositivos/"), "/"+action)
	var empty dispositivo.LifecycleRequest
	if !decodeJSON(w, r, 8192, &empty) {
		return
	}
	x, err := h.svc.Lifecycle(r.Context(), claims.Email, id, action)
	if errors.Is(err, dispositivo.ErrInvalidTransition) {
		response.Error(w, 409, "Transición de ciclo de vida inválida", map[string]string{"code": "invalid_lifecycle_transition"})
		return
	}
	if errors.Is(err, dispositivo.ErrDispositivoNotFound) {
		response.Error(w, 404, "El dispositivo no existe", map[string]string{"code": "device_not_found"})
		return
	}
	if err != nil {
		serverErr(w, err)
		return
	}
	if h.telemetry != nil {
		for _, state := range h.telemetry() {
			if state.DispositivoID == x.DispositivoID {
				x.Estado = state.Estado
				x.UltimaMetrica = state.UltimaMetrica
				x.LastSeen = state.LastSeen
				break
			}
		}
	}
	noStore(w)
	response.OK(w, "Estado de autenticación actualizado", x)
}

// HandleReprovision handles POST /dispositivos/{id}/reprovision.
func (h *EnrollmentHandler) HandleReprovision(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, 405, "Método no permitido", nil)
		return
	}
	claims := authController.GetClaimsFromContext(r.Context())
	if claims == nil {
		response.Error(w, 401, "Sesión requerida", nil)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/dispositivos/"), "/reprovision")
	var empty dispositivo.LifecycleRequest
	if !decodeJSON(w, r, 8192, &empty) {
		return
	}
	x, err := h.svc.Reprovision(r.Context(), claims.Email, id)
	if err != nil {
		serverErr(w, err)
		return
	}
	noStore(w)
	response.JSON(w, 201, true, "Reprovisionamiento creado", x, nil)
}

// HandleProvision handles the proof-authenticated code redemption endpoint.
func (h *EnrollmentHandler) HandleProvision(w http.ResponseWriter, r *http.Request) {
	input, ok := deviceproof.EnrollmentInputFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Prueba de dispositivo inválida", map[string]string{"code": "invalid_device_proof"})
		return
	}
	x, err := h.svc.Redeem(r.Context(), input.Code, input.PublicKey)
	if errors.Is(err, dispositivo.ErrEnrollmentUnavailable) {
		response.Error(w, 400, "La invitación no está disponible", map[string]string{"code": "enrollment_unavailable"})
		return
	}
	if errors.Is(err, dispositivo.ErrCredentialUsed) {
		response.Error(w, 409, "La credencial ya fue utilizada", map[string]string{"code": "credential_used"})
		return
	}
	if err != nil {
		serverErr(w, err)
		return
	}
	noStore(w)
	response.JSON(w, 201, true, "Dispositivo aprovisionado", x, nil)
}

// HandleRecover handles proof-only recovery.
func (h *EnrollmentHandler) HandleRecover(w http.ResponseWriter, r *http.Request) {
	input, ok := deviceproof.EnrollmentInputFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Prueba de dispositivo inválida", map[string]string{"code": "invalid_device_proof"})
		return
	}
	x, err := h.svc.Recover(r.Context(), input.PublicKey)
	if errors.Is(err, dispositivo.ErrCredentialRevoked) {
		response.Error(w, 409, "La credencial fue revocada", map[string]string{"code": "credential_revoked"})
		return
	}
	if errors.Is(err, dispositivo.ErrEnrollmentUnavailable) {
		response.Error(w, 404, "Identidad no encontrada", map[string]string{"code": "enrollment_not_found"})
		return
	}
	if err != nil {
		serverErr(w, err)
		return
	}
	noStore(w)
	response.OK(w, "Identidad recuperada", x)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, dst interface{}) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		response.Error(w, 415, "Content-Type debe ser application/json", nil)
		return false
	}
	return requestjson.Decode(w, r, limit, dst)
}
func pathPart(path, prefix, suffix string) string {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	x := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if strings.Contains(x, "/") || x == "" {
		return ""
	}
	return x
}
func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }
func serverErr(w http.ResponseWriter, err error) {
	response.Error(w, 500, "Error interno del servidor", map[string]string{"code": "internal_error"})
}
