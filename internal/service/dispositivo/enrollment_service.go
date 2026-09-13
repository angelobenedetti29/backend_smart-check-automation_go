package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
)

const enrollmentTTL = 15 * time.Minute

// enrollmentSubjectPrefix is the proof subject bound to an enrollment key.
const enrollmentSubjectPrefix = "urn:sca:enrollment-key:"

// EnrollmentService implements management and public enrollment workflows.
type EnrollmentService struct {
	repo     dispositivo.EnrollmentRepository
	audience string
}

// NewEnrollmentService creates the enrollment service.
func NewEnrollmentService(repo dispositivo.EnrollmentRepository, audience string) *EnrollmentService {
	return &EnrollmentService{repo: repo, audience: audience}
}

// requireEnrollmentPrincipal enforces the fail-closed proof contract for the
// redeem/recover operations: the context must carry an enrollment principal
// whose fingerprint and subject match the presented public key. Operational
// principals, missing principals and mismatched keys are rejected before any
// repository call is made.
func requireEnrollmentPrincipal(ctx context.Context, fp string) error {
	p, ok := deviceauth.PrincipalFromContext(ctx)
	if !ok || !p.Enrollment || p.Fingerprint != fp || p.DeviceID != enrollmentSubjectPrefix+fp {
		return deviceauth.ErrInvalidProof
	}
	return nil
}

// Issue creates a one-time invitation and returns its plaintext code once.
func (s *EnrollmentService) Issue(ctx context.Context, actor string, req dispositivo.EnrollmentCreateRequest) (*dispositivo.EnrollmentInvitation, error) {
	req.Nombre = strings.TrimSpace(req.Nombre)
	req.Ubicacion = strings.TrimSpace(req.Ubicacion)
	req.WhepURL = strings.TrimSpace(req.WhepURL)
	if err := req.Validate(); err != nil {
		return nil, err
	}
	id, err := randomText(16)
	if err != nil {
		return nil, err
	}
	code, err := randomText(32)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(code))
	x, err := s.repo.CreateEnrollment(ctx, actor, req, id, h[:], time.Now().UTC().Add(enrollmentTTL))
	if err != nil {
		return nil, err
	}
	x.Code = code
	return x, nil
}

// List returns only active, unexpired invitations.
func (s *EnrollmentService) List(ctx context.Context) ([]dispositivo.EnrollmentInvitation, error) {
	return s.repo.ListPendingEnrollments(ctx)
}

// Cancel permanently cancels an invitation.
func (s *EnrollmentService) Cancel(ctx context.Context, actor, id string) error {
	return s.repo.CancelEnrollment(ctx, actor, id)
}

// Redeem consumes a code and binds the supplied public key atomically.
func (s *EnrollmentService) Redeem(ctx context.Context, code string, key dispositivo.PublicJWK) (*dispositivo.DeviceIdentity, error) {
	pub, fp, err := deviceauth.ParsePublicJWK(key)
	if err != nil {
		return nil, deviceauth.ErrInvalidProof
	}
	if err := requireEnrollmentPrincipal(ctx, fp); err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(code))
	return s.repo.RedeemEnrollment(ctx, h[:], pub, fp, s.audience)
}

// Recover returns a previously enrolled identity without reactivating it.
func (s *EnrollmentService) Recover(ctx context.Context, key dispositivo.PublicJWK) (*dispositivo.DeviceIdentity, error) {
	_, fp, err := deviceauth.ParsePublicJWK(key)
	if err != nil {
		return nil, deviceauth.ErrInvalidProof
	}
	if err := requireEnrollmentPrincipal(ctx, fp); err != nil {
		return nil, err
	}
	return s.repo.RecoverEnrollment(ctx, fp, s.audience)
}

// Lifecycle updates a device admission state.
func (s *EnrollmentService) Lifecycle(ctx context.Context, actor, id, action string) (*dispositivo.DeviceRead, error) {
	return s.repo.Lifecycle(ctx, actor, id, action)
}

// Reprovision invalidates the old credential and issues a replacement invite.
func (s *EnrollmentService) Reprovision(ctx context.Context, actor, id string) (*dispositivo.EnrollmentInvitation, error) {
	eid, err := randomText(16)
	if err != nil {
		return nil, err
	}
	code, err := randomText(32)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(code))
	x, err := s.repo.Reprovision(ctx, actor, id, dispositivo.EnrollmentCreateRequest{}, eid, h[:], time.Now().UTC().Add(enrollmentTTL))
	if err != nil {
		return nil, err
	}
	x.Code = code
	return x, nil
}

// Reads returns DB-authoritative security state.
func (s *EnrollmentService) Reads(ctx context.Context) ([]dispositivo.DeviceRead, error) {
	return s.repo.ListDeviceReads(ctx)
}

func randomText(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random generation failed: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
