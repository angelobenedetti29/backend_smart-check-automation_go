package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
)

// fakeEnrollmentRepo counts admissions so tests can assert that rejected
// proofs never reach the persistence layer.
type fakeEnrollmentRepo struct {
	redeemCalls  int
	recoverCalls int
	identity     *dispositivo.DeviceIdentity
	redeemErr    error
	recoverErr   error
}

func (f *fakeEnrollmentRepo) CreateEnrollment(context.Context, string, dispositivo.EnrollmentCreateRequest, string, []byte, time.Time) (*dispositivo.EnrollmentInvitation, error) {
	return nil, nil
}
func (f *fakeEnrollmentRepo) ListPendingEnrollments(context.Context) ([]dispositivo.EnrollmentInvitation, error) {
	return nil, nil
}
func (f *fakeEnrollmentRepo) CancelEnrollment(context.Context, string, string) error { return nil }
func (f *fakeEnrollmentRepo) RedeemEnrollment(context.Context, []byte, ed25519.PublicKey, string, string) (*dispositivo.DeviceIdentity, error) {
	f.redeemCalls++
	return f.identity, f.redeemErr
}
func (f *fakeEnrollmentRepo) RecoverEnrollment(context.Context, string, string) (*dispositivo.DeviceIdentity, error) {
	f.recoverCalls++
	return f.identity, f.recoverErr
}
func (f *fakeEnrollmentRepo) Lifecycle(context.Context, string, string, string) (*dispositivo.DeviceRead, error) {
	return nil, nil
}
func (f *fakeEnrollmentRepo) Reprovision(context.Context, string, string, dispositivo.EnrollmentCreateRequest, string, []byte, time.Time) (*dispositivo.EnrollmentInvitation, error) {
	return nil, nil
}
func (f *fakeEnrollmentRepo) ListDeviceReads(context.Context) ([]dispositivo.DeviceRead, error) {
	return nil, nil
}

func testEnrollmentJWK(t *testing.T) (dispositivo.PublicJWK, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key := dispositivo.PublicJWK{Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(pub)}
	_, fp, err := deviceauth.ParsePublicJWK(key)
	if err != nil {
		t.Fatalf("generated JWK rejected: %v", err)
	}
	return key, fp
}

func enrollmentContext(fp string) context.Context {
	return deviceauth.WithPrincipal(context.Background(), deviceauth.Principal{
		DeviceID:    enrollmentSubjectPrefix + fp,
		Fingerprint: fp,
		Enrollment:  true,
	})
}

func TestEnrollmentServiceRedeemRequiresMatchingEnrollmentPrincipal(t *testing.T) {
	key, fp := testEnrollmentJWK(t)
	_, otherFP := testEnrollmentJWK(t)
	identity := &dispositivo.DeviceIdentity{DispositivoID: "d1", KeyFingerprint: fp, AuthStatus: dispositivo.AuthActive}
	repo := &fakeEnrollmentRepo{identity: identity}
	svc := NewEnrollmentService(repo, "aud")

	reject := map[string]context.Context{
		"missing_principal":     context.Background(),
		"operational_principal": deviceauth.WithPrincipal(context.Background(), deviceauth.Principal{DeviceID: enrollmentSubjectPrefix + fp, Fingerprint: fp}),
		"wrong_key_principal":   enrollmentContext(otherFP),
		"mismatched_subject":    deviceauth.WithPrincipal(context.Background(), deviceauth.Principal{DeviceID: enrollmentSubjectPrefix + otherFP, Fingerprint: fp, Enrollment: true}),
	}
	for name, ctx := range reject {
		t.Run(name, func(t *testing.T) {
			repo.redeemCalls = 0
			if _, err := svc.Redeem(ctx, "code", key); !errors.Is(err, deviceauth.ErrInvalidProof) {
				t.Fatalf("expected ErrInvalidProof, got %v", err)
			}
			if repo.redeemCalls != 0 {
				t.Fatalf("repository called %d time(s) for rejected proof", repo.redeemCalls)
			}
		})
	}

	t.Run("matching_principal_succeeds", func(t *testing.T) {
		repo.redeemCalls = 0
		got, err := svc.Redeem(enrollmentContext(fp), "code", key)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != identity {
			t.Fatalf("unexpected identity: %+v", got)
		}
		if repo.redeemCalls != 1 {
			t.Fatalf("redeem calls=%d want 1", repo.redeemCalls)
		}
	})
}

func TestEnrollmentServiceRecoverRequiresMatchingEnrollmentPrincipal(t *testing.T) {
	key, fp := testEnrollmentJWK(t)
	_, otherFP := testEnrollmentJWK(t)
	identity := &dispositivo.DeviceIdentity{DispositivoID: "d1", KeyFingerprint: fp, AuthStatus: dispositivo.AuthActive}
	repo := &fakeEnrollmentRepo{identity: identity}
	svc := NewEnrollmentService(repo, "aud")

	reject := map[string]context.Context{
		"missing_principal":     context.Background(),
		"operational_principal": deviceauth.WithPrincipal(context.Background(), deviceauth.Principal{DeviceID: enrollmentSubjectPrefix + fp, Fingerprint: fp}),
		"wrong_key_principal":   enrollmentContext(otherFP),
		"mismatched_subject":    deviceauth.WithPrincipal(context.Background(), deviceauth.Principal{DeviceID: enrollmentSubjectPrefix + otherFP, Fingerprint: fp, Enrollment: true}),
	}
	for name, ctx := range reject {
		t.Run(name, func(t *testing.T) {
			repo.recoverCalls = 0
			if _, err := svc.Recover(ctx, key); !errors.Is(err, deviceauth.ErrInvalidProof) {
				t.Fatalf("expected ErrInvalidProof, got %v", err)
			}
			if repo.recoverCalls != 0 {
				t.Fatalf("repository called %d time(s) for rejected proof", repo.recoverCalls)
			}
		})
	}

	t.Run("matching_principal_succeeds", func(t *testing.T) {
		repo.recoverCalls = 0
		got, err := svc.Recover(enrollmentContext(fp), key)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != identity {
			t.Fatalf("unexpected identity: %+v", got)
		}
		if repo.recoverCalls != 1 {
			t.Fatalf("recover calls=%d want 1", repo.recoverCalls)
		}
	})
}
