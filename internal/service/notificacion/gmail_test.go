package notificacion

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestNewGmail_RequiereConfiguracion(t *testing.T) {
	require.Nil(t, NewGmail("", "secret", "refresh", "yo@gmail.com"))
	require.Nil(t, NewGmail("id", "", "refresh", "yo@gmail.com"))
	require.Nil(t, NewGmail("id", "secret", "", "yo@gmail.com"))
	require.Nil(t, NewGmail("id", "secret", "refresh", ""))
	require.NotNil(t, NewGmail("id", "secret", "refresh", "yo@gmail.com"))
}

func TestGmailBuildMessage(t *testing.T) {
	g := &Gmail{from: "yo@gmail.com"}
	asunto := "Alerta 🔥 á"
	cuerpo := strings.Repeat("á", 80) // fuerza más de una línea base64
	now := time.Date(2026, 11, 4, 14, 2, 0, 0, time.FixedZone("ART", -3*3600))

	raw := string(g.buildMessage("dest@example.com", asunto, cuerpo, "", now))

	require.Contains(t, raw, "From: yo@gmail.com\r\n")
	require.Contains(t, raw, "To: dest@example.com\r\n")
	require.Contains(t, raw, "MIME-Version: 1.0\r\n")
	require.Contains(t, raw, "Content-Type: text/plain; charset=UTF-8\r\n")
	require.Contains(t, raw, "Content-Transfer-Encoding: base64\r\n")
	require.Contains(t, raw, "Date: Wed, 04 Nov 2026 14:02:00 -0300\r\n")

	subjectLine := headerValue(t, raw, "Subject: ")
	require.Contains(t, subjectLine, "=?UTF-8?")
	decodificado, err := new(mime.WordDecoder).DecodeHeader(subjectLine)
	require.NoError(t, err)
	require.Equal(t, asunto, decodificado)

	partes := strings.SplitN(raw, "\r\n\r\n", 2)
	require.Len(t, partes, 2)
	for _, linea := range strings.Split(partes[1], "\r\n") {
		require.LessOrEqual(t, len(linea), 76, "las líneas base64 deben tener <= 76 chars")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(partes[1], "\r\n", ""))
	require.NoError(t, err)
	require.Equal(t, cuerpo, string(decoded))
}

// TestGmailEnviar_ConEndpointFalso verifica el POST a Users.Messages.Send con
// un token estático y un servidor HTTP local.
func TestGmailEnviar_ConEndpointFalso(t *testing.T) {
	var metodo, path string
	var recibido struct {
		Raw string `json:"raw"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metodo = r.Method
		path = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&recibido))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m1","threadId":"t1"}`))
	}))
	defer srv.Close()

	g := NewGmail("id", "secret", "refresh", "yo@gmail.com")
	g.endpoint = srv.URL + "/"
	g.ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})

	require.NoError(t, g.Enviar(context.Background(), "dest@example.com", "Asunto", "Cuerpo", ""))
	require.Equal(t, http.MethodPost, metodo)
	require.Contains(t, path, "/users/me/messages/send")

	raw, err := base64.RawURLEncoding.DecodeString(recibido.Raw)
	require.NoError(t, err)
	require.Contains(t, string(raw), "To: dest@example.com\r\n")
	require.Contains(t, string(raw), "Subject: Asunto\r\n")
}

func TestGmailEnviar_ErrorNo2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid grant"}}`))
	}))
	defer srv.Close()

	g := NewGmail("id", "secret", "refresh", "yo@gmail.com")
	g.endpoint = srv.URL + "/"
	g.ts = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})

	err := g.Enviar(context.Background(), "dest@example.com", "Asunto", "Cuerpo", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

func TestGmailBuildMessage_Multipart(t *testing.T) {
	g := &Gmail{from: "yo@gmail.com"}
	now := time.Date(2026, 11, 4, 14, 2, 0, 0, time.FixedZone("ART", -3*3600))
	texto := "Cuerpo texto"
	html := strings.Repeat("<p>Hola HTML ✅</p>", 8)

	raw := string(g.buildMessage("dest@example.com", "Asunto", texto, html, now))

	require.Contains(t, raw, `Content-Type: multipart/alternative; boundary="smartcheck-alt-boundary"`)
	require.Contains(t, raw, "--smartcheck-alt-boundary\r\n")
	require.Contains(t, raw, "--smartcheck-alt-boundary--\r\n")
	require.Equal(t, texto, gmailPart(t, raw, "text/plain"))
	require.Equal(t, html, gmailPart(t, raw, "text/html"))
}

// gmailPart extrae y decodifica una parte del multipart/alternative.
func gmailPart(t *testing.T, raw, contentType string) string {
	t.Helper()
	header := "Content-Type: " + contentType + "; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n"
	idx := strings.Index(raw, header)
	require.GreaterOrEqual(t, idx, 0, "no se encontró la parte %s", contentType)
	rest := raw[idx+len(header):]
	end := strings.Index(rest, "\r\n--smartcheck-alt-boundary")
	require.GreaterOrEqual(t, end, 0, "no se encontró el boundary de cierre")
	encoded := strings.ReplaceAll(rest[:end], "\r\n", "")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	return string(decoded)
}

func headerValue(t *testing.T, raw, prefix string) string {
	t.Helper()
	idx := strings.Index(raw, prefix)
	require.GreaterOrEqual(t, idx, 0, "no se encontró %q", prefix)
	rest := raw[idx+len(prefix):]
	return strings.SplitN(rest, "\r\n", 2)[0]
}
