// Package registro define el modelo de dominio del flujo de registro de nodos
// Raspberry: la solicitud que el nodo emite, su aprobación/rechazo por un
// Supervisor y la entrega única del secret de autenticación.
package registro

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
)

// Estados de una solicitud de registro. Usar estas constantes en lugar de
// strings literales para evitar typos y mantener consistencia con el CHECK de
// registration_requests.
const (
	StatusPending  = "PENDING"
	StatusApproved = "APPROVED"
	StatusRejected = "REJECTED"
	StatusExpired  = "EXPIRED"
)

// Errores centinela — comparar con errors.Is() en el service y el handler.
var (
	// ErrRequestNotFound se retorna cuando el request_id no existe o es inválido.
	ErrRequestNotFound = errors.New("solicitud de registro no encontrada")

	// ErrRequestNotPending se retorna al aprobar/rechazar una solicitud ya resuelta.
	ErrRequestNotPending = errors.New("la solicitud de registro ya fue resuelta")

	// ErrRequestExpired se retorna al aprobar/rechazar una solicitud vencida.
	ErrRequestExpired = errors.New("la solicitud de registro expiró")
)

// CreateRequest es el body del alta pública de un nodo (POST /api/v1/registration-requests).
// RenameURL es opcional: una Pi con firmware legacy lo omite y el backend
// simplemente no podrá empujarle el renombre (se ignora el push).
type CreateRequest struct {
	Hostname  string  `json:"hostname"`
	Type      string  `json:"type"`
	RenameURL *string `json:"rename_url,omitempty"`
}

// Validate normaliza (trim + upper) y valida el hostname y el type. El type
// debe ser un tipo de dispositivo canónico (ENTRADA_HORNO/SALIDA_HORNO). La
// rename_url opcional, si viene, debe ser una URL absoluta válida.
func (c *CreateRequest) Validate() error {
	c.Hostname = strings.TrimSpace(c.Hostname)
	if c.Hostname == "" {
		return errors.New("hostname es requerido")
	}
	if utf8.RuneCountInString(c.Hostname) > 100 {
		return errors.New("hostname no puede superar los 100 caracteres")
	}
	for _, r := range c.Hostname {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("hostname no puede contener espacios ni caracteres de control")
		}
	}

	tipo, ok := dispositivo.ParseTipoDispositivo(c.Type)
	if !ok {
		return errors.New("type: debe ser ENTRADA_HORNO o SALIDA_HORNO")
	}
	c.Type = string(tipo)

	renameURL, err := validateRenameURL(c.RenameURL)
	if err != nil {
		return err
	}
	c.RenameURL = renameURL
	return nil
}

// validateRenameURL valida la URL opcional con la que el nodo se anuncia para el
// push de renombre. nil o vacío (tras el trim) significa "no configurada" y se
// normaliza a nil. Si viene con valor, debe parsear como URL absoluta con
// esquema y host (p. ej. http://localhost:8090 o https://mi-tunel.trycloudflare.com).
func validateRenameURL(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("rename_url: debe ser una URL absoluta con esquema y host (ej: http://host:puerto)")
	}
	return &trimmed, nil
}

// IssueResponse es la respuesta plana del alta: request_id + estado.
type IssueResponse struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

// Approval es el resultado de aprobar una solicitud (nunca incluye el secret).
type Approval struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	DeviceID  string `json:"device_id"`
}

// Pickup es la respuesta plana de la consulta del nodo: estado y, en la primera
// entrega aprobada, device_id + secret + rename_token. El rename_token se
// entrega una sola vez (junto con el secret), igual que el secret.
type Pickup struct {
	Status      string  `json:"status"`
	DeviceID    *string `json:"device_id,omitempty"`
	Secret      *string `json:"secret,omitempty"`
	RenameToken *string `json:"rename_token,omitempty"`
	Type        *string `json:"type,omitempty"`
}

// RegistrationRequest es la vista de una solicitud para el panel de supervisión.
type RegistrationRequest struct {
	RequestID string    `json:"requestId"`
	Hostname  string    `json:"hostname"`
	Type      *string   `json:"type,omitempty"`
	Status    string    `json:"status"`
	DeviceID  *string   `json:"deviceId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Repository define el contrato de persistencia para solicitudes de registro.
// La implementación real vive en internal/repository/.
type Repository interface {
	// Issue crea (o recupera) la solicitud PENDING de un hostname para un tipo
	// de dispositivo dado, guardando además la URL opcional que el nodo anuncia
	// para el push de renombre.
	Issue(ctx context.Context, hostname, tipo, requestID string, expiresAt time.Time, renameURL *string) (*IssueResponse, error)
	// ListPending devuelve las solicitudes PENDING vigentes.
	ListPending(ctx context.Context) ([]RegistrationRequest, error)
	// Approve aprueba una solicitud y crea el dispositivo en la misma transacción.
	Approve(ctx context.Context, actor, requestID, secret, secretHash string, pickupExpiresAt time.Time) (*Approval, error)
	// Reject rechaza una solicitud PENDING.
	Reject(ctx context.Context, actor, requestID string) error
	// Pickup entrega el secret una única vez.
	Pickup(ctx context.Context, requestID string) (*Pickup, error)
}
