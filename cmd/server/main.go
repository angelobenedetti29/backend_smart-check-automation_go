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
	"github.com/joho/godotenv"

	dispositivoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/dispositivo"
	hornoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/horno"
	loteController "github.com/angelobenedetti29/smart-check-automation/internal/controller/lote"
	loteProductivoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/lote_productivo"
	parametrosProductoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/parametros_producto"
	sseController "github.com/angelobenedetti29/smart-check-automation/internal/controller/sse"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/yolo_client"
	"github.com/angelobenedetti29/smart-check-automation/internal/repository"
	dispositivoService "github.com/angelobenedetti29/smart-check-automation/internal/service/dispositivo"
	hornoService "github.com/angelobenedetti29/smart-check-automation/internal/service/horno"
	loteProductivoService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_productivo"
	parametrosProductoService "github.com/angelobenedetti29/smart-check-automation/internal/service/parametros_producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	log.Println("Initializing Smart-Check Automation Backend...")

	// 0. Load environment variables from .env file
	if err := godotenv.Load(); err != nil {
		log.Println("Advertencia: archivo .env no encontrado, se usarán variables de entorno del sistema")
	}

	// 1. Initialize PostgreSQL connection pool
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

	// 2. Instantiate Infrastructure Adapters (Providers Layer)
	dbRepo := database.NewPostgresRepository()
	yolo := yolo_client.NewYOLOClient("http://localhost:8500/yolo/conveyor")

	// Real PostgreSQL repositories
	loteCreateRepo := repository.NewPostgresRepository(pgPool)
	loteGetRepo := repository.NewLoteProductivoPostgresRepository(pgPool)
	parametrosProductoRepo := repository.NewParametrosProductoPostgresRepository(pgPool)
	dispositivoRepo := repository.NewPostgresDispositivoRepository(pgPool)

	// SSE broker for real-time event streaming
	sseBroker := sse.NewBroker()

	// Dedicated SSE broker for dispositivo telemetry events (no cross-noise with lotes)
	dispositivoSSEBroker := sse.NewBroker()

	// 3. Instantiate Business Layer
	hornoSvc := hornoService.NewHornoService(dbRepo, dbRepo, yolo)
	loteProdSvc := loteProductivoService.NewLoteProductivoService(loteGetRepo)
	parametrosProductoSvc := parametrosProductoService.NewParametrosProductoService(parametrosProductoRepo)
	dispositivoStore := database.NewMemoryDispositivoStateStore()
	dispositivoSvc := dispositivoService.NewDispositivoService(dispositivoRepo, dispositivoStore, dispositivoSSEBroker)

	reaperInterval := getEnvDuration("DISPOSITIVO_REAPER_INTERVAL", 5*time.Second)
	offlineThreshold := getEnvDuration("DISPOSITIVO_OFFLINE_THRESHOLD", 25*time.Second)

	// Preload device catalog + latest metric into the in-memory state store
	if dispositivos, err := dispositivoRepo.GetDispositivosConUltimaMetrica(context.Background()); err != nil {
		log.Printf("[DISPOSITIVO] Error al precargar catálogo de dispositivos: %v", err)
	} else {
		dispositivoStore.Hydrate(dispositivos, offlineThreshold, time.Now().UTC())
	}

	// 4. Instantiate Presentation HTTP Handlers
	hornoHandler := hornoController.NewHornoHandler(hornoSvc, dbRepo)
	loteHandler := loteController.NewLoteHandler(loteCreateRepo, sseBroker, loteGetRepo)
	loteProductivoHandler := loteProductivoController.NewLoteProductivoHandler(loteProdSvc)
	parametrosProductoHandler := parametrosProductoController.NewParametrosProductoHandler(parametrosProductoSvc)
	sseHandler := sseController.NewSSEHandler(sseBroker)
	dispositivoHandler := dispositivoController.NewDispositivoHandler(dispositivoSvc)
	dispositivoSSEHandler := sseController.NewSSEHandler(dispositivoSSEBroker)

	// 5. Setup routes
	mux := http.NewServeMux()

	mux.HandleFunc("/", rootHandler)
	mux.HandleFunc("/health", healthHandler(pgPool))
	mux.HandleFunc("/healthz", healthHandler(pgPool))

	mux.HandleFunc("/api/v1/horno", loggingMiddleware(hornoHandler.GetHornoStatus))
	mux.HandleFunc("/api/v1/horno/temperatura", loggingMiddleware(hornoHandler.UpdateTemperature))
	mux.HandleFunc("/api/v1/lotes", loggingMiddleware(loteHandler.HandleCreateLote))
	mux.HandleFunc("/api/v1/lotes-productivos", loggingMiddleware(loteProductivoHandler.GetAll))
	mux.HandleFunc("/api/v1/parametros-producto", loggingMiddleware(parametrosProductoHandler.Handle))
	mux.HandleFunc("/api/v1/lotes-productivos/events", loggingMiddleware(sseHandler.HandleSSE))
	mux.HandleFunc("/api/v1/dispositivos/ping", loggingMiddleware(dispositivoHandler.HandlePing))
	mux.HandleFunc("/api/v1/dispositivos", loggingMiddleware(dispositivoHandler.HandleEstados))
	mux.HandleFunc("/api/v1/dispositivos/metricas", loggingMiddleware(dispositivoHandler.HandleMetricas))
	mux.HandleFunc("/api/v1/dispositivos/events", loggingMiddleware(dispositivoSSEHandler.HandleSSE))

	// 6. Wrap mux with CORS middleware
	handler := corsMiddleware(mux)

	// 7. Launch reaper goroutine: detects offline devices and emits SSE events
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	go dispositivoSvc.StartReaper(rootCtx, reaperInterval, offlineThreshold)

	// 8. Launch server with graceful shutdown
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Server running on http://localhost%s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server shutdown unexpectedly: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Iniciando apagado graceful del servidor...")

	// Detener el reaper de dispositivos antes de cerrar el servidor HTTP
	rootCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Error durante graceful shutdown: %v", err)
	}

	log.Println("Servidor apagado exitosamente.")
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Smart-Check Automation Backend running.\nEndpoints: GET /health, GET /api/v1/horno, POST /api/v1/horno/temperatura, POST /api/v1/lotes, GET /api/v1/lotes-productivos, GET|POST|PUT /api/v1/parametros-producto, GET /api/v1/lotes-productivos/events (SSE), POST /api/v1/dispositivos/ping, GET /api/v1/dispositivos, GET /api/v1/dispositivos/metricas, GET /api/v1/dispositivos/events (SSE)\n"))
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-API-Key")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("[HTTP] %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next(w, r)
		log.Printf("[HTTP] %s %s finished in %v", r.Method, r.URL.Path, time.Since(start))
	}
}

// getEnvDuration lee una variable de entorno en formato de duración Go,
// devolviendo el valor por defecto si no está configurada o es inválida.
func getEnvDuration(key string, def time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("Advertencia: valor inválido para %s ('%s'), usando default %s", key, raw, def)
		return def
	}
	return d
}

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
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
	}
}
