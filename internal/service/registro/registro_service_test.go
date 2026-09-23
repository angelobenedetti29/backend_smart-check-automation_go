package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/registro"
)

// fakeRepo es un registro.Repository en memoria que registra las llamadas.
type fakeRepo struct {
	issueCalls     int
	issueHostname  string
	issueTipo      string
	issueRequestID string
	issueExpiresAt time.Time
	issueErr       error

	approveCalls  int
	approveActor  string
	approveID     string
	approveSecret string
	approveHash   string
	approvePickup time.Time
	approveErr    error

	rejectCalls int
	rejectActor string
	rejectID    string
	rejectErr   error

	pickupCalls int
	pickupID    string
	pickupResp  *registro.Pickup
	pickupErr   error

	listResp []registro.RegistrationRequest
	listErr  error
}

func (f *fakeRepo) Issue(_ context.Context, hostname, tipo, requestID string, expiresAt time.Time) (*registro.IssueResponse, error) {
	f.issueCalls++
	f.issueHostname = hostname
	f.issueTipo = tipo
	f.issueRequestID = requestID
	f.issueExpiresAt = expiresAt
	if f.issueErr != nil {
		return nil, f.issueErr
	}
	return &registro.IssueResponse{RequestID: requestID, Status: registro.StatusPending}, nil
}

func (f *fakeRepo) ListPending(context.Context) ([]registro.RegistrationRequest, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResp, nil
}

func (f *fakeRepo) Approve(_ context.Context, actor, requestID, secret, secretHash string, pickupExpiresAt time.Time) (*registro.Approval, error) {
	f.approveCalls++
	f.approveActor = actor
	f.approveID = requestID
	f.approveSecret = secret
	f.approveHash = secretHash
	f.approvePickup = pickupExpiresAt
	if f.approveErr != nil {
		return nil, f.approveErr
	}
	return &registro.Approval{RequestID: requestID, Status: registro.StatusApproved, DeviceID: "dev-1"}, nil
}

func (f *fakeRepo) Reject(_ context.Context, actor, requestID string) error {
	f.rejectCalls++
	f.rejectActor = actor
	f.rejectID = requestID
	return f.rejectErr
}

func (f *fakeRepo) Pickup(_ context.Context, requestID string) (*registro.Pickup, error) {
	f.pickupCalls++
	f.pickupID = requestID
	if f.pickupErr != nil {
		return nil, f.pickupErr
	}
	return f.pickupResp, nil
}

// validRequestID devuelve un request_id con formato válido (req_ + 43 chars).
func validRequestID() string { return "req_" + strings.Repeat("a", 43) }

func TestIssue_GeneraRequestIDConPrefijoYVencimiento(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	before := time.Now()

	res, err := svc.Issue(context.Background(), registro.CreateRequest{Hostname: "  rpi-01  ", Type: "salida_horno"})

	require.NoError(t, err)
	require.True(t, strings.HasPrefix(res.RequestID, "req_"))
	require.Len(t, res.RequestID, 47)
	require.True(t, requestIDPattern.MatchString(res.RequestID))
	require.Equal(t, registro.StatusPending, res.Status)
	require.Equal(t, 1, repo.issueCalls)
	require.Equal(t, "rpi-01", repo.issueHostname, "debe pasar el hostname ya trimeado")
	require.Equal(t, "SALIDA_HORNO", repo.issueTipo, "debe propagar el tipo normalizado")
	// El TTL debe ser ~15 minutos desde ahora.
	require.WithinDuration(t, before.Add(requestTTL), repo.issueExpiresAt, 5*time.Second)
}

func TestIssue_TipoInvalido_NoLlamaRepositorio(t *testing.T) {
	for _, tipo := range []string{"", "   ", "HORNO", "entrada-horno"} {
		t.Run(tipo, func(t *testing.T) {
			repo := &fakeRepo{}
			svc := NewService(repo)
			_, err := svc.Issue(context.Background(), registro.CreateRequest{Hostname: "rpi-01", Type: tipo})
			require.Error(t, err)
			require.Zero(t, repo.issueCalls)
		})
	}
}

func TestIssue_HostnameInvalido_NoLlamaRepositorio(t *testing.T) {
	cases := []struct {
		name     string
		hostname string
	}{
		{"vacío", "   "},
		{"espacio interno", "rpi 01"},
		{"tab interno", "rpi\t01"},
		{"demasiado largo", strings.Repeat("x", 101)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepo{}
			svc := NewService(repo)
			_, err := svc.Issue(context.Background(), registro.CreateRequest{Hostname: tc.hostname, Type: "ENTRADA_HORNO"})
			require.Error(t, err)
			require.Zero(t, repo.issueCalls)
		})
	}
}

func TestApprove_GeneraSecretYHashYRegeneraEnCadaLlamada(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	before := time.Now()
	id := validRequestID()

	first, err := svc.Approve(context.Background(), "sup@fermar.com.ar", id)
	require.NoError(t, err)
	require.Equal(t, 1, repo.approveCalls)
	require.Equal(t, "sup@fermar.com.ar", repo.approveActor)
	require.Equal(t, id, repo.approveID)
	require.Len(t, repo.approveSecret, 43, "el secret son 32 bytes en base64url")
	require.Len(t, repo.approveHash, 64, "el hash es SHA-256 en hex")
	sum := sha256.Sum256([]byte(repo.approveSecret))
	require.Equal(t, hex.EncodeToString(sum[:]), repo.approveHash)
	require.Equal(t, "dev-1", first.DeviceID)
	require.WithinDuration(t, before.Add(pickupTTL), repo.approvePickup, 5*time.Second)
	firstSecret := repo.approveSecret

	// Re-aprobar genera un secret nuevo (nunca reutiliza).
	_, err = svc.Approve(context.Background(), "sup@fermar.com.ar", id)
	require.NoError(t, err)
	require.Equal(t, 2, repo.approveCalls)
	require.NotEqual(t, firstSecret, repo.approveSecret)
}

func TestApprove_RequestIDInvalido_RetornaNotFoundSinTocarRepo(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)

	_, err := svc.Approve(context.Background(), "sup@fermar.com.ar", "no-es-un-id")

	require.ErrorIs(t, err, registro.ErrRequestNotFound)
	require.Zero(t, repo.approveCalls)
}

func TestReject_RequestIDInvalido_RetornaNotFoundSinTocarRepo(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)

	err := svc.Reject(context.Background(), "sup@fermar.com.ar", "req_corto")

	require.ErrorIs(t, err, registro.ErrRequestNotFound)
	require.Zero(t, repo.rejectCalls)
}

func TestPickup_Passthrough(t *testing.T) {
	device := "11111111-1111-1111-1111-111111111111"
	secret := "secreto-de-prueba"
	repo := &fakeRepo{pickupResp: &registro.Pickup{Status: registro.StatusApproved, DeviceID: &device, Secret: &secret}}
	svc := NewService(repo)
	id := validRequestID()

	got, err := svc.Pickup(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, 1, repo.pickupCalls)
	require.Equal(t, id, repo.pickupID)
	require.Equal(t, registro.StatusApproved, got.Status)
	require.NotNil(t, got.Secret)
	require.Equal(t, secret, *got.Secret)
}

func TestPickup_RequestIDInvalido_RetornaNotFoundSinTocarRepo(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)

	_, err := svc.Pickup(context.Background(), "req_")

	require.ErrorIs(t, err, registro.ErrRequestNotFound)
	require.Zero(t, repo.pickupCalls)
}

func TestList_Passthrough(t *testing.T) {
	repo := &fakeRepo{listResp: []registro.RegistrationRequest{{RequestID: validRequestID(), Hostname: "rpi-01", Status: registro.StatusPending}}}
	svc := NewService(repo)

	got, err := svc.List(context.Background())

	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "rpi-01", got[0].Hostname)
}
