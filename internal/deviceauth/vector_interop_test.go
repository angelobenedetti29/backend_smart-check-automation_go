package deviceauth

// Cross-language interop test. It loads the deterministic DeviceProof vector
// committed by the Raspberry Python client
// (raspberry_front_back_smart-check/backend/tests/vectors/device_proof_vector.json)
// and verifies it with the REAL Go verifier. The vector's iat is frozen, so the
// only DB-time coupling (AdmitOperational/AdmitEnrollment) is replaced by an
// in-memory admission stub here. ValidateClaimsBinding and the Ed25519 signature
// are time-independent, so production admission logic is never weakened or
// bypassed in product code.

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
)

const committedVectorPath = "testdata/device_proof_vector.json"

type vectorCase struct {
	Typ     string            `json:"typ"`
	Method  string            `json:"method"`
	Target  string            `json:"target"`
	Body    string            `json:"body"`
	Subject string            `json:"subject"`
	Header  map[string]string `json:"header"`
	JWS     string            `json:"jws"`
}

type vectorClaims struct {
	Sub   string `json:"sub"`
	Aud   string `json:"aud"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
	Jti   string `json:"jti"`
	Htm   string `json:"htm"`
	RT    string `json:"rt"`
	BHash string `json:"bhash"`
}

type committedVector struct {
	PublicJwk   dispositivo.PublicJWK `json:"publicJwk"`
	Fingerprint string                `json:"fingerprint"`
	Audience    string                `json:"audience"`
	Iat         int64                 `json:"iat"`
	Exp         int64                 `json:"exp"`
	Jti         string                `json:"jti"`
	Operational vectorCase            `json:"operational"`
	Enrollment  vectorCase            `json:"enrollment"`
}

// interopStore is an in-memory admission stub so a frozen-iat vector can be
// replayed. It never changes production code; it only replaces the PostgreSQL
// transaction that enforces wall-clock admission.
type interopStore struct {
	cred Credential
}

func (s *interopStore) LookupOperational(context.Context, string) (Credential, error) {
	return s.cred, nil
}
func (s *interopStore) AdmitOperational(context.Context, string, string, time.Time, time.Time) (Credential, error) {
	return s.cred, nil
}
func (s *interopStore) AdmitEnrollment(context.Context, string, string, time.Time, time.Time) error {
	return nil
}

func loadCommittedVector(t *testing.T) committedVector {
	t.Helper()
	raw, err := os.ReadFile(committedVectorPath)
	if err != nil {
		t.Fatalf("read committed Python vector: %v", err)
	}
	var v committedVector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse committed Python vector: %v", err)
	}
	return v
}

func rawURLPart(t *testing.T, part string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		t.Fatalf("decode base64url part: %v", err)
	}
	return b
}

func vectorRequest(t *testing.T, method, target, jws string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, "http://interop.test"+target, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	r.RequestURI = target
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "DeviceProof "+jws)
	return r
}

// TestCommittedPythonVectorInterop verifies the exact Python-produced JWS with
// the real verifier and independently checks every binding the contract names.
func TestCommittedPythonVectorInterop(t *testing.T) {
	v := loadCommittedVector(t)

	// Go's own RFC7638 derivation must match the client-published fingerprint.
	pub, fp, err := ParsePublicJWK(v.PublicJwk)
	if err != nil {
		t.Fatalf("ParsePublicJWK(vector.publicJwk): %v", err)
	}
	if fp != v.Fingerprint {
		t.Fatalf("Go RFC7638 fingerprint=%q want vector %q", fp, v.Fingerprint)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("public key size=%d want %d", len(pub), ed25519.PublicKeySize)
	}

	store := &interopStore{cred: Credential{
		DeviceID:    v.Operational.Subject,
		Fingerprint: v.Fingerprint,
		PublicKey:   pub,
		Status:      dispositivo.AuthActive,
	}}
	verifier := &ProofVerifier{Audience: v.Audience, Store: store}

	t.Run("operational_sca-device+jwt", func(t *testing.T) {
		assertVectorCase(t, v, v.Operational, pub, store)
		p, err := verifier.Verify(context.Background(), vectorRequest(t, v.Operational.Method, v.Operational.Target, v.Operational.JWS), []byte(v.Operational.Body))
		if err != nil {
			t.Fatalf("real verifier rejected committed operational proof: %v", err)
		}
		if p.Enrollment || p.DeviceID != v.Operational.Subject || p.Fingerprint != v.Fingerprint {
			t.Fatalf("unexpected principal: %+v", p)
		}
	})

	t.Run("enrollment_sca-enrollment+jwt", func(t *testing.T) {
		assertVectorCase(t, v, v.Enrollment, pub, store)
		p, err := verifier.VerifyEnrollment(context.Background(), vectorRequest(t, v.Enrollment.Method, v.Enrollment.Target, v.Enrollment.JWS), []byte(v.Enrollment.Body), v.PublicJwk)
		if err != nil {
			t.Fatalf("real verifier rejected committed enrollment proof: %v", err)
		}
		if !p.Enrollment || p.DeviceID != v.Enrollment.Subject || p.Fingerprint != v.Fingerprint {
			t.Fatalf("unexpected principal: %+v", p)
		}
	})

	t.Run("rejections_are_byte_bound", func(t *testing.T) {
		body := []byte(v.Operational.Body)
		r := vectorRequest(t, v.Operational.Method, v.Operational.Target, v.Operational.JWS)
		if _, err := verifier.Verify(context.Background(), r, []byte("{}")); !errors.Is(err, ErrInvalidProof) {
			t.Fatalf("tampered body got err=%v want ErrInvalidProof", err)
		}
		r = vectorRequest(t, v.Operational.Method, "/api/v1/lotes", v.Operational.JWS)
		if _, err := verifier.Verify(context.Background(), r, body); !errors.Is(err, ErrInvalidProof) {
			t.Fatalf("tampered path got err=%v want ErrInvalidProof", err)
		}
		wrongAud := &ProofVerifier{Audience: v.Audience + "-other", Store: store}
		r = vectorRequest(t, v.Operational.Method, v.Operational.Target, v.Operational.JWS)
		if _, err := wrongAud.Verify(context.Background(), r, body); !errors.Is(err, ErrInvalidProof) {
			t.Fatalf("wrong audience got err=%v want ErrInvalidProof", err)
		}
	})
}

// assertVectorCase checks header, claims, bhash, signature and fingerprint
// without relying on the verifier under test.
func assertVectorCase(t *testing.T, v committedVector, c vectorCase, pub ed25519.PublicKey, store *interopStore) {
	t.Helper()
	parts := strings.Split(c.JWS, ".")
	if len(parts) != 3 {
		t.Fatalf("compact JWS has %d parts want 3", len(parts))
	}
	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(rawURLPart(t, parts[0]), &hdr); err != nil {
		t.Fatalf("decode protected header: %v", err)
	}
	if hdr.Alg != "EdDSA" {
		t.Fatalf("alg=%q want EdDSA", hdr.Alg)
	}
	if hdr.Typ != c.Typ {
		t.Fatalf("typ=%q want %q", hdr.Typ, c.Typ)
	}
	if hdr.Kid != v.Fingerprint {
		t.Fatalf("kid=%q want fingerprint %q", hdr.Kid, v.Fingerprint)
	}
	if c.Header["alg"] != hdr.Alg || c.Header["typ"] != hdr.Typ || c.Header["kid"] != hdr.Kid {
		t.Fatalf("header mismatch: vector=%v decoded=%+v", c.Header, hdr)
	}

	var claims vectorClaims
	if err := json.Unmarshal(rawURLPart(t, parts[1]), &claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	bodySum := sha256.Sum256([]byte(c.Body))
	wantBHash := base64.RawURLEncoding.EncodeToString(bodySum[:])
	if claims.BHash != wantBHash {
		t.Fatalf("bhash=%q want SHA-256(body)=%q", claims.BHash, wantBHash)
	}
	if claims.Htm != c.Method || claims.Htm != "POST" {
		t.Fatalf("htm=%q want method POST", claims.Htm)
	}
	if claims.RT != c.Target {
		t.Fatalf("rt=%q want target %q", claims.RT, c.Target)
	}
	if claims.Aud != v.Audience {
		t.Fatalf("aud=%q want audience %q", claims.Aud, v.Audience)
	}
	if claims.Sub != c.Subject {
		t.Fatalf("sub=%q want subject %q", claims.Sub, c.Subject)
	}
	if claims.Iat != v.Iat || claims.Exp != v.Exp || claims.Jti != v.Jti {
		t.Fatalf("time/jti mismatch: got iat=%d exp=%d jti=%q want iat=%d exp=%d jti=%q", claims.Iat, claims.Exp, claims.Jti, v.Iat, v.Exp, v.Jti)
	}
	if len(rawURLPart(t, claims.Jti)) != 16 {
		t.Fatalf("jti is not 16 bytes")
	}

	sig := rawURLPart(t, parts[2])
	if len(sig) != ed25519.SignatureSize {
		t.Fatalf("signature size=%d want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		t.Fatalf("Ed25519 signature over header.payload does not verify with vector public key")
	}
}
