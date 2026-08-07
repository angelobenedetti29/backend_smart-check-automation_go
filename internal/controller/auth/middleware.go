package auth

import (
	"context"
	"log"
	"net/http"

	"github.com/golang-jwt/jwt/v5"

	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// contextKey es un tipo privado para evitar colisiones con otras keys en el contexto HTTP.
type contextKey string

const claimsKey contextKey = "jwt_claims"

// JWTMiddleware es un middleware que protege rutas HTTP verificando la cookie `session_token`.
// Lee el JWT de la cookie HttpOnly (seteada por LoginWithGoogle) y valida:
//   - Existencia de la cookie
//   - Firma válida con HS256
//   - Token no expirado
//   - Algoritmo exactamente HS256 (previene ataque "alg=none")
//
// Si cualquier verificación falla, registra el evento con [SECURITY] y retorna 401
// sin exponer detalles internos al cliente.
func JWTMiddleware(jwtSecret []byte, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Verificar que la cookie de sesión exista
		cookie, err := r.Cookie("session_token")
		if err != nil {
			// http.ErrNoCookie — sesión no iniciada
			response.Error(w, http.StatusUnauthorized, "Sesión requerida", nil)
			return
		}

		// 2. Parsear y validar el JWT
		claims := &authService.Claims{}
		token, err := jwt.ParseWithClaims(cookie.Value, claims, func(t *jwt.Token) (interface{}, error) {
			// Verificar que el algoritmo sea exactamente HMAC (HS256).
			// Esto previene el ataque de confusión de algoritmo ("alg=none" o RS256 con clave pública).
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			// AUDIT: registrar el intento fallido sin exponer el token ni detalles de la firma
			log.Printf("[SECURITY] JWT inválido o expirado — IP: %s, Path: %s", r.RemoteAddr, r.URL.Path)
			response.Error(w, http.StatusUnauthorized, "Sesión inválida o expirada", nil)
			return
		}

		// 3. Inyectar los claims en el contexto para uso en los handlers downstream
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next(w, r.WithContext(ctx))
	}
}

// GetClaimsFromContext extrae los JWT claims inyectados por JWTMiddleware.
// Retorna nil si no hay claims en el contexto (ruta no protegida).
func GetClaimsFromContext(ctx context.Context) *authService.Claims {
	c, _ := ctx.Value(claimsKey).(*authService.Claims)
	return c
}

// RequireRole valida que el usuario en sesión (extraído del JWT context) posea uno de los roles permitidos.
// Si no hay claims en el contexto, responde HTTP 401.
// Si el rol del usuario no coincide con allowedRoles (ej: Operario), registra un evento [SECURITY]
// y responde HTTP 403 Forbidden.
func RequireRole(allowedRoles []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaimsFromContext(r.Context())
		if claims == nil {
			response.Error(w, http.StatusUnauthorized, "Sesión requerida", nil)
			return
		}

		isAllowed := false
		for _, role := range allowedRoles {
			if claims.Role == role {
				isAllowed = true
				break
			}
		}

		if !isAllowed {
			log.Printf("[SECURITY] Acceso denegado (403 Forbidden) para usuario %s (rol: %s) en ruta %s", claims.Email, claims.Role, r.URL.Path)
			response.Error(w, http.StatusForbidden, "Acceso denegado: privilegios insuficientes", nil)
			return
		}

		next(w, r)
	}
}
