// Package devicetoken autentica las peticiones originadas por nodos Raspberry Pi
// mediante un secret Bearer por dispositivo antes de que lleguen al ServeMux.
package devicetoken

import (
	"net/http"
	"regexp"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/guard"
)

// protectedExact es el conjunto explícito de endpoints device-only ("MÉTODO ruta")
// de coincidencia exacta que requieren autenticación por token de dispositivo.
var protectedExact = map[string]struct{}{
	"POST /api/v1/dispositivos/ping":  {},
	"PUT /api/v1/dispositivos/nombre": {},
	"POST /api/v1/lotes/inicio":       {},
	"GET /api/v1/dispositivos/sector": {},
}

// loteEventPattern cubre POST /api/v1/lotes/{id}/eventos y .../{id}/cierre, donde
// {id} es un único segmento no vacío. Deliberadamente NO coincide con
// /api/v1/lotes/inicio, /api/v1/lotes/abierto ni /api/v1/lotes/events (esos
// últimos se resuelven por match exacto o quedan fuera del set device-only).
var loteEventPattern = regexp.MustCompile(`^/api/v1/lotes/[^/]+/(eventos|cierre)$`)

// isProtectedTarget indica si la petición pertenece al conjunto device-only:
// coincidencia exacta más el patrón POST /lotes/{id}/(eventos|cierre).
//
// path es la ruta decodificada (r.URL.Path) y se usa para el lookup exacto.
// escapedPath es la ruta escapada (r.URL.EscapedPath()) y se usa para el patrón:
// ServeMux enruta por la forma escapada y desescapa por segmento, de modo que un
// id con slash escapado (a%2Fb) llega al handler como un único segmento "a/b".
// Matchear sobre la ruta decodificada dejaría pasar sin autenticar ese caso.
func isProtectedTarget(method, path, escapedPath string) bool {
	if _, exact := protectedExact[method+" "+path]; exact {
		return true
	}
	return method == http.MethodPost && loteEventPattern.MatchString(escapedPath)
}

// invalidTokenBody es la respuesta plana (sin envelope) exigida para un token
// de dispositivo inválido.
const invalidTokenBody = `{"error":"Token de dispositivo inválido","code":"invalid_device_token"}`

// BeforeMux autentica las rutas de dispositivo antes de ServeMux. Las rutas no
// protegidas pasan sin modificar. Ante un token inválido responde 401 con un
// error plano; ante exceso de cuota responde 429.
func BeforeMux(verifier *deviceauth.Verifier, limiter *guard.Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isProtectedTarget(r.Method, r.URL.Path, r.URL.EscapedPath()) {
			next.ServeHTTP(w, r)
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
			writeInvalidToken(w)
			return
		}

		if limiter != nil && !limiter.Allow("device", principal.DeviceID) {
			guard.RateLimited(w)
			return
		}

		next.ServeHTTP(w, r.WithContext(deviceauth.WithPrincipal(r.Context(), principal)))
	})
}

// writeInvalidToken escribe el error plano 401 con Cache-Control: no-store.
func writeInvalidToken(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(invalidTokenBody))
}
