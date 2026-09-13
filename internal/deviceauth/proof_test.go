package deviceauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
)

// proofStore is a recording Store. The counters let tests assert the exact
// rejection stage (no credential lookup, no admission) instead of merely
// asserting "some error occurred".
type proofStore struct {
	cred       Credential
	replay     error
	lookups    int
	admissions int
}

func (s *proofStore) LookupOperational(context.Context, string) (Credential, error) {
	s.lookups++
	return s.cred, nil
}
func (s *proofStore) AdmitOperational(context.Context, string, string, time.Time, time.Time) (Credential, error) {
	s.admissions++
	return s.cred, s.replay
}
func (s *proofStore) AdmitEnrollment(context.Context, string, string, time.Time, time.Time) error {
	s.admissions++
	return s.replay
}

// signingKey is one generated Ed25519 identity reused across a whole test so
// that every proof variant is signed by the credential actually stored.
type signingKey struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	fp   string
	x    string
}

func newSigningKey(t *testing.T) signingKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	x := base64.RawURLEncoding.EncodeToString(pub)
	canonical := `{"crv":"Ed25519","kty":"OKP","x":"` + x + `"}`
	sum := sha256.Sum256([]byte(canonical))
	return signingKey{pub: pub, priv: priv, fp: base64.RawURLEncoding.EncodeToString(sum[:]), x: x}
}

func (k signingKey) publicJWK() dispositivo.PublicJWK {
	return dispositivo.PublicJWK{Kty: "OKP", Crv: "Ed25519", X: k.x}
}

func baseClaims(body []byte, mutate func(map[string]interface{})) map[string]interface{} {
	now := time.Now().Unix()
	bh := sha256.Sum256(body)
	claims := map[string]interface{}{
		"sub":   "00000000-0000-0000-0000-000000000001",
		"aud":   "aud",
		"iat":   now,
		"exp":   now + 60,
		"jti":   base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef")),
		"htm":   "POST",
		"rt":    "/api/v1/dispositivos/ping",
		"bhash": base64.RawURLEncoding.EncodeToString(bh[:]),
	}
	if mutate != nil {
		mutate(claims)
	}
	return claims
}

// signRaw signs an already-serialized protected header and payload, allowing a
// test to build malformed-but-validly-signed JWS documents.
func signRaw(t *testing.T, k signingKey, headerBytes, claimsBytes []byte) string {
	t.Helper()
	enc := base64.RawURLEncoding.EncodeToString
	raw := enc(headerBytes) + "." + enc(claimsBytes)
	return raw + "." + enc(ed25519.Sign(k.priv, []byte(raw)))
}

func proofRequest(t *testing.T, proof string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, "http://example.test/api/v1/dispositivos/ping", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	r.RequestURI = "/api/v1/dispositivos/ping"
	r.Header.Set("Authorization", "DeviceProof "+proof)
	r.Header.Set("Content-Type", "application/json")
	return r
}

// signedRequestWithKey signs a proof for the supplied claims using one fixed
// key, so a mutated-claims rejection proves the intended binding check rather
// than a fingerprint mismatch.
func signedRequestWithKey(t *testing.T, k signingKey, typ, alg string, mutate func(map[string]interface{}), body []byte) *http.Request {
	t.Helper()
	hb, err := json.Marshal(map[string]string{"alg": alg, "typ": typ, "kid": k.fp})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := json.Marshal(baseClaims(body, mutate))
	if err != nil {
		t.Fatal(err)
	}
	return proofRequest(t, signRaw(t, k, hb, pb))
}

func signedRequest(t *testing.T, typ, alg string, mutate func(map[string]interface{}), body []byte) (*http.Request, signingKey) {
	t.Helper()
	k := newSigningKey(t)
	return signedRequestWithKey(t, k, typ, alg, mutate, body), k
}

func TestProofRejectsTamperAudienceTargetAndBody(t *testing.T) {
	body := []byte(`{"dispositivoId":"00000000-0000-0000-0000-000000000001"}`)
	k := newSigningKey(t)
	s := &proofStore{cred: Credential{DeviceID: "00000000-0000-0000-0000-000000000001", Fingerprint: k.fp, PublicKey: k.pub, Status: dispositivo.AuthActive}}
	v := &ProofVerifier{Audience: "aud", Store: s}
	if _, err := v.Verify(context.Background(), signedRequestWithKey(t, k, "sca-device+jwt", "EdDSA", nil, body), body); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}
	if s.admissions != 1 {
		t.Fatalf("valid proof admissions=%d want 1", s.admissions)
	}
	for name, mutate := range map[string]func(map[string]interface{}){
		"audience": func(c map[string]interface{}) { c["aud"] = "other" },
		"target":   func(c map[string]interface{}) { c["rt"] = "/api/v1/lotes" },
		"body":     func(c map[string]interface{}) { c["bhash"] = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" },
	} {
		t.Run(name, func(t *testing.T) {
			s.admissions = 0
			s.lookups = 0
			r := signedRequestWithKey(t, k, "sca-device+jwt", "EdDSA", mutate, body)
			if _, err := v.Verify(context.Background(), r, body); !errors.Is(err, ErrInvalidProof) {
				t.Fatalf("expected ErrInvalidProof, got %v", err)
			}
			if s.admissions != 0 {
				t.Fatalf("tampered proof reached admission %d time(s)", s.admissions)
			}
		})
	}
}

func TestProofRejectsDuplicateAndUnknownHeaders(t *testing.T) {
	body := []byte(`{}`)
	k := newSigningKey(t)
	s := &proofStore{cred: Credential{DeviceID: "00000000-0000-0000-0000-000000000001", Fingerprint: k.fp, PublicKey: k.pub, Status: dispositivo.AuthActive}}
	v := &ProofVerifier{Audience: "aud", Store: s}

	validHeader, err := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "sca-device+jwt", "kid": k.fp})
	if err != nil {
		t.Fatal(err)
	}
	validClaims, err := json.Marshal(baseClaims(body, nil))
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		// A validly re-signed but non-canonical header must be rejected at
		// strict header decoding, before any credential lookup.
		"unknown_header_member_resigned":   signRaw(t, k, []byte(`{"alg":"EdDSA","typ":"sca-device+jwt","kid":"`+k.fp+`","extra":1}`), validClaims),
		"duplicate_header_member_resigned": signRaw(t, k, []byte(`{"alg":"EdDSA","typ":"sca-device+jwt","kid":"`+k.fp+`","kid":"`+k.fp+`"}`), validClaims),
	}
	for name, proof := range cases {
		t.Run(name, func(t *testing.T) {
			s.lookups, s.admissions = 0, 0
			if _, err := v.Verify(context.Background(), proofRequest(t, proof), body); !errors.Is(err, ErrInvalidProof) {
				t.Fatalf("expected ErrInvalidProof, got %v", err)
			}
			if s.lookups != 0 || s.admissions != 0 {
				t.Fatalf("malformed header reached lookup=%d admission=%d", s.lookups, s.admissions)
			}
		})
	}

	t.Run("duplicate_claims_member_resigned", func(t *testing.T) {
		s.lookups, s.admissions = 0, 0
		now := time.Now().Unix()
		bh := sha256.Sum256(body)
		claimsJSON := `{"sub":"00000000-0000-0000-0000-000000000001","sub":"00000000-0000-0000-0000-000000000001",` +
			`"aud":"aud","iat":` + strconv.FormatInt(now, 10) + `,"exp":` + strconv.FormatInt(now+60, 10) +
			`,"jti":"` + base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef")) +
			`","htm":"POST","rt":"/api/v1/dispositivos/ping","bhash":"` + base64.RawURLEncoding.EncodeToString(bh[:]) + `"}`
		if _, err := v.Verify(context.Background(), proofRequest(t, signRaw(t, k, validHeader, []byte(claimsJSON))), body); !errors.Is(err, ErrInvalidProof) {
			t.Fatalf("expected ErrInvalidProof, got %v", err)
		}
		if s.admissions != 0 {
			t.Fatalf("duplicate claims reached admission %d time(s)", s.admissions)
		}
	})
}

func TestProofRejectsAlgorithmWithSameCredentialKey(t *testing.T) {
	body := []byte(`{}`)
	k := newSigningKey(t)
	s := &proofStore{cred: Credential{DeviceID: "00000000-0000-0000-0000-000000000001", Fingerprint: k.fp, PublicKey: k.pub, Status: dispositivo.AuthActive}}
	r := signedRequestWithKey(t, k, "sca-device+jwt", "HS256", nil, body)
	if _, err := (&ProofVerifier{Audience: "aud", Store: s}).Verify(context.Background(), r, body); !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("HS256 proof accepted: %v", err)
	}
	if s.lookups != 0 || s.admissions != 0 {
		t.Fatalf("HS256 reached lookup=%d admission=%d", s.lookups, s.admissions)
	}
}

func TestParsePublicJWKRejectsAliasesUnknownAndDuplicateMembers(t *testing.T) {
	k := newSigningKey(t)
	validKey := `{"kty":"OKP","crv":"Ed25519","x":"` + k.x + `"}`
	cases := map[string]string{
		"alias_public_key":     `{"code":"x","PublicKey":` + validKey + `}`,
		"unknown_top_level":    `{"code":"x","publicKey":` + validKey + `,"extra":1}`,
		"unknown_key_member":   `{"code":"x","publicKey":{"kty":"OKP","crv":"Ed25519","x":"` + k.x + `","kid":"y"}}`,
		"duplicate_key_member": `{"code":"x","publicKey":{"kty":"OKP","crv":"Ed25519","x":"` + k.x + `","x":"` + k.x + `"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseAndValidatePublicRequest([]byte(body), false); !errors.Is(err, ErrInvalidProof) {
				t.Fatalf("expected ErrInvalidProof, got %v", err)
			}
		})
	}
	if _, fp, err := ParseAndValidatePublicRequest([]byte(`{"code":"x","publicKey":`+validKey+`}`), false); err != nil || fp != k.fp {
		t.Fatalf("canonical JWK rejected: fp=%s want=%s err=%v", fp, k.fp, err)
	}
}
