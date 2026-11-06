// Package helpers concentra funciones comunes a varios handlers del API:
//
//   - ExtractLoteID: parsea el {id} de /api/v1/lotes/{id}/<suffix>. Antes
//     vivía duplicado en lote_sector_handler (string), control_handler
//     (string, error) y decisiones_handler (string, error). Aquí vive la
//     versión canónica (string, error) que ambos callers usan.
//   - RolValido: compara el rol del JWT contra los permitidos.
//
// Moverlos aquí evita drift entre handlers (los tres chequeaban lo mismo
// con ligeras variaciones).
package helpers

import (
	"net/http"
	"strings"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
)

// ErrLoteIDInvalido se devuelve cuando el path no matchea el patrón esperado
// o contiene segmentos adicionales. Los callers lo traducen a 404.
var ErrLoteIDInvalido = lotesector.ErrNotFound

// ExtractLoteID obtiene el {id} de /api/v1/lotes/{id}/<suffix>. Devuelve
// ErrLoteIDInvalido cuando el path no calza con el patrón, el id está vacío
// o contiene segmentos anidados.
func ExtractLoteID(path, suffix string) (string, error) {
	const prefix = "/api/v1/lotes/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", ErrLoteIDInvalido
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	mid = strings.Trim(mid, "/")
	if mid == "" || strings.Contains(mid, "/") {
		return "", ErrLoteIDInvalido
	}
	return mid, nil
}

// RolValido devuelve true si el rol del JWT actual está dentro de la lista
// de roles permitidos. Requiere que el middleware JWT ya haya corrido.
func RolValido(r *http.Request, roles ...string) bool {
	claims := authController.GetClaimsFromContext(r.Context())
	if claims == nil {
		return false
	}
	for _, rol := range roles {
		if claims.Role == rol {
			return true
		}
	}
	return false
}
