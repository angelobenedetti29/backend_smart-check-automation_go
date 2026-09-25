package controller

import (
	"fmt"
	"log"
	"net/http"
	"time"

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

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming no soportado", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		log.Printf("[SSE] Error al limpiar write deadline: %v", err)
	}

	client := h.broker.Subscribe()
	defer h.broker.Unsubscribe(client)

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case event, ok := <-client.Events:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.EventType, string(event.Data))
			flusher.Flush()

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}
