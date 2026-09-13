package deviceproof

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/angelobenedetti29/smart-check-automation/internal/guard"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// signedTargets is the explicit, canonical set of device-proof signed
// endpoints. Only a raw RequestURI byte-for-byte equal to one of these values
// may be admitted by the proof guard before ServeMux.
var signedTargets = []string{
	"/api/v1/dispositivos/ping",
	"/api/v1/lotes",
	"/api/v1/lotes/inicio",
	"/api/v1/dispositivos/provision",
	"/api/v1/dispositivos/enrollments/recover",
}

type enrollmentInputKey struct{}

// EnrollmentInput is the single, validated interpretation of a provision or
// recovery body. Controllers must consume this value rather than decode the
// raw body a second time.
type EnrollmentInput struct {
	Code        string
	PublicKey   dispositivo.PublicJWK
	Fingerprint string
}

// PrincipalFromContext returns the trusted principal installed by Middleware.
func PrincipalFromContext(ctx context.Context) (deviceauth.Principal, bool) {
	return deviceauth.PrincipalFromContext(ctx)
}

// EnrollmentInputFromContext returns the exact DTO authenticated by the proof.
func EnrollmentInputFromContext(ctx context.Context) (EnrollmentInput, bool) {
	x, ok := ctx.Value(enrollmentInputKey{}).(EnrollmentInput)
	return x, ok
}

// BeforeMux performs signed-route admission before ServeMux can normalize or
// redirect a path.
func BeforeMux(verifier *deviceauth.ProofVerifier, next http.Handler) http.Handler {
	return BeforeMuxWithDeviceLimiter(verifier, nil, next)
}

// BeforeMuxWithDeviceLimiter adds the authenticated-device quota after proof
// verification, keyed by the trusted UUID rather than the peer IP.
func BeforeMuxWithDeviceLimiter(verifier *deviceauth.ProofVerifier, deviceLimiter *guard.Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		// Classification only decides whether the request is routed into the
		// proof guard. Non-canonical variants (escaped aliases, dot segments,
		// duplicate/trailing slashes, queries, absolute-form) are routed in so
		// the guard can reject them before ServeMux normalizes or redirects.
		target, _, candidate := classifySignedRaw(r.RequestURI)
		if !candidate {
			next.ServeHTTP(w, r)
			return
		}
		enrollment := target == "/api/v1/dispositivos/provision" || target == "/api/v1/dispositivos/enrollments/recover"
		MiddlewareWithDeviceLimiter(verifier, enrollment, deviceLimiter, next).ServeHTTP(w, r)
	})
}

// Middleware validates target, raw body binding, JWS and durable replay before
// ServeMux handlers or node business code can run.
func Middleware(verifier *deviceauth.ProofVerifier, enrollment bool, next http.Handler) http.Handler {
	return MiddlewareWithDeviceLimiter(verifier, enrollment, nil, next)
}

// MiddlewareWithDeviceLimiter verifies proof and then applies a UUID quota.
func MiddlewareWithDeviceLimiter(verifier *deviceauth.ProofVerifier, enrollment bool, deviceLimiter *guard.Limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Acceptance requires the raw RequestURI to be byte-for-byte canonical.
		// r.URL.Path is decoded and is NOT sufficient: an escaped alias such as
		// /ping%70 or %70ing resolves to the canonical decoded path but must be
		// rejected. Query strings, trailing/bare separators and absolute-form
		// targets fail this equality as well.
		target, exact := canonicalSignedTarget(r.RequestURI)
		if r.Method != http.MethodPost || !exact || r.RequestURI == "" || r.RequestURI[0] != '/' || len(r.RequestURI) > 2048 {
			writeProofError(w, deviceauth.ErrInvalidProof)
			return
		}
		limit := int64(1 << 20)
		if target == "/api/v1/dispositivos/provision" || target == "/api/v1/dispositivos/enrollments/recover" {
			limit = 8192
		}
		authValues := r.Header.Values("Authorization")
		if len(authValues) != 1 || len(authValues[0]) > 4096 {
			writeProofError(w, deviceauth.ErrInvalidProof)
			return
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil || int64(len(b)) > limit {
			writeProofError(w, deviceauth.ErrInvalidProof)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		var p deviceauth.Principal
		var input EnrollmentInput
		var e error
		if enrollment {
			input, e = parseEnrollmentInput(b, target == "/api/v1/dispositivos/enrollments/recover")
			if e == nil {
				p, e = verifier.VerifyEnrollment(r.Context(), r, b, input.PublicKey)
			}
			if e != nil {
				writeProofError(w, e)
				return
			}
		} else {
			p, err = verifier.Verify(r.Context(), r, b)
			if err != nil {
				writeProofError(w, err)
				return
			}
		}
		if deviceLimiter != nil && !enrollment && !deviceLimiter.Allow("device", p.DeviceID) {
			guard.RateLimited(w)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		ctx := deviceauth.WithPrincipal(r.Context(), p)
		if enrollment {
			_, fp, _ := deviceauth.ParsePublicJWK(input.PublicKey)
			input.Fingerprint = fp
			ctx = context.WithValue(ctx, enrollmentInputKey{}, input)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// canonicalSignedTarget returns the canonical signed endpoint only when raw is
// byte-for-byte equal to it. This is the sole acceptance test.
func canonicalSignedTarget(raw string) (string, bool) {
	for _, target := range signedTargets {
		if raw == target {
			return target, true
		}
	}
	return "", false
}

// classifySignedRaw reports whether raw targets a signed endpoint once it is
// reduced, and whether it already matches a canonical target exactly. The
// reduced path is used ONLY to route non-canonical variants into the proof
// guard for rejection; it never proves acceptance.
func classifySignedRaw(raw string) (target string, exact bool, candidate bool) {
	if target, ok := canonicalSignedTarget(raw); ok {
		return target, true, true
	}
	reduced := reduceRawTarget(raw)
	for _, target := range signedTargets {
		if reduced == target {
			return target, false, true
		}
	}
	return "", false, false
}

// reduceRawTarget normalizes a raw target for classification only. It
// percent-decodes first so encoded separators are exposed, then strips
// fragments/queries, extracts absolute-form paths, collapses duplicate slashes
// and resolves dot segments so aliases cannot slip past the pre-mux guard.
func reduceRawTarget(raw string) string {
	if raw == "" {
		return ""
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		raw = raw[:i]
	}
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil {
			raw = u.Path
			if raw == "" {
				raw = "/"
			}
		}
	}
	for strings.Contains(raw, "//") {
		raw = strings.ReplaceAll(raw, "//", "/")
	}
	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == "" {
		cleaned = "/"
	}
	return cleaned
}

func parseEnrollmentInput(body []byte, recover bool) (EnrollmentInput, error) {
	var fields map[string]json.RawMessage
	if err := deviceauth.DecodeStrictJSON(body, &fields); err != nil {
		return EnrollmentInput{}, err
	}
	if recover {
		if len(fields) != 1 {
			return EnrollmentInput{}, deviceauth.ErrInvalidProof
		}
	} else if len(fields) != 2 {
		return EnrollmentInput{}, deviceauth.ErrInvalidProof
	}
	keyRaw, ok := fields["publicKey"]
	if !ok || string(keyRaw) == "null" {
		return EnrollmentInput{}, deviceauth.ErrInvalidProof
	}
	var keyFields map[string]json.RawMessage
	if err := deviceauth.DecodeStrictJSON(keyRaw, &keyFields); err != nil || len(keyFields) != 3 {
		return EnrollmentInput{}, deviceauth.ErrInvalidProof
	}
	for _, name := range []string{"kty", "crv", "x"} {
		if value, ok := keyFields[name]; !ok || string(value) == "null" {
			return EnrollmentInput{}, deviceauth.ErrInvalidProof
		}
	}
	var key dispositivo.PublicJWK
	if err := deviceauth.DecodeStrictJSON(keyRaw, &key); err != nil {
		return EnrollmentInput{}, err
	}
	if _, _, err := deviceauth.ParsePublicJWK(key); err != nil {
		return EnrollmentInput{}, err
	}
	input := EnrollmentInput{PublicKey: key}
	if !recover {
		raw, ok := fields["code"]
		if !ok || string(raw) == "null" || deviceauth.DecodeStrictJSON(raw, &input.Code) != nil || input.Code == "" {
			return EnrollmentInput{}, deviceauth.ErrInvalidProof
		}
	}
	return input, nil
}

func writeProofError(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "no-store")
	code := deviceauth.ErrorCode(err)
	status := http.StatusUnauthorized
	if code == "auth_store_unavailable" {
		status = http.StatusServiceUnavailable
	}
	response.Error(w, status, map[string]string{"invalid_device_proof": "Prueba de dispositivo inválida", "proof_replayed": "Prueba de dispositivo repetida", "auth_store_unavailable": "Servicio de autenticación temporalmente no disponible"}[code], map[string]string{"code": code})
}
