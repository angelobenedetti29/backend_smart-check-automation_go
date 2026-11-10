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

// TestModeloEntrenado cubre presente, ausente y status no-200.
func TestModeloEntrenado(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		productoID string
		want       bool
		wantErr    bool
	}{
		{"presente", http.StatusOK, `{"productos":["p1","p2"]}`, "p2", true, false},
		{"ausente", http.StatusOK, `{"productos":["p1"]}`, "p9", false, false},
		{"lista-vacia", http.StatusOK, `{"productos":[]}`, "p1", false, false},
		{"no-200", http.StatusInternalServerError, `boom`, "p1", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotAuth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := NewClientePredictorWithAuth(srv.URL, "tok")
			got, err := c.ModeloEntrenado(context.Background(), tc.productoID)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("ModeloEntrenado = %v, want %v", got, tc.want)
			}
			if gotPath != "/admin/modelos" {
				t.Fatalf("path = %q, want /admin/modelos", gotPath)
			}
			if gotAuth != "Bearer tok" {
				t.Fatalf("Authorization = %q, want Bearer tok", gotAuth)
			}
		})
	}
}
