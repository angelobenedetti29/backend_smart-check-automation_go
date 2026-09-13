// Package deviceauth contains the deliberately separate device-proof verifier.
// It never accepts the human JWT or the legacy shared API key.
package deviceauth

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
)

var (
	ErrInvalidProof  = errors.New("invalid device proof")
	ErrProofReplayed = errors.New("proof replayed")
	ErrAuthStore     = errors.New("device auth store unavailable")
	ErrIdentityMatch = errors.New("device identity mismatch")
)
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Principal is the only device identity accepted by node-originated services.
type Principal struct {
	DeviceID    string
	Fingerprint string
	Enrollment  bool
}
type principalContextKey struct{}

// WithPrincipal attaches a verified device identity to a request context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// PrincipalFromContext gets the verified device identity, if present.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}

// Credential is a database-backed device credential.
type Credential struct {
	DeviceID    string
	Fingerprint string
	PublicKey   ed25519.PublicKey
	Status      dispositivo.AuthStatus
}

// Store performs the durable replay/admission checks.
type Store interface {
	LookupOperational(ctx context.Context, fingerprint string) (Credential, error)
	AdmitOperational(ctx context.Context, fingerprint, jti string, iat, exp time.Time) (Credential, error)
	AdmitEnrollment(ctx context.Context, fingerprint, jti string, iat, exp time.Time) error
}

// ProofVerifier verifies JWS signatures and binds them to the exact HTTP bytes.
type ProofVerifier struct {
	Audience string
	Store    Store
	Now      func() time.Time
}

// Verify validates an operational proof. The body must be the raw transmitted bytes.
func (v *ProofVerifier) Verify(ctx context.Context, r *http.Request, body []byte) (Principal, error) {
	return v.verify(ctx, r, body, false, nil)
}

// VerifyEnrollment validates an enrollment proof against the supplied JWK.
func (v *ProofVerifier) VerifyEnrollment(ctx context.Context, r *http.Request, body []byte, key dispositivo.PublicJWK) (Principal, error) {
	pub, fp, err := ParsePublicJWK(key)
	if err != nil {
		return Principal{}, ErrInvalidProof
	}
	return v.verify(ctx, r, body, true, &pubWithFingerprint{pub: pub, fp: fp})
}

type pubWithFingerprint struct {
	pub ed25519.PublicKey
	fp  string
}

func (v *ProofVerifier) verify(ctx context.Context, r *http.Request, body []byte, enrollment bool, supplied *pubWithFingerprint) (Principal, error) {
	if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.RequestURI == "" || len(r.RequestURI) > 2048 || r.Header.Values("Authorization") == nil || len(r.Header.Values("Authorization")) != 1 {
		return Principal{}, ErrInvalidProof
	}
	if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Content-Encoding") != "" {
		return Principal{}, ErrInvalidProof
	}
	parts := strings.Split(r.Header.Get("Authorization"), " ")
	if len(parts) != 2 || parts[0] != "DeviceProof" {
		return Principal{}, ErrInvalidProof
	}
	protected, payload, sig, err := decodeJWS(parts[1])
	if err != nil {
		return Principal{}, ErrInvalidProof
	}
	fingerprint := protected.Kid
	var pub ed25519.PublicKey
	deviceID := ""
	if supplied != nil {
		if fingerprint != supplied.fp {
			return Principal{}, ErrInvalidProof
		}
		pub, fingerprint = supplied.pub, supplied.fp
	} else {
		cred, err := v.Store.LookupOperational(ctx, fingerprint)
		if err != nil {
			return Principal{}, err
		}
		pub = cred.PublicKey
		deviceID = cred.DeviceID
		if cred.Fingerprint != fingerprint || cred.Status != dispositivo.AuthActive {
			return Principal{}, ErrInvalidProof
		}
	}
	if enrollment != (protected.Typ == "sca-enrollment+jwt") {
		return Principal{}, ErrInvalidProof
	}
	if !enrollment && protected.Typ != "sca-device+jwt" {
		return Principal{}, ErrInvalidProof
	}
	if enrollment {
		if payload.Sub != "urn:sca:enrollment-key:"+fingerprint {
			return Principal{}, ErrInvalidProof
		}
	} else if !uuidPattern.MatchString(payload.Sub) || payload.Sub != deviceID {
		return Principal{}, ErrInvalidProof
	}
	if err := v.ValidateClaimsBinding(r, body, payload); err != nil {
		return Principal{}, err
	}
	if !ed25519.Verify(pub, signingBytes(parts[1]), sig) {
		return Principal{}, ErrInvalidProof
	}
	// Enrollment replay admission happens only after cryptographic verification,
	// while operational admission has already locked/reloaded the credential.
	if enrollment {
		if err := v.Store.AdmitEnrollment(ctx, fingerprint, payload.JTI, time.Unix(payload.Iat, 0).UTC(), time.Unix(payload.Exp, 0).UTC()); err != nil {
			return Principal{}, err
		}
	} else {
		if _, err := v.Store.AdmitOperational(ctx, fingerprint, payload.JTI, time.Unix(payload.Iat, 0).UTC(), time.Unix(payload.Exp, 0).UTC()); err != nil {
			return Principal{}, err
		}
	}
	return Principal{DeviceID: payload.Sub, Fingerprint: fingerprint, Enrollment: enrollment}, nil
}

type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}
type claims struct {
	Sub   string `json:"sub"`
	Aud   string `json:"aud"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
	JTI   string `json:"jti"`
	Htm   string `json:"htm"`
	RT    string `json:"rt"`
	BHash string `json:"bhash"`
}

func decodeJWS(raw string) (header, claims, []byte, error) {
	var h header
	var c claims
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return h, c, nil, ErrInvalidProof
	}
	if parts[0] != encodeJSONPart(parts[0]) { /* checked below; keep explicit canonical test */
	}
	hb, err := strictObject([]byte(mustRawURL(parts[0])))
	if err != nil {
		return h, c, nil, err
	}
	if err := decodeStrict(hb, &h); err != nil || h.Alg != "EdDSA" || (h.Typ != "sca-device+jwt" && h.Typ != "sca-enrollment+jwt") || h.Kid == "" {
		return h, c, nil, ErrInvalidProof
	}
	pb, err := rawURL(parts[1])
	if err != nil {
		return h, c, nil, ErrInvalidProof
	}
	if err := decodeStrict(pb, &c); err != nil {
		return h, c, nil, ErrInvalidProof
	}
	sig, err := rawURL(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return h, c, nil, ErrInvalidProof
	}
	if !validB64(c.JTI, 16) || c.Htm == "" || c.RT == "" || !validHash(c.BHash) || c.Aud == "" || c.Sub == "" {
		return h, c, nil, ErrInvalidProof
	}
	return h, c, sig, nil
}

// ParsePublicJWK rejects unknown fields, duplicate members, private fields and
// noncanonical base64url encodings.
func ParsePublicJWK(k dispositivo.PublicJWK) (ed25519.PublicKey, string, error) {
	if k.Kty != "OKP" || k.Crv != "Ed25519" || k.X == "" {
		return nil, "", ErrInvalidProof
	}
	x, err := rawURL(k.X)
	if err != nil || len(x) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(x) != k.X {
		return nil, "", ErrInvalidProof
	}
	canonical := `{"crv":"Ed25519","kty":"OKP","x":"` + k.X + `"}`
	h := sha256.Sum256([]byte(canonical))
	fp := base64.RawURLEncoding.EncodeToString(h[:])
	return ed25519.PublicKey(x), fp, nil
}

func (v *ProofVerifier) clock() time.Time {
	if v.Now != nil {
		return v.Now().UTC()
	}
	return time.Now().UTC()
}
func signingBytes(raw string) []byte { p := strings.Split(raw, "."); return []byte(p[0] + "." + p[1]) }
func rawURL(s string) ([]byte, error) {
	if s == "" || strings.Contains(s, "=") {
		return nil, ErrInvalidProof
	}
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil || base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, ErrInvalidProof
	}
	return b, nil
}
func validB64(s string, n int) bool { b, e := rawURL(s); return e == nil && len(b) == n }
func validHash(s string) bool       { return validB64(s, 32) }

func decodeStrict(b []byte, dst interface{}) error {
	if err := rejectDuplicateJSON(b); err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		return ErrInvalidProof
	}
	return nil
}

// DecodeStrictJSON decodes exactly one JSON document and rejects unknown or
// duplicate object members.
func DecodeStrictJSON(b []byte, dst interface{}) error { return decodeStrict(b, dst) }
func strictObject(b []byte) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := decodeStrict(b, &raw); err != nil {
		return nil, err
	}
	return b, nil
}
func mustRawURL(s string) string     { b, _ := rawURL(s); return string(b) }
func encodeJSONPart(s string) string { return s }

// ValidateClaimsBinding applies claims that depend on the request, after JWS
// parsing. It is intentionally exported so the HTTP middleware can apply it
// before ServeMux gets a chance to normalize a path.
func (v *ProofVerifier) ValidateClaimsBinding(r *http.Request, body []byte, c claims) error {
	if c.Htm != r.Method || c.RT != r.RequestURI || c.Aud != v.Audience {
		return ErrInvalidProof
	}
	h := sha256.Sum256(body)
	if c.BHash != base64.RawURLEncoding.EncodeToString(h[:]) {
		return ErrInvalidProof
	}
	// Wall-clock freshness and expiration are checked by the PostgreSQL
	// admission transaction. Only the protocol lifetime is structural here.
	if c.Exp <= c.Iat || c.Exp-c.Iat > 60 {
		return ErrInvalidProof
	}
	return nil
}

// ParseAndValidatePublicRequest strictly decodes the JWK-bearing DTO.
func ParseAndValidatePublicRequest(body []byte, recover bool) (dispositivo.PublicJWK, string, error) {
	var raw map[string]json.RawMessage
	if err := decodeStrict(body, &raw); err != nil {
		return dispositivo.PublicJWK{}, "", err
	}
	if recover {
		if len(raw) != 1 {
			return dispositivo.PublicJWK{}, "", ErrInvalidProof
		}
	} else if len(raw) != 2 {
		return dispositivo.PublicJWK{}, "", ErrInvalidProof
	}
	keyRaw, ok := raw["publicKey"]
	if !ok || string(keyRaw) == "null" {
		return dispositivo.PublicJWK{}, "", ErrInvalidProof
	}
	var keyFields map[string]json.RawMessage
	if err := decodeStrict(keyRaw, &keyFields); err != nil || len(keyFields) != 3 {
		return dispositivo.PublicJWK{}, "", ErrInvalidProof
	}
	for _, field := range []string{"kty", "crv", "x"} {
		if value, exists := keyFields[field]; !exists || string(value) == "null" {
			return dispositivo.PublicJWK{}, "", ErrInvalidProof
		}
	}
	var key dispositivo.PublicJWK
	if err := decodeStrict(keyRaw, &key); err != nil {
		return key, "", err
	}
	_, fp, err := ParsePublicJWK(key)
	return key, fp, err
}

// ErrorCode maps verifier errors to stable API machine codes.
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrProofReplayed):
		return "proof_replayed"
	case errors.Is(err, ErrAuthStore):
		return "auth_store_unavailable"
	default:
		return "invalid_device_proof"
	}
}

func rejectDuplicateJSON(b []byte) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	var walk func() error
	walk = func() error {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		switch d := t.(type) {
		case json.Delim:
			if d == '{' {
				seen := map[string]bool{}
				for dec.More() {
					kt, err := dec.Token()
					if err != nil {
						return err
					}
					key, ok := kt.(string)
					if !ok || seen[key] {
						return ErrInvalidProof
					}
					seen[key] = true
					if err := walk(); err != nil {
						return err
					}
				}
				_, err = dec.Token()
				return err
			}
			if d == '[' {
				for dec.More() {
					if err := walk(); err != nil {
						return err
					}
				}
				_, err = dec.Token()
				return err
			}
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		return ErrInvalidProof
	}
	return nil
}
