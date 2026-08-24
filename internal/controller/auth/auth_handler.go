package auth

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// maxLoginBodyBytes limita el body del login a 8 KB.
// Un Google ID Token típico pesa ~1–2 KB; este límite previene DoS por body enorme.
const maxLoginBodyBytes = 8 * 1024

// googleLoginRequest es el payload esperado del frontend al hacer login con Google.
type googleLoginRequest struct {
	GoogleToken string `json:"googleToken"`
}

// localLoginRequest es el payload esperado del frontend al hacer login con credenciales locales.
type localLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// AuthHandler maneja los endpoints de autenticación.
type AuthHandler struct {
	svc *authService.AuthService
}

// NewAuthHandler crea el handler de autenticación inyectando el servicio.
func NewAuthHandler(svc *authService.AuthService) *AuthHandler {
	return &AuthHandler{svc: svc}
}

// Login procesa POST /api/v1/auth/login.
// Recibe email y password, valida credenciales contra PostgreSQL (bcrypt) y emite la cookie JWT.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBodyBytes)

	var req localLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "Body inválido", nil)
		return
	}

	if req.Email == "" || req.Password == "" {
		response.Error(w, http.StatusBadRequest, "email y password requeridos", nil)
		return
	}

	jwtToken, err := h.svc.LoginWithCredentials(r.Context(), req.Email, req.Password)
	if err != nil {
		log.Printf("[SECURITY] Login local fallido desde %s para email %s — razón: %v", r.RemoteAddr, req.Email, err)

		if errors.Is(err, user.ErrInvalidCredentials) || errors.Is(err, user.ErrUserNotFound) {
			response.Error(w, http.StatusUnauthorized, "Credenciales inválidas", nil)
			return
		}

		response.Error(w, http.StatusInternalServerError, "Error interno de autenticación", nil)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    jwtToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(authService.JWTDuration.Seconds()),
	})

	log.Printf("[AUDIT] Login local exitoso desde %s para email %s", r.RemoteAddr, req.Email)
	response.JSON(w, http.StatusOK, true, "Autenticación exitosa", nil, nil)
}

// LoginWithGoogle procesa POST /api/v1/auth/google.
// Recibe el Google ID Token del frontend, lo valida, verifica el usuario corporativo
// y setea el JWT interno en una cookie HttpOnly+Secure+SameSite=Strict.
func (h *AuthHandler) LoginWithGoogle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return
	}

	// Limitar body para prevenir DoS por payload enorme (igual que LoteHandler)
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBodyBytes)

	var req googleLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "Body inválido", nil)
		return
	}

	if req.GoogleToken == "" {
		response.Error(w, http.StatusBadRequest, "googleToken requerido", nil)
		return
	}

	jwtToken, err := h.svc.LoginWithGoogle(r.Context(), req.GoogleToken)
	if err != nil {
		// AUDIT: registrar el intento fallido con IP pero SIN el Google Token
		// (podría contener información personal si se logueara completo)
		log.Printf("[SECURITY] Login fallido desde %s — razón: %v", r.RemoteAddr, err)

		if errors.Is(err, user.ErrInvalidToken) {
			response.Error(w, http.StatusUnauthorized, "Token de Google inválido o expirado", nil)
			return
		}

		if errors.Is(err, user.ErrUserNotFound) {
			// Mensaje genérico INTENCIONAL: no revelar si el email existe o no
			// (previene enumeración de usuarios corporativos)
			response.Error(w, http.StatusUnauthorized, "Acceso no autorizado", nil)
			return
		}

		// Error de infraestructura (DB caída, error de firma, etc.)
		response.Error(w, http.StatusInternalServerError, "Error interno de autenticación", nil)
		return
	}

	// Login exitoso — setear JWT en cookie HttpOnly
	// HttpOnly: JS del frontend no puede acceder → protección XSS
	// Secure: solo se envía en HTTPS → protección en tránsito
	// SameSite=Strict: no se envía en requests cross-origin → protección CSRF
	// MaxAge: 8h sincronizado con la expiración del JWT
	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    jwtToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(authService.JWTDuration.Seconds()),
	})

	log.Printf("[AUDIT] Login exitoso desde %s", r.RemoteAddr)
	response.JSON(w, http.StatusOK, true, "Autenticación exitosa", nil, nil)
}

// Logout procesa POST /api/v1/auth/logout.
// Revoca la cookie de sesión enviando una cookie con MaxAge=-1,
// lo que instruye al navegador a eliminarla inmediatamente.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,            // -1 instruye al browser a eliminar la cookie inmediatamente
		Expires:  time.Unix(0, 0), // compatibilidad con clientes HTTP/1.0
	})

	log.Printf("[AUDIT] Logout desde %s", r.RemoteAddr)
	response.JSON(w, http.StatusOK, true, "Sesión cerrada", nil, nil)
}
