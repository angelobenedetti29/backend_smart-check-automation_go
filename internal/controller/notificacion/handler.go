// Package controller expone los endpoints admin de notificaciones:
//
//	GET   /api/v1/admin/notificaciones
//	PATCH /api/v1/admin/notificaciones/{usuarioID}
//	POST  /api/v1/admin/notificaciones/test
package controller

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/controller/requestjson"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/notificacion"
	notificacionService "github.com/angelobenedetti29/smart-check-automation/internal/service/notificacion"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

const notificacionRequestTimeout = 15 * time.Second

const notificacionPathPrefix = "/api/v1/admin/notificaciones/"

// Service es el contrato que consume el handler (el service real o un fake en tests).
type Service interface {
	ListarDestinatarios(ctx context.Context) ([]notificacion.Destinatario, error)
	ActualizarPreferencias(ctx context.Context, usuarioID string, patch notificacion.PreferenciasPatch) (*notificacion.Destinatario, error)
	Probar(ctx context.Context, usuarioID string, canal notificacion.Canal) error
	ProbarAlerta(ctx context.Context, usuarioID string, canal notificacion.Canal, tipo string) error
	Configuracion() notificacionService.Configuracion
}

// Handler maneja la configuración y las pruebas de notificaciones.
type Handler struct {
	svc Service
}

// NewHandler instancia el handler con el service de notificaciones.
func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

// HandleColeccion maneja GET /api/v1/admin/notificaciones.
func (h *Handler) HandleColeccion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), notificacionRequestTimeout)
	defer cancel()

	destinatarios, err := h.svc.ListarDestinatarios(ctx)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "Error al obtener destinatarios", nil)
		return
	}
	if destinatarios == nil {
		destinatarios = []notificacion.Destinatario{}
	}

	cfg := h.svc.Configuracion()
	data := map[string]any{
		"canales": map[string]any{
			"email": map[string]any{
				"configurado": cfg.EmailConfigurado,
				"remitente":   cfg.Remitente,
			},
			"telegram": map[string]any{
				"configurado": cfg.TelegramConfigurado,
			},
		},
		"destinatarios": destinatarios,
	}
	response.OK(w, "Configuración de notificaciones obtenida", data)
}

type preferenciasPayload struct {
	TelegramChatID    *string `json:"telegram_chat_id"`
	NotificarEmail    *bool   `json:"notificar_email"`
	NotificarTelegram *bool   `json:"notificar_telegram"`
}

// HandleUsuario maneja PATCH /api/v1/admin/notificaciones/{usuarioID}.
func (h *Handler) HandleUsuario(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	usuarioID := strings.TrimPrefix(r.URL.Path, notificacionPathPrefix)
	usuarioID = strings.Trim(usuarioID, "/")
	if usuarioID == "" || strings.Contains(usuarioID, "/") {
		response.Error(w, http.StatusNotFound, "Usuario no encontrado", nil)
		return
	}

	if !requestjson.RequireJSON(w, r) {
		return
	}
	var payload preferenciasPayload
	if !requestjson.Decode(w, r, requestjson.DefaultLimit, &payload) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), notificacionRequestTimeout)
	defer cancel()

	destinatario, err := h.svc.ActualizarPreferencias(ctx, usuarioID, notificacion.PreferenciasPatch{
		TelegramChatID:    payload.TelegramChatID,
		NotificarEmail:    payload.NotificarEmail,
		NotificarTelegram: payload.NotificarTelegram,
	})
	if err != nil {
		h.escribirError(w, err)
		return
	}
	response.OK(w, "Preferencias actualizadas", destinatario)
}

type probarPayload struct {
	Canal     string `json:"canal"`
	UsuarioID string `json:"usuario_id"`
	// Tipo es opcional: si viene, se envía el mensaje real de ese tipo de alerta
	// con datos de ejemplo; si no, el mensaje de prueba genérico.
	Tipo string `json:"tipo"`
}

// HandleTest maneja POST /api/v1/admin/notificaciones/test.
func (h *Handler) HandleTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	if !requestjson.RequireJSON(w, r) {
		return
	}
	var payload probarPayload
	if !requestjson.Decode(w, r, requestjson.DefaultLimit, &payload) {
		return
	}

	canal := notificacion.Canal(strings.ToUpper(strings.TrimSpace(payload.Canal)))
	if canal != notificacion.CanalEmail && canal != notificacion.CanalTelegram {
		response.Error(w, http.StatusBadRequest, "Canal inválido: use EMAIL o TELEGRAM", nil)
		return
	}
	usuarioID := strings.TrimSpace(payload.UsuarioID)
	if usuarioID == "" {
		response.Error(w, http.StatusBadRequest, "usuario_id es requerido", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), notificacionRequestTimeout)
	defer cancel()

	tipo := strings.ToUpper(strings.TrimSpace(payload.Tipo))
	var err error
	if tipo != "" {
		err = h.svc.ProbarAlerta(ctx, usuarioID, canal, tipo)
	} else {
		err = h.svc.Probar(ctx, usuarioID, canal)
	}
	if err != nil {
		// El detalle queda en el log del servidor; al cliente sólo va el mensaje
		// genérico para no exponer configuración del proveedor.
		log.Printf("[NOTIFICACIONES] Prueba fallida usuario=%s canal=%s tipo=%s: %v", usuarioID, canal, tipo, err)
		h.escribirError(w, err)
		return
	}
	response.OK(w, "Notificación de prueba enviada", map[string]any{"canal": canal, "tipo": tipo})
}

// escribirError traduce los errores del service a la respuesta HTTP.
func (h *Handler) escribirError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notificacion.ErrDestinatarioNoEncontrado):
		response.Error(w, http.StatusNotFound, "Usuario no encontrado", nil)
	case errors.Is(err, notificacion.ErrCanalNoConfigurado):
		response.Error(w, http.StatusConflict, "Canal no configurado",
			map[string]string{"code": "canal_no_configurado"})
	case errors.Is(err, notificacion.ErrSinDestino):
		response.Error(w, http.StatusBadRequest, "El usuario no tiene destino para el canal",
			map[string]string{"code": "sin_destino"})
	case errors.Is(err, notificacion.ErrTipoAlertaInvalido):
		response.Error(w, http.StatusBadRequest, "Tipo de alerta inválido: use "+
			strings.Join(notificacionService.TiposAlertaPrueba(), ", "), nil)
	case errors.Is(err, notificacion.ErrChatIDInvalido):
		response.Error(w, http.StatusBadRequest, notificacion.ErrChatIDInvalido.Error(), nil)
	default:
		// No se expone el error del proveedor: puede contener detalles internos.
		response.Error(w, http.StatusBadGateway, "No se pudo enviar la notificación", nil)
	}
}
