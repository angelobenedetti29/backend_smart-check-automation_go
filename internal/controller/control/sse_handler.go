package controller

import (
	"encoding/json"
	"net/http"
	"strings"

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

	sse.Serve(w, r, h.broker, "connected lote="+loteID, func(e sse.SSEEvent) bool {
		return e.EventType == "control.propuesta" && payloadMatchesLote(e.Data, loteID)
	})
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
