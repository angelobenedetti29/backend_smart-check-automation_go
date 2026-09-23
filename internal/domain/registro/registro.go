// Package registro define el modelo de dominio del flujo de registro de nodos
// Raspberry: la solicitud que el nodo emite, su aprobación/rechazo por un
// Supervisor y la entrega única del secret de autenticación.
package registro

import (
	"context"
	"errors"
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

	// ErrHostnameDuplicate se retorna cuando ya existe una solicitud PENDING para
	// el mismo hostname (índice único parcial).
	ErrHostnameDuplicate = errors.New("ya existe una solicitud pendiente para este hostname")
)

// CreateRequest es el body del alta pública de un nodo (POST /api/v1/registration-requests).
type CreateRequest struct {
	Hostname string `json:"hostname"`
	Type     string `json:"type"`
}

// Validate normaliza (trim + upper) y valida el hostname y el type. El type
// debe ser un tipo de dispositivo canónico (ENTRADA_HORNO/SALIDA_HORNO).
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
	return nil
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
// entrega aprobada, device_id + secret.
type Pickup struct {
	Status   string  `json:"status"`
	DeviceID *string `json:"device_id,omitempty"`
	Secret   *string `json:"secret,omitempty"`
	Type     *string `json:"type,omitempty"`
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
	// de dispositivo dado.
	Issue(ctx context.Context, hostname, tipo, requestID string, expiresAt time.Time) (*IssueResponse, error)
	// ListPending devuelve las solicitudes PENDING vigentes.
	ListPending(ctx context.Context) ([]RegistrationRequest, error)
	// Approve aprueba una solicitud y crea el dispositivo en la misma transacción.
	Approve(ctx context.Context, actor, requestID, secret, secretHash string, pickupExpiresAt time.Time) (*Approval, error)
	// Reject rechaza una solicitud PENDING.
	Reject(ctx context.Context, actor, requestID string) error
	// Pickup entrega el secret una única vez.
	Pickup(ctx context.Context, requestID string) (*Pickup, error)
}
