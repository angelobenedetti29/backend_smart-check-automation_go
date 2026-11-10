package notificacion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewTelegram_RequiereToken(t *testing.T) {
	require.Nil(t, NewTelegram(""))
	require.NotNil(t, NewTelegram("token"))
}

func TestTelegramEnviar(t *testing.T) {
	const token = "123456:secret-token"
	var path string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cliente := NewTelegram(token)
	cliente.baseURL = srv.URL

	require.NoError(t, cliente.Enviar(context.Background(), "987654321", "Hola"))
	require.Equal(t, "/bot"+token+"/sendMessage", path)
	require.Equal(t, "987654321", payload["chat_id"])
	require.Equal(t, "Hola", payload["text"])
	require.Equal(t, true, payload["disable_web_page_preview"])
}

func TestTelegramEnviar_ErrorSinSecreto(t *testing.T) {
	const token = "123456:secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cliente := NewTelegram(token)
	cliente.baseURL = srv.URL

	err := cliente.Enviar(context.Background(), "987654321", "Hola")
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
	require.False(t, strings.Contains(err.Error(), token), "el error no debe filtrar el token")
	require.False(t, strings.Contains(err.Error(), srv.URL), "el error no debe filtrar la URL")
}

func TestTelegramEnviar_ErrorDeTransporteSinSecreto(t *testing.T) {
	const token = "123456:secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	baseURL := srv.URL
	srv.Close() // fuerza un error de conexión

	cliente := NewTelegram(token)
	cliente.baseURL = baseURL

	err := cliente.Enviar(context.Background(), "987654321", "Hola")
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), token), "el error no debe filtrar el token")
	require.False(t, strings.Contains(err.Error(), baseURL), "el error no debe filtrar la URL")
}
