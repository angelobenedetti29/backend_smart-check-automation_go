package notificacion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const resendDefaultBaseURL = "https://api.resend.com"

// Resend es el cliente HTTP mínimo de la API de Resend.
type Resend struct {
	apiKey  string
	from    string
	baseURL string
	http    *http.Client
}

// NewResend construye el cliente. Devuelve nil si falta la API key o el remitente.
func NewResend(apiKey, from string) *Resend {
	if apiKey == "" || from == "" {
		return nil
	}
	return &Resend{
		apiKey:  apiKey,
		from:    from,
		baseURL: resendDefaultBaseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Enviar entrega un email de texto plano (y HTML opcional) vía POST /emails.
func (r *Resend) Enviar(ctx context.Context, destino, asunto, cuerpo, html string) error {
	payloadMap := map[string]any{
		"from":    r.from,
		"to":      []string{destino},
		"subject": asunto,
		"text":    cuerpo,
	}
	if html != "" {
		payloadMap["html"] = html
	}
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		return fmt.Errorf("marshal resend payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/emails", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("crear request resend: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.http.Do(req)
	if err != nil {
		return fmt.Errorf("resend http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("resend status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
