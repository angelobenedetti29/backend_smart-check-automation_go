// Package dualauth selecciona la credencial correcta para endpoints compartidos
// entre dispositivos Raspberry Pi (Bearer) y usuarios humanos (cookie).
package dualauth

import (
	"net/http"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
)

// DualAuth protege endpoints consumidos tanto por dispositivos como por usuarios
// humanos. Si la petición trae CUALQUIER header Authorization se resuelve
// exclusivamente como intento de dispositivo: ante fallo responde 401 plano sin
// caer en la cookie (evita degradación de credenciales). Sin Authorization
// delega en auth.JWTMiddleware, que valida la cookie `session_token` y emite el
// envelope 401 estándar cuando no hay sesión válida.
func DualAuth(verifier *deviceauth.Verifier, jwtSecret []byte, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("Authorization")) == 0 {
			authController.JWTMiddleware(jwtSecret, next)(w, r)
			return
		}

		var (
			principal deviceauth.Principal
			err       error
		)
		if verifier == nil {
			err = deviceauth.ErrInvalidToken
		} else {
			principal, err = verifier.Authenticate(r.Context(), r)
		}
		if err != nil {
			deviceauth.WriteInvalidToken(w)
			return
		}

		next(w, r.WithContext(deviceauth.WithPrincipal(r.Context(), principal)))
	}
}
