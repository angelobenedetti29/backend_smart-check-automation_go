package sse

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

// Serve transmite el stream SSE del broker. accept, si no es nil, filtra los
// eventos antes de escribirlos.
func Serve(w http.ResponseWriter, r *http.Request, b *Broker, banner string, accept func(SSEEvent) bool) {
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

	client := b.Subscribe()
	defer b.Unsubscribe(client)

	fmt.Fprintf(w, ": %s\n\n", banner)
	flusher.Flush()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case event, ok := <-client.Events:
			if !ok {
				return
			}
			if accept != nil && !accept(event) {
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
