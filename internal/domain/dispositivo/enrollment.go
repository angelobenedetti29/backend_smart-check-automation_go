package dispositivo

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"time"
)

var (
	ErrEnrollmentUnavailable = errors.New("enrollment unavailable")
	ErrEnrollmentConsumed    = errors.New("enrollment consumed")
	ErrCredentialUsed        = errors.New("credential already used")
	ErrCredentialRevoked     = errors.New("credential revoked")
	ErrInvalidTransition     = errors.New("invalid lifecycle transition")
)

// AuthStatus is the credential admission state, separate from heartbeat health.
type AuthStatus string

const (
	AuthUnenrolled AuthStatus = "unenrolled"
	AuthActive     AuthStatus = "active"
	AuthDisabled   AuthStatus = "disabled"
	AuthRevoked    AuthStatus = "revoked"
)

// PublicJWK is the only key representation accepted by the device protocol.
type PublicJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
}

// EnrollmentCreateRequest is the human management invitation DTO.
type EnrollmentCreateRequest struct {
	Nombre    string `json:"nombre"`
	Ubicacion string `json:"ubicacion,omitempty"`
	WhepURL   string `json:"whepUrl,omitempty"`
}

// Validate applies the same metadata rules as the legacy device DTO.
func (r EnrollmentCreateRequest) Validate() error {
	return CreateDispositivoRequest{Nombre: strings.TrimSpace(r.Nombre), Ubicacion: strings.TrimSpace(r.Ubicacion), WhepURL: strings.TrimSpace(r.WhepURL)}.Validate()
}

// EnrollmentRedeemRequest is submitted by a device with its public key.
type EnrollmentRedeemRequest struct {
	Code      string    `json:"code,omitempty"`
	PublicKey PublicJWK `json:"publicKey"`
}

// EnrollmentRecoverRequest is the proof-only recovery DTO.
type EnrollmentRecoverRequest struct {
	PublicKey PublicJWK `json:"publicKey"`
}

// EnrollmentInvitation is safe to return to human operators; Code is only set
// on the one issuance response and is never persisted or returned by listing.
type EnrollmentInvitation struct {
	EnrollmentID  string    `json:"enrollmentId"`
	DispositivoID *string   `json:"dispositivoId"`
	Nombre        string    `json:"nombre"`
	Ubicacion     string    `json:"ubicacion,omitempty"`
	WhepURL       string    `json:"whepUrl,omitempty"`
	Status        string    `json:"status"`
	Code          string    `json:"code,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

// DeviceIdentity is returned after successful redemption or recovery.
type DeviceIdentity struct {
	EnrollmentID   string     `json:"enrollmentId"`
	DispositivoID  string     `json:"dispositivoId"`
	KeyFingerprint string     `json:"keyFingerprint"`
	AuthStatus     AuthStatus `json:"authStatus"`
	EnrolledAt     time.Time  `json:"enrolledAt"`
	Audience       string     `json:"audience"`
}

// DeviceRead is the security-aware catalog read model.
type DeviceRead struct {
	EstadoDispositivo
	AuthStatus        AuthStatus         `json:"authStatus"`
	KeyFingerprint    *string            `json:"keyFingerprint"`
	EnrolledAt        *time.Time         `json:"enrolledAt"`
	AuthUpdatedAt     *time.Time         `json:"authUpdatedAt"`
	PendingEnrollment *PendingEnrollment `json:"pendingEnrollment"`
}

// PendingEnrollment identifies a pending invitation without exposing a code.
type PendingEnrollment struct {
	EnrollmentID string    `json:"enrollmentId"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// LifecycleRequest is intentionally empty; it exists to enforce JSON object
// decoding and reject unknown fields at the controller boundary.
type LifecycleRequest struct{}

// EnrollmentRepository persists invitations, credentials and lifecycle state.
type EnrollmentRepository interface {
	CreateEnrollment(context.Context, string, EnrollmentCreateRequest, string, []byte, time.Time) (*EnrollmentInvitation, error)
	ListPendingEnrollments(context.Context) ([]EnrollmentInvitation, error)
	CancelEnrollment(context.Context, string, string) error
	RedeemEnrollment(context.Context, []byte, ed25519.PublicKey, string, string) (*DeviceIdentity, error)
	RecoverEnrollment(context.Context, string, string) (*DeviceIdentity, error)
	Lifecycle(context.Context, string, string, string) (*DeviceRead, error)
	Reprovision(context.Context, string, string, EnrollmentCreateRequest, string, []byte, time.Time) (*EnrollmentInvitation, error)
	ListDeviceReads(context.Context) ([]DeviceRead, error)
}
