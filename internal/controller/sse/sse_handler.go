package controller

import (
	"net/http"

	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// SSEHandler manages SSE connections for real-time batch notifications.
type SSEHandler struct {
	broker *sse.Broker
}

// NewSSEHandler creates a new SSE handler.
func NewSSEHandler(broker *sse.Broker) *SSEHandler {
	return &SSEHandler{broker: broker}
}

// HandleSSE maneja GET en las rutas SSE /api/v1/lotes/events,
// /api/v1/dispositivos/events y /api/v1/horno/events: transmite en tiempo real
// los eventos publicados en el broker.
func (h *SSEHandler) HandleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	sse.Serve(w, r, h.broker, "connected", nil)
}
