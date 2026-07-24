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

	hornoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/horno"
	loteController "github.com/angelobenedetti29/smart-check-automation/internal/controller/lote"
	loteProductivoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/lote_productivo"
	parametrosProductoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/parametros_producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/yolo_client"
	"github.com/angelobenedetti29/smart-check-automation/internal/repository"
	hornoService "github.com/angelobenedetti29/smart-check-automation/internal/service/horno"
	loteProductivoService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_productivo"
	parametrosProductoService "github.com/angelobenedetti29/smart-check-automation/internal/service/parametros_producto"
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

	// 3. Instantiate Business Layer
	hornoSvc := hornoService.NewHornoService(dbRepo, dbRepo, yolo)
	loteProdSvc := loteProductivoService.NewLoteProductivoService(loteGetRepo)
	parametrosProductoSvc := parametrosProductoService.NewParametrosProductoService(parametrosProductoRepo)

	// 4. Instantiate Presentation HTTP Handlers
	hornoHandler := hornoController.NewHornoHandler(hornoSvc, dbRepo)
	loteHandler := loteController.NewLoteHandler(loteCreateRepo)
	loteProductivoHandler := loteProductivoController.NewLoteProductivoHandler(loteProdSvc)
	parametrosProductoHandler := parametrosProductoController.NewParametrosProductoHandler(parametrosProductoSvc)

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

	// 6. Launch server with graceful shutdown
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
	_, _ = w.Write([]byte("Smart-Check Automation Backend running.\nEndpoints: GET /health, GET /api/v1/horno, POST /api/v1/horno/temperatura, POST /api/v1/lotes, GET /api/v1/lotes-productivos, GET|POST|PUT /api/v1/parametros-producto\n"))
}

func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("[HTTP] %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next(w, r)
		log.Printf("[HTTP] %s %s finished in %v", r.Method, r.URL.Path, time.Since(start))
	}
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
