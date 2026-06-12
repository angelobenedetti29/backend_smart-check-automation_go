package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/controller/horno"
	lotectrl "github.com/angelobenedetti29/smart-check-automation/internal/controller/lote"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/yolo_client"
	"github.com/angelobenedetti29/smart-check-automation/internal/repository"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/horno"
	"github.com/joho/godotenv"
)

func main() {
	// Enable microsecond resolution logs for Industry 4.0 telemetry monitoring
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	log.Println("Initializing Smart-Check Automation Backend (Layered/Clean Architecture - Modular)...")

	// 0. Load environment variables from .env file
	if err := godotenv.Load(); err != nil {
		log.Println("Advertencia: archivo .env no encontrado, se usarán variables de entorno del sistema")
	}

	// Initialize PostgreSQL connection pool with mandatory SSL for Aiven Cloud
	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		log.Fatal("DATABASE_URL no configurada. Verifica el archivo .env o la variable de entorno")
	}

	pgPool, err := database.NewPostgresPool(context.Background(), connString)
	if err != nil {
		log.Fatalf("Error fatal al inicializar pool de PostgreSQL: %v", err)
	}
	defer pgPool.Close()

	log.Println("Pool de conexiones a PostgreSQL inicializado y verificado exitosamente.")

	log.Println("PostgreSQL pool delegado a los repositorios. Inicializando capas de negocio...")

	// 1. Instantiate Infrastructure Adapters (Providers Layer)
	dbRepo := database.NewPostgresRepository()
	yolo := yolo_client.NewYOLOClient("http://localhost:8500/yolo/conveyor")

	// 2. Instantiate Business Layer injecting providers (Service Layer)
	hornoService := service.NewHornoService(dbRepo, dbRepo, yolo)

	// 3. Instantiate Presentation HTTP Handlers injecting services (Controller Layer)
	hornoHandler := controller.NewHornoHandler(hornoService, dbRepo)

	// Instantiate Lote handler with real PostgreSQL repository
	loteRepo := repository.NewPostgresRepository(pgPool)
	loteHandler := lotectrl.NewLoteHandler(loteRepo)

	// 4. Setup Serve Multiplexer and Register routes
	mux := http.NewServeMux()

	// Root welcome landing
	mux.HandleFunc("/", rootHandler)

	// Register API endpoints with logging middleware
	mux.HandleFunc("/api/v1/horno", loggingMiddleware(hornoHandler.GetHornoStatus))
	mux.HandleFunc("/api/v1/horno/temperatura", loggingMiddleware(hornoHandler.UpdateTemperature))
	mux.HandleFunc("/api/v1/lotes", loggingMiddleware(loteHandler.HandleCreateLote))

	// Health check for liveness/readiness probes (load balancers, Kubernetes, etc.)
	mux.HandleFunc("/health", healthHandler(pgPool))

	// 5. Read environment configs and launch server with graceful shutdown
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine so we can listen for shutdown signals
	go func() {
		log.Printf("Transactional server running on http://localhost%s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server shutdown unexpectedly: %v", err)
		}
	}()

	// Block until an OS signal is received (Ctrl+C, SIGTERM)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Iniciando apagado graceful del servidor...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Error durante graceful shutdown: %v", err)
	}

	log.Println("Servidor apagado exitosamente. Pool de PostgreSQL cerrado.")
}

// rootHandler maps the homepage to double check server status
func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Smart-Check Automation Backend running. Endpoints: GET /api/v1/horno, POST /api/v1/horno/temperatura, POST /api/v1/lotes\n"))
}

// loggingMiddleware prints execution diagnostics for each HTTP request
func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("[HTTP Telemetry] %s %s starting from %s", r.Method, r.URL.Path, r.RemoteAddr)

		next(w, r)

		log.Printf("[HTTP Telemetry] %s %s finished in %v", r.Method, r.URL.Path, time.Since(start))
	}
}

// healthHandler returns 200 OK if the service is up and can reach PostgreSQL,
// or 503 Service Unavailable if the database ping fails.
func healthHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := pool.Ping(ctx); err != nil {
			log.Printf("[HEALTH] PostgreSQL ping failed: %v", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "unhealthy",
				"reason": "database unreachable",
			})
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "healthy",
		})
	}
}
