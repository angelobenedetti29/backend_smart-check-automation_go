package notificacion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const telegramDefaultBaseURL = "https://api.telegram.org"

// Telegram es el cliente HTTP mínimo de la Bot API.
type Telegram struct {
	token   string
	baseURL string
	http    *http.Client
}

// NewTelegram construye el cliente. Devuelve nil si no hay token.
func NewTelegram(token string) *Telegram {
	if token == "" {
		return nil
	}
	return &Telegram{
		token:   token,
		baseURL: telegramDefaultBaseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Enviar entrega un mensaje de texto vía sendMessage.
//
// Los errores sólo incluyen el status HTTP: ni el token ni la URL deben quedar
// en logs ni en el outbox.
func (t *Telegram) Enviar(ctx context.Context, chatID, texto string) error {
	payload, err := json.Marshal(map[string]any{
		"chat_id":                  chatID,
		"text":                     texto,
		"disable_web_page_preview": true,
	})
	if err != nil {
		return fmt.Errorf("marshal telegram payload: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", t.baseURL, t.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		// No envolver el error: puede incluir la URL con el token.
		return fmt.Errorf("crear request telegram: configuración inválida")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.http.Do(req)
	if err != nil {
		// http.Client.Do puede devolver un *url.Error con la URL (y el token).
		return fmt.Errorf("telegram: no se pudo conectar")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram status %d", resp.StatusCode)
	}
	return nil
}
