package auth

import (
	"log"
	"net/http"

	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// APIKeyMiddleware protege rutas HTTP verificando el header X-API-Key.
// Diseñado para autenticación M2M (Machine-to-Machine) de clientes embebidos
// como la Raspberry Pi, que no pueden usar cookies de sesión OAuth/JWT.
//
// El header es comparado directamente contra el secret configurado en la
// variable de entorno API_KEY_SECRET. Si falta o es incorrecto, registra
// el evento con [SECURITY] y retorna 401 Unauthorized sin exponer detalles.
func APIKeyMiddleware(secret string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")

		if key == "" {
			response.Error(w, http.StatusUnauthorized, "API key requerida", nil)
			return
		}

		if key != secret {
			log.Printf("[SECURITY] API key inválida — IP: %s, Path: %s", r.RemoteAddr, r.URL.Path)
			response.Error(w, http.StatusUnauthorized, "API key inválida", nil)
			return
		}

		next(w, r)
	}
}
