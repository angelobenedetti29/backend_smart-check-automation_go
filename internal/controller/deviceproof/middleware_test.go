package deviceproof

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
)

func TestParseEnrollmentInputExactPublicKeyAliasesAndMemberOrder(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	x := base64.RawURLEncoding.EncodeToString(pub)
	cases := []struct {
		name, body string
		want       bool
	}{
		{"canonical", `{"code":"a-code","publicKey":{"kty":"OKP","crv":"Ed25519","x":"` + x + `"}}`, true},
		{"reordered-canonical", `{"publicKey":{"x":"` + x + `","crv":"Ed25519","kty":"OKP"},"code":"a-code"}`, true},
		{"snake-case-alias", `{"code":"a-code","public_key":{"kty":"OKP","crv":"Ed25519","x":"` + x + `"}}`, false},
		{"case-alias", `{"code":"a-code","PublicKey":{"kty":"OKP","crv":"Ed25519","x":"` + x + `"}}`, false},
		{"jwk-case-alias", `{"code":"a-code","publicKey":{"Kty":"OKP","crv":"Ed25519","x":"` + x + `"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseEnrollmentInput([]byte(tc.body), false)
			if (err == nil) != tc.want {
				t.Fatalf("accepted=%v error=%v, want=%v", err == nil, err, tc.want)
			}
		})
	}
}

func TestBeforeMuxRejectsRawTargetVariantsBeforeServeMux(t *testing.T) {
	for _, target := range []string{"/api/v1/dispositivos/ping?", "/api/v1/dispositivos/ping/", "/api/v1/dispositivos/ping%2F", "/api/v1/dispositivos//ping", "/api/v1/dispositivos/./ping", "/api/v1/dispositivos/%70ing", "/api/v1/dispositivos/ping%3Ffoo=1", "http://example.test/api/v1/dispositivos/ping"} {
		t.Run(target, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
			r := httptest.NewRequest(http.MethodPost, target, nil)
			r.RequestURI = target
			r.Header.Set("Authorization", "DeviceProof malformed")
			r.Header.Set("Content-Type", "application/json")
			rw := httptest.NewRecorder()
			BeforeMux(nil, next).ServeHTTP(rw, r)
			if called {
				t.Fatal("target reached mux handler")
			}
			if rw.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d, want 401", rw.Code)
			}
		})
	}
}

func TestNodeRoutesRejectAPIKeyAndHumanJWTWithoutProof(t *testing.T) {
	for _, path := range []string{"/api/v1/dispositivos/ping", "/api/v1/lotes", "/api/v1/lotes/inicio"} {
		for _, auth := range []string{"api-key-only", "Bearer human-jwt"} {
			t.Run(path+"/"+auth, func(t *testing.T) {
				called := false
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
				r.RequestURI = path
				r.Header.Set("Content-Type", "application/json")
				if auth == "api-key-only" {
					r.Header.Set("X-API-Key", "legacy-secret")
				} else {
					r.Header.Set("Authorization", auth)
				}
				rw := httptest.NewRecorder()
				BeforeMux(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(rw, r)
				if called || rw.Code != http.StatusUnauthorized {
					t.Fatalf("called=%v status=%d", called, rw.Code)
				}
			})
		}
	}
}

// stubProofStore is a minimal deviceauth.Store for proof admission tests.
type stubProofStore struct {
	cred deviceauth.Credential
}

func (s stubProofStore) LookupOperational(context.Context, string) (deviceauth.Credential, error) {
	return s.cred, nil
}

func (s stubProofStore) AdmitOperational(context.Context, string, string, time.Time, time.Time) (deviceauth.Credential, error) {
	return s.cred, nil
}

func (s stubProofStore) AdmitEnrollment(context.Context, string, string, time.Time, time.Time) error {
	return nil
}

// signOperational builds a cryptographically valid sca-device+jwt proof whose
// rt claim is exactly target, and returns the Authorization value plus the
// credential a verifier must look up.
func signOperational(t *testing.T, target string, body []byte) (string, deviceauth.Credential) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	x := base64.RawURLEncoding.EncodeToString(pub)
	canonical := `{"crv":"Ed25519","kty":"OKP","x":"` + x + `"}`
	sum := sha256.Sum256([]byte(canonical))
	fp := base64.RawURLEncoding.EncodeToString(sum[:])
	now := time.Now().Unix()
	bh := sha256.Sum256(body)
	const deviceID = "00000000-0000-0000-0000-000000000001"
	claims := map[string]interface{}{
		"sub": deviceID, "aud": "aud", "iat": now, "exp": now + 60,
		"jti":   base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef")),
		"htm":   "POST",
		"rt":    target,
		"bhash": base64.RawURLEncoding.EncodeToString(bh[:]),
	}
	hb, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "sca-device+jwt", "kid": fp})
	pb, _ := json.Marshal(claims)
	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	raw := enc(hb) + "." + enc(pb)
	proof := raw + "." + enc(ed25519.Sign(priv, []byte(raw)))
	cred := deviceauth.Credential{DeviceID: deviceID, Fingerprint: fp, PublicKey: pub, Status: dispositivo.AuthActive}
	return "DeviceProof " + proof, cred
}

// TestBeforeMuxRejectsEscapedAliasRawTargetForValidProof proves the rejection
// is due to the non-canonical RAW target, not a malformed token: the alias
// proof is signed over the exact alias rt and only the raw-target equality
// keeps it out. The canonical control with the same signing routine is admitted.
func TestBeforeMuxRejectsEscapedAliasRawTargetForValidProof(t *testing.T) {
	body := []byte(`{"dispositivoId":"00000000-0000-0000-0000-000000000001","cpuPct":1,"memRamDisponibleMb":1,"tempChip":1,"aiProcessorPct":1}`)
	const canonical = "/api/v1/dispositivos/ping"
	const alias = "/api/v1/dispositivos/%70ing"

	// Control: canonical raw target signed over the canonical rt is admitted.
	authCanonical, credCanonical := signOperational(t, canonical, body)
	called := false
	r := httptest.NewRequest(http.MethodPost, canonical, bytes.NewReader(body))
	r.RequestURI = canonical
	r.Header.Set("Authorization", authCanonical)
	r.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	BeforeMux(&deviceauth.ProofVerifier{Audience: "aud", Store: stubProofStore{cred: credCanonical}}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(rw, r)
	if !called || rw.Code != http.StatusOK {
		t.Fatalf("canonical valid proof not admitted: called=%v status=%d", called, rw.Code)
	}

	// Alias: valid signature, rt==alias, RequestURI==alias. Raw target mismatch
	// must reject it before ServeMux even though r.URL.Path decodes to canonical.
	authAlias, credAlias := signOperational(t, alias, body)
	called = false
	r2 := httptest.NewRequest(http.MethodPost, alias, bytes.NewReader(body))
	r2.RequestURI = alias
	r2.Header.Set("Authorization", authAlias)
	r2.Header.Set("Content-Type", "application/json")
	if r2.URL.Path != canonical {
		t.Fatalf("precondition: decoded URL.Path=%q, want canonical", r2.URL.Path)
	}
	rw2 := httptest.NewRecorder()
	BeforeMux(&deviceauth.ProofVerifier{Audience: "aud", Store: stubProofStore{cred: credAlias}}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(rw2, r2)
	if called {
		t.Fatal("escaped alias raw target reached ServeMux")
	}
	if rw2.Code != http.StatusUnauthorized {
		t.Fatalf("alias status=%d, want 401", rw2.Code)
	}
}

// TestBeforeMuxLeavesHumanReprovisionToServeMux is the regression for the
// substring bug: /dispositivos/{uuid}/reprovision contains "/provision" but is
// a human management route and must never enter the device-proof guard.
func TestBeforeMuxLeavesHumanReprovisionToServeMux(t *testing.T) {
	for _, target := range []string{
		"/api/v1/dispositivos/device-1/reprovision",
		"/api/v1/dispositivos/device-1/reprovision/",
		"/api/v1/dispositivos/device-1/re%70rovision",
	} {
		t.Run(target, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/device-1/reprovision", strings.NewReader(`{}`))
			r.RequestURI = target
			rw := httptest.NewRecorder()
			BeforeMux(nil, next).ServeHTTP(rw, r)
			if !called {
				t.Fatalf("human reprovision %q was intercepted by the device-proof guard (status=%d)", target, rw.Code)
			}
		})
	}
}
