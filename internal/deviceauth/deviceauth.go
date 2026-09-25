// Package deviceauth resuelve la identidad de un dispositivo Raspberry Pi a
// partir de un secret Bearer opaco. Nunca acepta el JWT humano ni la antigua
// API key compartida.
package deviceauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// ErrInvalidToken indica que el token de dispositivo es ausente, malformado o
// no corresponde a ningún dispositivo activo.
var ErrInvalidToken = errors.New("token de dispositivo inválido")

// maxTokenBytes acota el tamaño del secret aceptado para evitar hashing de
// payloads arbitrariamente grandes.
const maxTokenBytes = 512

// Principal es la única identidad de dispositivo aceptada por los servicios
// originados por nodos.
type Principal struct {
	DeviceID string
}

// principalContextKey es la clave privada usada para transportar el Principal.
type principalContextKey struct{}

// WithPrincipal adjunta una identidad de dispositivo verificada al contexto.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// PrincipalFromContext recupera la identidad de dispositivo verificada, si existe.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}

// HashSecret devuelve el SHA-256 hexadecimal en minúsculas del secret recibido.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Store resuelve el dispositivo activo a partir del hash de su secret.
type Store interface {
	LookupActiveBySecretHash(ctx context.Context, secretHash string) (string, error)
}

// Verifier autentica peticiones de dispositivo contra un Store de secrets.
type Verifier struct {
	Store Store
}

// Authenticate valida el header Authorization: Bearer <secret> y devuelve el
// Principal del dispositivo activo. Cualquier fallo se normaliza a
// ErrInvalidToken para no filtrar la causa al cliente.
func (v *Verifier) Authenticate(ctx context.Context, r *http.Request) (Principal, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return Principal{}, ErrInvalidToken
	}
	parts := strings.Split(values[0], " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return Principal{}, ErrInvalidToken
	}
	token := parts[1]
	if !validToken(token) {
		return Principal{}, ErrInvalidToken
	}

	deviceID, err := v.Store.LookupActiveBySecretHash(ctx, HashSecret(token))
	if err != nil {
		if errors.Is(err, ErrInvalidToken) {
			return Principal{}, ErrInvalidToken
		}
		return Principal{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if deviceID == "" {
		return Principal{}, ErrInvalidToken
	}
	return Principal{DeviceID: deviceID}, nil
}

// validToken exige un secret no vacío, acotado y sin espacios ni caracteres de
// control (el split por espacio ya descarta el separador del esquema).
func validToken(token string) bool {
	if token == "" || len(token) > maxTokenBytes {
		return false
	}
	for _, r := range token {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
