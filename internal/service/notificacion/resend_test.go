package notificacion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewResend_RequiereConfiguracion(t *testing.T) {
	require.Nil(t, NewResend("", "a@x.com"))
	require.Nil(t, NewResend("key", ""))
	require.NotNil(t, NewResend("key", "a@x.com"))
}

func TestResendEnviar(t *testing.T) {
	var auth, path string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		path = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cliente := NewResend("re_key", "alertas@fermar.com.ar")
	cliente.baseURL = srv.URL

	require.NoError(t, cliente.Enviar(context.Background(), "operario@fermar.com.ar", "Asunto", "Cuerpo", "<p>HTML</p>"))
	require.Equal(t, "Bearer re_key", auth)
	require.Equal(t, "/emails", path)
	require.Equal(t, "alertas@fermar.com.ar", payload["from"])
	require.Equal(t, "Asunto", payload["subject"])
	require.Equal(t, "Cuerpo", payload["text"])
	require.Equal(t, "<p>HTML</p>", payload["html"])
	require.Equal(t, []any{"operario@fermar.com.ar"}, payload["to"])
}

func TestResendEnviar_ErrorNo2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"invalid from"}`))
	}))
	defer srv.Close()

	cliente := NewResend("re_key", "alertas@fermar.com.ar")
	cliente.baseURL = srv.URL

	err := cliente.Enviar(context.Background(), "a@x.com", "Asunto", "Cuerpo", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "422")
	require.Contains(t, err.Error(), "invalid from")
}

func TestResendEnviar_SinHTMLOmiteCampo(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cliente := NewResend("re_key", "alertas@fermar.com.ar")
	cliente.baseURL = srv.URL

	require.NoError(t, cliente.Enviar(context.Background(), "a@x.com", "Asunto", "Cuerpo", ""))
	require.NotContains(t, payload, "html")
}
