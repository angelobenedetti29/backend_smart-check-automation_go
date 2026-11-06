// Package notificacion define el dominio de notificaciones de alertas:
// preferencias por usuario, destinatarios, outbox y los contratos de envío.
package notificacion

import (
	"context"
	"errors"
	"time"
)

// Canal es el medio de entrega de una notificación.
type Canal string

// Canales de entrega soportados.
const (
	CanalEmail    Canal = "EMAIL"
	CanalTelegram Canal = "TELEGRAM"
)

// Estado es el ciclo de vida de una fila del outbox.
type Estado string

// Estados del outbox.
const (
	EstadoPendiente Estado = "PENDIENTE"
	EstadoEnviado   Estado = "ENVIADO"
	EstadoFallido   Estado = "FALLIDO"
)

// Errores de dominio de notificaciones.
var (
	ErrDestinatarioNoEncontrado = errors.New("usuario destinatario no encontrado")
	ErrCanalNoConfigurado       = errors.New("canal de notificación no configurado")
	ErrSinDestino               = errors.New("el usuario no tiene destino para el canal")
	ErrChatIDInvalido           = errors.New("chat ID de Telegram inválido")
)

// Preferencias es el estado persistido de preferencias de un usuario.
type Preferencias struct {
	UsuarioID         string
	TelegramChatID    *string
	NotificarEmail    bool
	NotificarTelegram bool
}

// PreferenciasPatch representa un PATCH parcial: nil = no cambiar.
// TelegramChatID apuntando a "" limpia el chat ID.
type PreferenciasPatch struct {
	TelegramChatID    *string
	NotificarEmail    *bool
	NotificarTelegram *bool
}

// Destinatario es un usuario con sus preferencias resueltas para notificar.
type Destinatario struct {
	UsuarioID         string  `json:"usuario_id"`
	Email             string  `json:"email"`
	Nombre            string  `json:"nombre"`
	Rol               string  `json:"rol"`
	Activo            bool    `json:"activo"`
	TelegramChatID    *string `json:"telegram_chat_id"`
	NotificarEmail    bool    `json:"notificar_email"`
	NotificarTelegram bool    `json:"notificar_telegram"`
}

// Notificacion es una fila del outbox pendiente o ya procesada.
type Notificacion struct {
	ID               string
	AlertaID         string
	UsuarioID        string
	Canal            Canal
	Destino          string
	Asunto           string
	Cuerpo           string
	CuerpoHTML       string
	Estado           Estado
	Intentos         int
	ProximoIntentoAt time.Time
	UltimoError      string
	CreadaEn         time.Time
	EnviadaEn        *time.Time
}

// Repository es el contrato de persistencia de notificaciones.
type Repository interface {
	ListarDestinatarios(ctx context.Context) ([]Destinatario, error)
	ObtenerDestinatario(ctx context.Context, usuarioID string) (*Destinatario, error)
	GuardarPreferencias(ctx context.Context, p Preferencias) (*Destinatario, error)
	Encolar(ctx context.Context, notificaciones []Notificacion) error
	Pendientes(ctx context.Context, limite int) ([]Notificacion, error)
	MarcarEnviada(ctx context.Context, id string) error
	MarcarFallida(ctx context.Context, id, ultimoError string, proximo time.Time, agotada bool) error
}

// EnviadorEmail entrega un email. Implementado por los clientes de Resend y
// Gmail API. `html` puede ser vacío (se envía sólo texto).
type EnviadorEmail interface {
	Enviar(ctx context.Context, destino, asunto, texto, html string) error
}

// EnviadorTelegram entrega un mensaje de Telegram. Implementado por Bot API.
type EnviadorTelegram interface {
	Enviar(ctx context.Context, chatID, texto string) error
}
