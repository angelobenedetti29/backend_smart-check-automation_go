// Package predictor implementa el cliente HTTP al sidecar XGBoost
// (cmd/predictor-svc). El backend Go llama por cada decisión del MPC.
//
// El sidecar puede estar caído (por ejemplo durante un deploy). En ese caso
// el optimizador recibe error y la decisión se marca como "skipped" en
// decisiones_control.razon_skipped para no proponer cambios sin base.
package predictor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
)

// ClientePredictor satisface optimizador.Predictor hablando HTTP al sidecar.
type ClientePredictor struct {
	baseURL string
	http    *http.Client
}

// NewClientePredictor construye un cliente con timeout acotado.
func NewClientePredictor(baseURL string) *ClientePredictor {
	return &ClientePredictor{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: 2 * time.Second,
		},
	}
}

// Predict envía la request al sidecar y devuelve la triplete predicha.
func (c *ClientePredictor) Predict(ctx context.Context, in optimizador.EntradasPredictor) (optimizador.PrediccionSalida, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"producto_id":     in.ProductoID,
		"temp_horno_1":    in.TempHorno1,
		"temp_horno_2":    in.TempHorno2,
		"velocidad_cinta": in.VelocidadCinta,
		"temp_comb_1":     in.TempComb1,
		"temp_comb_2":     in.TempComb2,
		"humedad_horno":   in.HumedadHorno,
	})
	if err != nil {
		return optimizador.PrediccionSalida{}, fmt.Errorf("marshal predict payload: %w", err)
	}

	url := c.baseURL + "/predict"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return optimizador.PrediccionSalida{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return optimizador.PrediccionSalida{}, fmt.Errorf("predictor http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return optimizador.PrediccionSalida{}, fmt.Errorf(
			"predictor status %d: %s", resp.StatusCode, string(body),
		)
	}

	var out optimizador.PrediccionSalida
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return optimizador.PrediccionSalida{}, fmt.Errorf("decode predictor response: %w", err)
	}
	return out, nil
}

// Health devuelve true cuando el sidecar responde 200 a /health.
func (c *ClientePredictor) Health(ctx context.Context) bool {
	url := c.baseURL + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}