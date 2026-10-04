package controller

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/controller/helpers"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// ControlSSEHandler expone GET /api/v1/lotes/{id}/control/events: transmite
// sólo los eventos `control.propuesta` cuyo `lote_id` del payload coincide
// con el {id} del path. El broker global emite a todos los lotes activos;
// este handler filtra client-side para que el dashboard reciba sólo lo suyo.
type ControlSSEHandler struct {
	broker *sse.Broker
}

// NewControlSSEHandler crea el handler a partir del broker dedicado al MPC.
func NewControlSSEHandler(broker *sse.Broker) *ControlSSEHandler {
	return &ControlSSEHandler{broker: broker}
}

// HandleControlEvents filtra los eventos `control.propuesta` del broker por
// el {id} del path. Requiere middleware JWT previo (Supervisor+ mínimo).
func (h *ControlSSEHandler) HandleControlEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	loteID, err := helpers.ExtractLoteID(r.URL.Path, "/control/events")
	if err != nil {
		http.Error(w, "Lote no encontrado", http.StatusNotFound)
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

	fmt.Fprintf(w, ": connected lote=%s\n\n", loteID)
	flusher.Flush()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case event, ok := <-client.Events:
			if !ok {
				return
			}
			if event.EventType != "control.propuesta" {
				continue
			}
			if !payloadMatchesLote(event.Data, loteID) {
				continue
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

// payloadMatchesLote inspecciona el payload JSON y devuelve true si su
// `lote_id` coincide con el del path. Si el payload no parsea o no tiene
// `lote_id`, devuelve false (defensa por defecto: descartar).
func payloadMatchesLote(data []byte, loteID string) bool {
	var p struct {
		LoteID string `json:"lote_id"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(p.LoteID), strings.TrimSpace(loteID))
}