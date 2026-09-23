// Package requestjson centralizes strict controller-side DTO decoding.
//
// It bounds the raw body, requires a single JSON object, rejects duplicate
// members (recursively via the local decodeStrict helper), rejects null/array/
// scalar/trailing-document payloads, and enforces the exact JSON field-name set
// derived from the destination struct's tags so case aliases cannot bypass the
// boundary. It never validates business rules; callers keep doing that.
package requestjson

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// DefaultLimit is the conservative bound used when a caller passes no limit.
const DefaultLimit int64 = 8192

// Decode reads exactly one bounded JSON object from r.Body and decodes it into
// dst. On any violation it writes the controller 400 envelope and returns false.
// Content-Type checks stay in the handlers so their status/order is preserved.
func Decode(w http.ResponseWriter, r *http.Request, limit int64, dst interface{}) bool {
	if limit <= 0 {
		limit = DefaultLimit
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		response.Error(w, http.StatusBadRequest, "Formato JSON inválido o body demasiado grande", nil)
		return false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		response.Error(w, http.StatusBadRequest, "El body debe ser un objeto JSON", nil)
		return false
	}

	// decodeStrict rejects duplicate members at every nesting level and any
	// trailing document. Decoding into a map also lets us verify the exact
	// field-name set before the typed decode.
	var members map[string]json.RawMessage
	if err := decodeStrict(trimmed, &members); err != nil {
		response.Error(w, http.StatusBadRequest, "Formato JSON inválido", nil)
		return false
	}
	allowed := jsonFieldNames(dst)
	for name := range members {
		if _, ok := allowed[name]; !ok {
			response.Error(w, http.StatusBadRequest, "Formato JSON inválido", nil)
			return false
		}
	}
	if err := decodeStrict(trimmed, dst); err != nil {
		response.Error(w, http.StatusBadRequest, "Formato JSON inválido", nil)
		return false
	}
	return true
}

// jsonFieldNames derives the exact allowed JSON member names from the exported
// fields of dst's struct type, avoiding a second hard-coded list.
func jsonFieldNames(dst interface{}) map[string]struct{} {
	names := map[string]struct{}{}
	t := reflect.TypeOf(dst)
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return names
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // unexported
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		names[name] = struct{}{}
	}
	return names
}
