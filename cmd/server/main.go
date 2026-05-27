package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/controller/horno"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/yolo_client"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/horno"
)

func main() {
	// Enable microsecond resolution logs for Industry 4.0 telemetry monitoring
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	log.Println("Initializing Smart-Check Automation Backend (Layered/Clean Architecture - Modular)...")

	// 1. Instantiate Infrastructure Adapters (Providers Layer)
	dbRepo := database.NewMySQLRepository()
	yolo := yolo_client.NewYOLOClient("http://localhost:8500/yolo/conveyor")

	// 2. Instantiate Business Layer injecting providers (Service Layer)
	hornoService := service.NewHornoService(dbRepo, dbRepo, yolo)

	// 3. Instantiate Presentation HTTP Handlers injecting services (Controller Layer)
	hornoHandler := controller.NewHornoHandler(hornoService, dbRepo)

	// 4. Setup Serve Multiplexer and Register routes
	mux := http.NewServeMux()

	// Root welcome landing
	mux.HandleFunc("/", rootHandler)

	// Register API endpoints with logging middleware
	mux.HandleFunc("/api/v1/horno", loggingMiddleware(hornoHandler.GetHornoStatus))
	mux.HandleFunc("/api/v1/horno/temperatura", loggingMiddleware(hornoHandler.UpdateTemperature))

	// 5. Read environment configs and launch server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	serverAddr := ":" + port
	log.Printf("Transactional server running on http://localhost%s", serverAddr)

	if err := http.ListenAndServe(serverAddr, mux); err != nil {
		log.Fatalf("Server shutdown unexpectedly: %v", err)
	}
}

// rootHandler maps the homepage to double check server status
func rootHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Smart-Check Automation Clean Architecture Backend running. Use /api/v1/horno?id=horno-01 to query.\n"))
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
