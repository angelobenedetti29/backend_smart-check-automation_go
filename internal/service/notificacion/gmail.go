package notificacion

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

const gmailTimeout = 10 * time.Second

// Gmail envía emails con la Gmail API (HTTPS + OAuth2). No depende de un
// dominio propio: usa la cuenta autenticada como remitente.
type Gmail struct {
	from         string
	refreshToken string
	config       *oauth2.Config
	// ts y endpoint son hooks de test: en producción ts es nil (se arma desde
	// el refresh token) y endpoint es "" (el default de la API).
	ts       oauth2.TokenSource
	endpoint string
}

// NewGmail construye el cliente. Devuelve nil si falta algún dato.
func NewGmail(clientID, clientSecret, refreshToken, from string) *Gmail {
	if clientID == "" || clientSecret == "" || refreshToken == "" || from == "" {
		return nil
	}
	config := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{gmail.GmailSendScope},
	}
	return &Gmail{
		from:         from,
		refreshToken: refreshToken,
		config:       config,
		// Cachear el token source evita pedir un access token nuevo en cada
		// envío; oauth2 lo refresca solo cuando expira.
		ts: config.TokenSource(context.Background(), &oauth2.Token{RefreshToken: refreshToken}),
	}
}

// Enviar entrega un email por Gmail API. Si html está vacío envía text/plain;
// si no, arma multipart/alternative con texto y HTML.
func (g *Gmail) Enviar(ctx context.Context, destino, asunto, texto, html string) error {
	ctx, cancel := context.WithTimeout(ctx, gmailTimeout)
	defer cancel()

	ts := g.ts
	if ts == nil {
		ts = g.config.TokenSource(ctx, &oauth2.Token{RefreshToken: g.refreshToken})
	}
	opts := []option.ClientOption{option.WithTokenSource(ts)}
	if g.endpoint != "" {
		opts = append(opts, option.WithEndpoint(g.endpoint))
	}
	svc, err := gmail.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("gmail: crear servicio: %w", err)
	}

	raw := base64.RawURLEncoding.EncodeToString(
		g.buildMessage(destino, asunto, texto, html, time.Now()),
	)
	if _, err := svc.Users.Messages.Send("me", &gmail.Message{Raw: raw}).Do(); err != nil {
		return fmt.Errorf("gmail: enviar: %w", err)
	}
	return nil
}

const gmailAltBoundary = "smartcheck-alt-boundary"

// buildMessage arma el RFC 2822. El cuerpo va base64 en líneas de 76
// caracteres; con HTML usa multipart/alternative.
func (g *Gmail) buildMessage(destino, asunto, texto, html string, now time.Time) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", sanitizeHeader(g.from))
	fmt.Fprintf(&b, "To: %s\r\n", sanitizeHeader(destino))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", asunto))
	b.WriteString("MIME-Version: 1.0\r\n")
	if html == "" {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
	} else {
		fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n", gmailAltBoundary)
	}
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	b.WriteString("\r\n")

	writeBase64Part := func(contentType, contenido string) {
		fmt.Fprintf(&b, "--%s\r\n", gmailAltBoundary)
		fmt.Fprintf(&b, "Content-Type: %s; charset=UTF-8\r\n", contentType)
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		writeBase64Lines(&b, contenido)
		b.WriteString("\r\n")
	}

	if html == "" {
		writeBase64Lines(&b, texto)
		return []byte(b.String())
	}

	writeBase64Part("text/plain", texto)
	writeBase64Part("text/html", html)
	fmt.Fprintf(&b, "--%s--\r\n", gmailAltBoundary)
	return []byte(b.String())
}

// writeBase64Lines escribe el contenido en base64 con líneas de 76 caracteres.
func writeBase64Lines(b *strings.Builder, contenido string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(contenido))
	for len(encoded) > 76 {
		b.WriteString(encoded[:76])
		b.WriteString("\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded)
}

// sanitizeHeader evita inyección de cabeceras por CR/LF.
func sanitizeHeader(v string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}
