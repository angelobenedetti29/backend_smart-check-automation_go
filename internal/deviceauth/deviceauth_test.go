package deviceauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubStore registra el último hash consultado para verificar el contrato.
type stubStore struct {
	id       string
	err      error
	lastHash string
	calls    int
}

func (s *stubStore) LookupActiveBySecretHash(_ context.Context, secretHash string) (string, error) {
	s.calls++
	s.lastHash = secretHash
	return s.id, s.err
}

func requestWithAuth(value string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", nil)
	if value != "" {
		r.Header.Set("Authorization", value)
	}
	return r
}

func TestHashSecretMatchesSHA256HexLowercase(t *testing.T) {
	sum := sha256.Sum256([]byte("s3cr3t"))
	assert.Equal(t, hex.EncodeToString(sum[:]), HashSecret("s3cr3t"))
}

func TestPrincipalContextRoundTrip(t *testing.T) {
	ctx := WithPrincipal(context.Background(), Principal{DeviceID: "dev-1"})
	p, ok := PrincipalFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "dev-1", p.DeviceID)

	_, ok = PrincipalFromContext(context.Background())
	assert.False(t, ok)
}

func TestAuthenticateValidBearerReturnsPrincipal(t *testing.T) {
	store := &stubStore{id: "dev-1"}
	v := &Verifier{Store: store}
	p, err := v.Authenticate(context.Background(), requestWithAuth("Bearer abc123"))
	require.NoError(t, err)
	assert.Equal(t, "dev-1", p.DeviceID)
	assert.Equal(t, HashSecret("abc123"), store.lastHash)
	assert.Equal(t, 1, store.calls)
}

func TestAuthenticateRejectsMissingMalformedAndOversizedTokens(t *testing.T) {
	cases := map[string]string{
		"missing":        "",
		"wrong-scheme":   "Basic abc123",
		"lowercase":      "bearer abc123",
		"no-token":       "Bearer",
		"two-spaces":     "Bearer  abc123",
		"space-in-token": "Bearer ab c",
		"control-char":   "Bearer ab\tc",
		"oversized":      "Bearer " + string(make([]byte, maxTokenBytes+1)),
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			store := &stubStore{id: "dev-1"}
			_, err := (&Verifier{Store: store}).Authenticate(context.Background(), requestWithAuth(header))
			assert.ErrorIs(t, err, ErrInvalidToken)
			assert.Equal(t, 0, store.calls)
		})
	}
}

func TestAuthenticateRejectsMultipleAuthorizationHeaders(t *testing.T) {
	r := requestWithAuth("Bearer abc123")
	r.Header.Add("Authorization", "Bearer other")
	_, err := (&Verifier{Store: &stubStore{id: "dev-1"}}).Authenticate(context.Background(), r)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestAuthenticateWrapsStoreErrorsAsInvalidToken(t *testing.T) {
	_, err := (&Verifier{Store: &stubStore{err: errors.New("db caída")}}).
		Authenticate(context.Background(), requestWithAuth("Bearer abc123"))
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestAuthenticateRejectsEmptyStoreResult(t *testing.T) {
	_, err := (&Verifier{Store: &stubStore{id: ""}}).
		Authenticate(context.Background(), requestWithAuth("Bearer abc123"))
	assert.ErrorIs(t, err, ErrInvalidToken)
}
