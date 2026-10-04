package predictor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
)

// prediccionOK es una respuesta válida del sidecar para /predict.
const prediccionOK = `{"pctCrudos":1,"pctOk":98,"pctQuemados":1,"modelVersion":"test","esPlaceholder":false}`

// TestClientePredictorSinAuthNoEnviaHeader verifica que sin token configurado
// no se envía el header Authorization (compat con sidecars sin auth).
func TestClientePredictorSinAuthNoEnviaHeader(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(prediccionOK))
	}))
	defer srv.Close()

	c := NewClientePredictor(srv.URL)
	if _, err := c.Predict(context.Background(), optimizador.EntradasPredictor{ProductoID: "prod-1"}); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if gotPath != "/predict" {
		t.Fatalf("path = %q, want /predict", gotPath)
	}
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want vacío", gotAuth)
	}
}

// TestClientePredictorConAuthEnviaBearer verifica que con token se envía
// Authorization: Bearer <token> en /predict.
func TestClientePredictorConAuthEnviaBearer(t *testing.T) {
	const token = "s3cr3t-token"
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(prediccionOK))
	}))
	defer srv.Close()

	c := NewClientePredictorWithAuth(srv.URL, token)
	if _, err := c.Predict(context.Background(), optimizador.EntradasPredictor{ProductoID: "prod-1"}); err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if gotPath != "/predict" {
		t.Fatalf("path = %q, want /predict", gotPath)
	}
	if want := "Bearer " + token; gotAuth != want {
		t.Fatalf("Authorization = %q, want %q", gotAuth, want)
	}
}

// TestClientePredictorHealthConAuth verifica que /health también lleva el
// header Bearer cuando hay token.
func TestClientePredictorHealthConAuth(t *testing.T) {
	const token = "s3cr3t-token"
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClientePredictorWithAuth(srv.URL, token)
	if !c.Health(context.Background()) {
		t.Fatalf("Health = false, want true")
	}
	if gotPath != "/health" {
		t.Fatalf("path = %q, want /health", gotPath)
	}
	if want := "Bearer " + token; gotAuth != want {
		t.Fatalf("Authorization = %q, want %q", gotAuth, want)
	}
}
