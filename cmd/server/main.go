package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	schema "github.com/angelobenedetti29/smart-check-automation/database"
	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	consignaController "github.com/angelobenedetti29/smart-check-automation/internal/controller/consigna"
	deviceTokenController "github.com/angelobenedetti29/smart-check-automation/internal/controller/devicetoken"
	dispositivoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/dualauth"
	hornoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/horno"
	loteProductivoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/lote_productivo"
	loteSectorController "github.com/angelobenedetti29/smart-check-automation/internal/controller/lote_sector"
	parametrosProductoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/parametros_producto"
	productoController "github.com/angelobenedetti29/smart-check-automation/internal/controller/producto"
	registroController "github.com/angelobenedetti29/smart-check-automation/internal/controller/registro"
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
	loteSectorService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
	parametrosProductoService "github.com/angelobenedetti29/smart-check-automation/internal/service/parametros_producto"
	productoService "github.com/angelobenedetti29/smart-check-automation/internal/service/producto"
	registroService "github.com/angelobenedetti29/smart-check-automation/internal/service/registro"
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
	loteGetRepo := repository.NewLoteProductivoPostgresRepository(pgPool)
	parametrosProductoRepo := repository.NewParametrosProductoPostgresRepository(pgPool)
	dispositivoRepo := repository.NewPostgresDispositivoRepository(pgPool)
	consignaRepo := repository.NewConsignaPostgresRepository(pgPool)
	userRepo := repository.NewUserPostgresRepository(pgPool)
	deviceAuthRepo := repository.NewDeviceAuthRepository(pgPool)
	registrationRepo := repository.NewRegistrationRepository(pgPool)
	productoRepo := repository.NewProductoPostgresRepository(pgPool)
	sectorRepo := repository.NewSectorPostgresRepository(pgPool)
	loteSectorRepo := repository.NewLoteSectorPostgresRepository(pgPool)

	// Auth provider (Google)
	googleOAuthProvider := googleProvider.NewOAuthProvider(googleClientID)

	// SSE broker for real-time event streaming
	sseBroker := sse.NewBroker()

	// Dedicated SSE broker for dispositivo telemetry events (no cross-noise with lotes)
	dispositivoSSEBroker := sse.NewBroker()

	// Dedicated SSE broker for horno/consigna events (no cross-noise con lotes/dispositivos)
	hornoSSEBroker := sse.NewBroker()

	// Dedicated SSE broker for the new sector/lotes stream (GET /api/v1/lotes/events),
	// kept apart from sseBroker to avoid cross-talk with the legacy
	// GET /api/v1/lotes-productivos/events stream.
	loteSectorSSEBroker := sse.NewBroker()

	// 3. Instantiate Business Layer
	hornoSvc := hornoService.NewHornoService(dbRepo, dbRepo, yolo)
	loteProdSvc := loteProductivoService.NewLoteProductivoService(loteGetRepo)
	parametrosProductoSvc := parametrosProductoService.NewParametrosProductoService(parametrosProductoRepo)
	dispositivoStore := database.NewMemoryDispositivoStateStore()
	dispositivoSvc := dispositivoService.NewDispositivoService(dispositivoRepo, dispositivoStore, dispositivoSSEBroker)
	// Autenticación de nodos: Bearer <secret> resuelto por hash contra dispositivos activos.
	deviceVerifier := &deviceauth.Verifier{Store: deviceAuthRepo}
	registroSvc := registroService.NewService(registrationRepo)
	consignaSvc := consignaService.NewConsignaService(consignaRepo, dbRepo, parametrosProductoRepo, ovenController, hornoSSEBroker, dbRepo)

	authSvc := authService.NewAuthService(googleOAuthProvider, userRepo, jwtSecret)
	userSvc := userService.NewService(userRepo)

	// Catálogo de productos y ciclo de lote por sector (contrato lotes-sector).
	productoSvc := productoService.NewService(productoRepo)
	// LOTE_CIERRE_MIN_INACTIVIDAD_SEGUNDOS en segundos float; 0 = no enforce.
	loteCierreMinInactividad := getEnvFloatSeconds("LOTE_CIERRE_MIN_INACTIVIDAD_SEGUNDOS", 0)
	loteSectorSvc := loteSectorService.NewService(loteSectorRepo, sectorRepo, productoRepo, loteSectorSSEBroker, loteCierreMinInactividad)

	reaperInterval := getEnvDuration("DISPOSITIVO_REAPER_INTERVAL", 5*time.Second)
	offlineThreshold := getEnvDuration("DISPOSITIVO_OFFLINE_THRESHOLD", 90*time.Second)

	// Preload device catalog + latest metric into the in-memory state store
	if dispositivos, err := dispositivoRepo.GetDispositivosConUltimaMetrica(context.Background()); err != nil {
		log.Printf("[DISPOSITIVO] Error al precargar catálogo de dispositivos: %v", err)
	} else {
		dispositivoStore.Hydrate(dispositivos, offlineThreshold, time.Now().UTC())
	}

	// 4. Instantiate Presentation HTTP Handlers
	hornoHandler := hornoController.NewHornoHandler(hornoSvc, dbRepo)
	loteProductivoHandler := loteProductivoController.NewLoteProductivoHandler(loteProdSvc)
	parametrosProductoHandler := parametrosProductoController.NewParametrosProductoHandler(parametrosProductoSvc)
	sseHandler := sseController.NewSSEHandler(sseBroker)
	loteSectorSSEHandler := sseController.NewSSEHandler(loteSectorSSEBroker)
	dispositivoHandler := dispositivoController.NewDispositivoHandler(dispositivoSvc, dispositivoRepo)
	registroHandler := registroController.NewHandler(registroSvc)
	dispositivoSSEHandler := sseController.NewSSEHandler(dispositivoSSEBroker)
	consignaHandler := consignaController.NewConsignaHandler(consignaSvc)
	hornoSSEHandler := sseController.NewSSEHandler(hornoSSEBroker)
	productoHandler := productoController.NewProductoHandler(productoSvc)
	loteSectorHandler := loteSectorController.NewLoteSectorHandler(loteSectorSvc)

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
		switch r.Method {
		case http.MethodGet:
			dualauth.DualAuth(deviceVerifier, jwtSecretBytes, loteSectorHandler.HandleHistorial)(w, r)
		default:
			// POST /api/v1/lotes (alta legada de lote ya finalizado) fue removido:
			// el ciclo de lote ahora se inicia con POST /api/v1/lotes/inicio.
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		}
	}))
	mux.HandleFunc("/api/v1/lotes/inicio", loggingMiddleware(func(w http.ResponseWriter, r *http.Request) {
		// Reemplaza al inicio legado que disparaba consigna (SCA-142): ahora
		// abre/persiste el lote abierto del sector y NO dispara consigna.
		loteSectorHandler.HandleInicio(w, r)
	}))
	mux.HandleFunc("/api/v1/lotes/abierto", loggingMiddleware(dualauth.DualAuth(deviceVerifier, jwtSecretBytes, loteSectorHandler.HandleAbierto)))
	mux.HandleFunc("/api/v1/lotes/events", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, loteSectorSSEHandler.HandleSSE)))
	mux.HandleFunc("/api/v1/lotes/", loggingMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/eventos"):
			loteSectorHandler.HandleEventos(w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cierre"):
			loteSectorHandler.HandleCierre(w, r)
		default:
			response.Error(w, http.StatusNotFound, "Recurso no encontrado", nil)
		}
	}))
	mux.HandleFunc("/api/v1/productos", loggingMiddleware(dualauth.DualAuth(deviceVerifier, jwtSecretBytes, productoHandler.Handle)))
	mux.HandleFunc("/api/v1/dispositivos/sector", loggingMiddleware(loteSectorHandler.HandleSector))
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
	// Rename emitido por la propia Raspberry con su secret Bearer; sin JWT.
	mux.HandleFunc("/api/v1/dispositivos/nombre", loggingMiddleware(dispositivoHandler.HandleRename))

	// Registro de dispositivos bajo /api/v1, alineado con el resto de la API. El
	// cliente Raspberry debe apuntar api.registration_requests_endpoint a
	// /api/v1/registration-requests.
	// POST (alta del nodo) y GET por id (pickup) son públicos y JSON plano; GET
	// del listado y las acciones approve/reject son del panel (JWT Supervisor/Admin).
	mux.HandleFunc("/api/v1/registration-requests", loggingMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			registroHandler.HandleCreate(w, r)
		case http.MethodGet:
			authController.JWTMiddleware(jwtSecretBytes, authController.RequireRoleFromDB(userRepo, []string{user.RoleSupervisor, user.RoleAdmin}, management(registroHandler.HandleList)))(w, r)
		default:
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		}
	}))
	mux.HandleFunc("/api/v1/registration-requests/", loggingMiddleware(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && !strings.HasSuffix(r.URL.Path, "/approve") && !strings.HasSuffix(r.URL.Path, "/reject"):
			registroHandler.HandlePickup(w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/approve"):
			authController.JWTMiddleware(jwtSecretBytes, authController.RequireRoleFromDB(userRepo, []string{user.RoleSupervisor, user.RoleAdmin}, management(registroHandler.HandleApprove)))(w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/reject"):
			authController.JWTMiddleware(jwtSecretBytes, authController.RequireRoleFromDB(userRepo, []string{user.RoleSupervisor, user.RoleAdmin}, management(registroHandler.HandleReject)))(w, r)
		default:
			response.Error(w, http.StatusNotFound, "Recurso no encontrado", nil)
		}
	}))
	mux.HandleFunc("/api/v1/dispositivos/metricas", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, dispositivoHandler.HandleMetricas)))
	mux.HandleFunc("/api/v1/dispositivos/events", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, dispositivoSSEHandler.HandleSSE)))
	mux.HandleFunc("/api/v1/horno/consigna", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, authController.RequireRoleFromDB(userRepo, []string{user.RoleOperario, user.RoleSupervisor, user.RoleAdmin}, management(consignaHandler.DispatchManual)))))
	mux.HandleFunc("/api/v1/horno/consigna/historial", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, consignaHandler.GetHistorial)))
	mux.HandleFunc("/api/v1/horno/events", loggingMiddleware(authController.JWTMiddleware(jwtSecretBytes, hornoSSEHandler.HandleSSE)))

	// 6. Autenticación de dispositivo antes del ServeMux, luego cuotas por peer/usuario.
	limiter := guard.NewLimiter(0.5, 10)     // 30/minute, burst 10 para endpoints públicos de registro
	deviceLimiter := guard.NewLimiter(2, 30) // 120/minute, burst 30 tras autenticar por UUID
	handler := deviceTokenController.BeforeMux(deviceVerifier, deviceLimiter, corsMiddleware(mux))
	handler = limiter.Middleware(func(r *http.Request) string {
		// Endpoints públicos del registro: alta del nodo (POST) y pickup (GET).
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/registration-requests" {
			return "registration-public"
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/registration-requests/") &&
			!strings.HasSuffix(r.URL.Path, "/approve") && !strings.HasSuffix(r.URL.Path, "/reject") {
			return "registration-public"
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
	// Endpoints originados por nodos: no tienen Origin de navegador, así que no
	// aplican el allowlist de orígenes (su auth es el Bearer secret).
	if r.URL.Path == "/api/v1/dispositivos/ping" || r.URL.Path == "/api/v1/lotes/inicio" || r.URL.Path == "/api/v1/registration-requests" {
		return false
	}
	// Reporte de eventos y cierre de lote: también device-only.
	if strings.HasPrefix(r.URL.Path, "/api/v1/lotes/") &&
		(strings.HasSuffix(r.URL.Path, "/eventos") || strings.HasSuffix(r.URL.Path, "/cierre")) {
		return false
	}
	// Aprobar/rechazar una solicitud son escrituras del panel (cookie JWT).
	if strings.HasSuffix(r.URL.Path, "/approve") || strings.HasSuffix(r.URL.Path, "/reject") {
		return strings.HasPrefix(r.URL.Path, "/api/v1/registration-requests/")
	}
	return strings.HasPrefix(r.URL.Path, "/api/v1/admin/") || strings.HasPrefix(r.URL.Path, "/api/v1/dispositivos") || strings.HasPrefix(r.URL.Path, "/api/v1/parametros-producto") || strings.HasPrefix(r.URL.Path, "/api/v1/horno/")
}

func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		path := redactPath(r.URL.Path)
		log.Printf("[HTTP] %s %s from %s", r.Method, path, r.RemoteAddr)
		next(w, r)
		log.Printf("[HTTP] %s %s finished in %v", r.Method, path, time.Since(start))
	}
}

// redactPath oculta el request_id de las rutas de registro: es una credencial de
// alta capacidad y no debe quedar en los logs de acceso.
func redactPath(path string) string {
	const prefix = "/api/v1/registration-requests/"
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	rest := strings.TrimPrefix(path, prefix)
	for _, suffix := range []string{"/approve", "/reject"} {
		if strings.HasSuffix(rest, suffix) {
			return prefix + "{id}" + suffix
		}
	}
	return prefix + "{id}"
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

// getEnvFloatSeconds lee una variable de entorno en segundos (float) y la
// convierte a time.Duration. Devuelve el default si no está configurada, es
// inválida o es negativa.
func getEnvFloatSeconds(key string, defSeconds float64) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return time.Duration(defSeconds * float64(time.Second))
	}
	secs, err := strconv.ParseFloat(raw, 64)
	if err != nil || secs < 0 {
		log.Printf("Advertencia: valor inválido para %s ('%s'), usando default %gs", key, raw, defSeconds)
		return time.Duration(defSeconds * float64(time.Second))
	}
	return time.Duration(secs * float64(time.Second))
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
