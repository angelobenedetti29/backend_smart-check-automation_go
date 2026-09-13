package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	schema "github.com/angelobenedetti29/smart-check-automation/database"
	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	consignaController "github.com/angelobenedetti29/smart-check-automation/internal/controller/consigna"
	deviceProofController "github.com/angelobenedetti29/smart-check-automation/internal/controller/deviceproof"
	dispositivoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/dispositivo"
	hornoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/horno"
	loteController "github.com/angelobenedetti29/smart-check-automation/internal/controller/lote"
	loteProductivoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/lote_productivo"
	parametrosProductoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/parametros_producto"
	sseController "github.com/angelobenedetti29/smart-check-automation/internal/controller/sse"
	userController "github.com/angelobenedetti29/smart-check-automation/internal/controller/user"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	"github.com/angelobenedetti29/smart-check-automation/internal/guard"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	googleProvider "github.com/angelobenedetti29/smart-check-automation/internal/provider/google"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/oven_controller"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/yolo_client"
	"github.com/angelobenedetti29/smart-check-automation/internal/repository"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
	consignaService "github.com/angelobenedetti29/smart-check-automation/internal/service/consigna"
	dispositivoService "github.com/angelobenedetti29/smart-check-automation/internal/service/dispositivo"
	hornoService "github.com/angelobenedetti29/smart-check-automation/internal/service/horno"
	loteProductivoService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_productivo"
	parametrosProductoService "github.com/angelobenedetti29/smart-check-automation/internal/service/parametros_producto"
	userService "github.com/angelobenedetti29/smart-check-automation/internal/service/user"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	log.Println("Initializing Smart-Check Automation Backend...")

	// 0. Load environment variables from .env file
	if err := godotenv.Load(); err != nil {
		log.Println("Advertencia: archivo .env no encontrado, se usarán variables de entorno del sistema")
	}

	// Validar variables de entorno requeridas para auth antes de iniciar el servidor
	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")
	jwtSecret := os.Getenv("JWT_SECRET")
	if googleClientID == "" || jwtSecret == "" {
		log.Fatal("GOOGLE_CLIENT_ID y JWT_SECRET son requeridos. Verifica el archivo .env")
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

	// Aplicar el schema idempotente: garantiza que las tablas, columnas e
	// índices estén al día en bases existentes (CREATE/ADD IF NOT EXISTS).
	if err := schema.Apply(context.Background(), pgPool); err != nil {
		log.Fatalf("Error fatal al aplicar schema de base de datos: %v", err)
	}
	log.Println("Schema de base de datos aplicado/verificado correctamente.")

	// 2. Instantiate Infrastructure Adapters (Providers Layer)
	dbRepo := database.NewPostgresRepository()
	yolo := yolo_client.NewYOLOClient("http://localhost:8500/yolo/conveyor")
	ovenController := oven_controller.NewOvenControllerClient("http://localhost:8600/plc/horno")

	// Real PostgreSQL repositories
	loteCreateRepo := repository.NewPostgresRepository(pgPool)
	loteGetRepo := repository.NewLoteProductivoPostgresRepository(pgPool)
	parametrosProductoRepo := repository.NewParametrosProductoPostgresRepository(pgPool)
	dispositivoRepo := repository.NewPostgresDispositivoRepository(pgPool)
	consignaRepo := repository.NewConsignaPostgresRepository(pgPool)
	userRepo := repository.NewUserPostgresRepository(pgPool)
	deviceAuthRepo := repository.NewDeviceAuthRepository(pgPool)
	deviceEnrollmentRepo := repository.NewDeviceEnrollmentRepository(pgPool)

	// Auth provider (Google)
	googleOAuthProvider := googleProvider.NewOAuthProvider(googleClientID)

	// SSE broker for real-time event streaming
	sseBroker := sse.NewBroker()

	// Dedicated SSE broker for dispositivo telemetry events (no cross-noise with lotes)
	dispositivoSSEBroker := sse.NewBroker()

	// Dedicated SSE broker for horno/consigna events (no cross-noise con lotes/dispositivos)
	hornoSSEBroker := sse.NewBroker()

	// 3. Instantiate Business Layer
	hornoSvc := hornoService.NewHornoService(dbRepo, dbRepo, yolo)
	loteProdSvc := loteProductivoService.NewLoteProductivoService(loteGetRepo)
	parametrosProductoSvc := parametrosProductoService.NewParametrosProductoService(parametrosProductoRepo)
	dispositivoStore := database.NewMemoryDispositivoStateStore()
	dispositivoSvc := dispositivoService.NewDispositivoService(dispositivoRepo, dispositivoStore, dispositivoSSEBroker)
	audience := os.Getenv("DEVICE_AUTH_AUDIENCE")
	if audience == "" {
		audience = "http://localhost:" + getEnvOr("PORT", "8080") + "/api/v1"
	}
	proofVerifier := &deviceauth.ProofVerifier{Audience: audience, Store: deviceAuthRepo}
	enrollmentSvc := dispositivoService.NewEnrollmentService(deviceEnrollmentRepo, audience)
	consignaSvc := consignaService.NewConsignaService(consignaRepo, dbRepo, parametrosProductoRepo, ovenController, hornoSSEBroker, dbRepo)

	authSvc := authService.NewAuthService(googleOAuthProvider, userRepo, jwtSecret)
	userSvc := userService.NewService(userRepo)

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
	loteHandler := loteController.NewLoteHandler(loteCreateRepo, sseBroker, loteGetRepo, consignaSvc)
	loteProductivoHandler := loteProductivoController.NewLoteProductivoHandler(loteProdSvc)
	parametrosProductoHandler := parametrosProductoController.NewParametrosProductoHandler(parametrosProductoSvc)
	sseHandler := sseController.NewSSEHandler(sseBroker)
	dispositivoHandler := dispositivoController.NewDispositivoHandler(dispositivoSvc, enrollmentSvc)
	enrollmentHandler := dispositivoController.NewEnrollmentHandler(enrollmentSvc, dispositivoSvc.GetAllEstados)
	dispositivoSSEHandler := sseController.NewSSEHandler(dispositivoSSEBroker)
	consignaHandler := consignaController.NewConsignaHandler(consignaSvc)
	hornoSSEHandler := sseController.NewSSEHandler(hornoSSEBroker)

	authHandler := authController.NewAuthHandler(authSvc)
	userHandler := userController.NewUserHandler(userSvc)
	jwtSecretBytes := []byte(jwtSecret)
	managementLimiter := guard.NewLimiter(0.5, 10)
	management := func(next http.HandlerFunc) http.HandlerFunc {
		return authController.RateLimitByUser(userRepo, managementLimiter, next)
	}

	// 5. Setup routes
	mux := http.NewServeMux()

	mux.HandleFunc("/", rootHandler)
	mux.HandleFunc("/health", healthHandler(pgPool))
	mux.HandleFunc("/healthz", healthHandler(pgPool))

	// Auth — rutas públicas (no requieren JWT)
	mux.HandleFunc("/api/v1/auth/login", loggingMiddleware(authHandler.Login))
	mux.HandleFunc("/api/v1/auth/google", loggingMiddleware(authHandler.LoginWithGoogle))
	mux.HandleFunc("/api/v1/auth/logout", loggingMiddleware(authHandler.Logout))

	// Rutas de Administración de Usuarios (Solo Administrador)
	mux.HandleFunc("/api/v1/admin/usuarios", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, authController.RequireRoleFromDB(userRepo, []string{user.RoleAdmin}, management(userHandler.HandleUsers)))))
	mux.HandleFunc("/api/v1/admin/usuarios/", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, authController.RequireRoleFromDB(userRepo, []string{user.RoleAdmin}, management(userHandler.HandleUserByID)))))

	// Rutas del dominio industrial
	mux.HandleFunc("/api/v1/horno", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, hornoHandler.GetHornoStatus)))
	mux.HandleFunc("/api/v1/horno/temperatura", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, authController.RequireRoleFromDB(userRepo, []string{user.RoleSupervisor, user.RoleAdmin}, management(hornoHandler.UpdateTemperature)))))
	mux.HandleFunc("/api/v1/lotes", loggingMiddleware(func(w http.ResponseWriter, r *http.Request) {
		loteHandler.HandleCreateLote(w, r)
	}))
	mux.HandleFunc("/api/v1/lotes/inicio", loggingMiddleware(func(w http.ResponseWriter, r *http.Request) {
		loteHandler.HandleIniciarLote(w, r)
	}))
	mux.HandleFunc("/api/v1/lotes-productivos", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, loteProductivoHandler.GetAll)))
	parametrosRoleAware := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			authController.RequireRoleFromDB(userRepo, []string{user.RoleSupervisor, user.RoleAdmin}, parametrosProductoHandler.Handle)(w, r)
			return
		}
		parametrosProductoHandler.Handle(w, r)
	}
	mux.HandleFunc("/api/v1/parametros-producto", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, management(parametrosRoleAware))))
	mux.HandleFunc("/api/v1/lotes-productivos/events", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, sseHandler.HandleSSE)))
	mux.HandleFunc("/api/v1/dispositivos/ping", loggingMiddleware(func(w http.ResponseWriter, r *http.Request) {
		dispositivoHandler.HandlePing(w, r)
	}))
	mux.HandleFunc("/api/v1/dispositivos/provision", loggingMiddleware(func(w http.ResponseWriter, r *http.Request) {
		enrollmentHandler.HandleProvision(w, r)
	}))
	mux.HandleFunc("/api/v1/dispositivos/enrollments/recover", loggingMiddleware(func(w http.ResponseWriter, r *http.Request) {
		enrollmentHandler.HandleRecover(w, r)
	}))
	mux.HandleFunc("/api/v1/dispositivos", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, management(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost || r.Method == http.MethodDelete {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		if r.Method == http.MethodPut {
			authController.RequireRoleFromDB(userRepo, []string{user.RoleSupervisor, user.RoleAdmin}, dispositivoHandler.Handle)(w, r)
			return
		}
		dispositivoHandler.Handle(w, r)
	}))))
	mux.HandleFunc("/api/v1/dispositivos/enrollments", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, management(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			authController.RequireRoleFromDB(userRepo, []string{user.RoleOperario, user.RoleSupervisor, user.RoleAdmin}, enrollmentHandler.HandleCollection)(w, r)
			return
		}
		authController.RequireRoleFromDB(userRepo, []string{user.RoleSupervisor, user.RoleAdmin}, enrollmentHandler.HandleCollection)(w, r)
	}))))
	mux.HandleFunc("/api/v1/dispositivos/", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, management(func(w http.ResponseWriter, r *http.Request) {
		h := authController.RequireRoleFromDB(userRepo, []string{user.RoleSupervisor, user.RoleAdmin}, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/cancel"):
				enrollmentHandler.HandleCancel(w, r)
			case strings.HasSuffix(r.URL.Path, "/reprovision"):
				enrollmentHandler.HandleReprovision(w, r)
			case strings.HasSuffix(r.URL.Path, "/disable"), strings.HasSuffix(r.URL.Path, "/enable"), strings.HasSuffix(r.URL.Path, "/revoke"):
				enrollmentHandler.HandleLifecycle(w, r)
			default:
				http.NotFound(w, r)
			}
		})
		h(w, r)
	}))))
	mux.HandleFunc("/api/v1/dispositivos/metricas", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, dispositivoHandler.HandleMetricas)))
	mux.HandleFunc("/api/v1/dispositivos/events", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, dispositivoSSEHandler.HandleSSE)))
	mux.HandleFunc("/api/v1/horno/consigna", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, authController.RequireRoleFromDB(userRepo, []string{user.RoleOperario, user.RoleSupervisor, user.RoleAdmin}, management(consignaHandler.DispatchManual)))))
	mux.HandleFunc("/api/v1/horno/consigna/historial", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, consignaHandler.GetHistorial)))
	mux.HandleFunc("/api/v1/horno/events", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, hornoSSEHandler.HandleSSE)))

	// 6. Guard signed targets before ServeMux, then apply peer/user quotas.
	limiter := guard.NewLimiter(0.5, 10)     // 30/minute, burst 10 for public proof writes
	deviceLimiter := guard.NewLimiter(2, 30) // 120/minute, burst 30 after proof by UUID
	handler := deviceProofController.BeforeMuxWithDeviceLimiter(proofVerifier, deviceLimiter, corsMiddleware(mux))
	handler = limiter.Middleware(func(r *http.Request) string {
		if r.Method != http.MethodPost {
			return ""
		}
		if r.URL.Path == "/api/v1/dispositivos/provision" || r.URL.Path == "/api/v1/dispositivos/enrollments/recover" {
			return "proof-public"
		}
		return ""
	}, handler)

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
		Addr:    ":" + port,
		Handler: handler,
		// ReadHeaderTimeout acota la lectura de los headers de la request.
		// Sin él, una conexión que envía bytes lentamente puede retener el
		// socket indefinidamente (ataque Slowloris). 10s es holgado para
		// clientes legítimos y deja margen frente a ReadTimeout (15s).
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		// SSE handlers intentionally hold the response open; a write deadline
		// here would disconnect healthy dashboard subscriptions every 15s.
		WriteTimeout: 0,
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
	_, _ = w.Write([]byte("Smart-Check Automation Backend running.\n"))
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false
		origins := os.Getenv("FRONTEND_ORIGINS")
		if origins == "" {
			origins = "http://localhost:3000,http://127.0.0.1:3000"
		}
		for _, candidate := range strings.Split(origins, ",") {
			if strings.TrimSpace(candidate) != "" && strings.TrimSpace(candidate) == origin {
				allowed = true
			}
		}
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			if origin != "" && !allowed {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if origin != "" && !allowed && isManagementWrite(r) {
			response.Error(w, http.StatusForbidden, "Origen no permitido", map[string]string{"code": "origin_not_allowed"})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isManagementWrite(r *http.Request) bool {
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		return false
	}
	if r.URL.Path == "/api/v1/dispositivos/ping" || r.URL.Path == "/api/v1/lotes" || r.URL.Path == "/api/v1/lotes/inicio" {
		return false
	}
	return strings.HasPrefix(r.URL.Path, "/api/v1/admin/") || strings.HasPrefix(r.URL.Path, "/api/v1/dispositivos") || strings.HasPrefix(r.URL.Path, "/api/v1/parametros-producto") || strings.HasPrefix(r.URL.Path, "/api/v1/horno/")
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

func getEnvOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
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
