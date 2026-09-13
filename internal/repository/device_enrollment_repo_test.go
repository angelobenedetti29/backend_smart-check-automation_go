package repository

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// enrollmentPool provisions one disposable database per test. It is driven
// exclusively by TEST_DATABASE_URL and never uses .env or production state.
func enrollmentPool(t *testing.T) *pgxpool.Pool {
	return repositoryTestPool(t)
}

func publicKey(t *testing.T) (ed25519.PublicKey, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	digest := sha256.Sum256(pub)
	return pub, base64.RawURLEncoding.EncodeToString(digest[:])
}

func issue(t *testing.T, r *DeviceEnrollmentRepository, code string) (string, []byte) {
	t.Helper()
	id := testUUID()
	digest := sha256.Sum256([]byte(code))
	_, err := r.CreateEnrollment(context.Background(), "admin@fermar.com.ar", dispositivo.EnrollmentCreateRequest{Nombre: "phase-a"}, id, digest[:], time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	return id, digest[:]
}

func TestDeviceEnrollmentConcurrentDistinctKeysOneWinner(t *testing.T) {
	p := enrollmentPool(t)
	r := NewDeviceEnrollmentRepository(p)
	id, codeHash := issue(t, r, "distinct-"+testUUID())
	pub1, fp1 := publicKey(t)
	pub2, fp2 := publicKey(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []struct {
		pub ed25519.PublicKey
		fp  string
	}{
		{pub1, fp1}, {pub2, fp2},
	} {
		wg.Add(1)
		go func(key struct {
			pub ed25519.PublicKey
			fp  string
		}) {
			defer wg.Done()
			_, err := r.RedeemEnrollment(context.Background(), codeHash, key.pub, key.fp, "test-audience")
			results <- err
		}(key)
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			require.ErrorIs(t, err, dispositivo.ErrEnrollmentUnavailable)
		}
	}
	require.Equal(t, 1, wins)
	var devices, credentials, consumed int
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM dispositivos`).Scan(&devices))
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM device_credentials`).Scan(&credentials))
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM device_enrollments WHERE enrollment_id=$1 AND consumed_at IS NOT NULL`, id).Scan(&consumed))
	require.Equal(t, 1, devices)
	require.Equal(t, 1, credentials)
	require.Equal(t, 1, consumed)
}

func TestDeviceEnrollmentExpiryCancellationAndConsumedState(t *testing.T) {
	p := enrollmentPool(t)
	r := NewDeviceEnrollmentRepository(p)
	expiredID := testUUID()
	expiredHash := sha256.Sum256([]byte("expired"))
	_, err := r.CreateEnrollment(context.Background(), "admin@fermar.com.ar", dispositivo.EnrollmentCreateRequest{Nombre: "expired"}, expiredID, expiredHash[:], time.Now().UTC().Add(-time.Second))
	require.NoError(t, err)
	pub, fp := publicKey(t)
	_, err = r.RedeemEnrollment(context.Background(), expiredHash[:], pub, fp, "aud")
	require.ErrorIs(t, err, dispositivo.ErrEnrollmentUnavailable)
	var n int
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM dispositivos`).Scan(&n))
	require.Equal(t, 0, n, "expired redemption must not create a UUID")

	cancelledID, cancelledHash := issue(t, r, "cancelled")
	require.NoError(t, r.CancelEnrollment(context.Background(), "admin@fermar.com.ar", cancelledID))
	pub, fp = publicKey(t)
	_, err = r.RedeemEnrollment(context.Background(), cancelledHash, pub, fp, "aud")
	require.ErrorIs(t, err, dispositivo.ErrEnrollmentUnavailable)
	require.NoError(t, r.CancelEnrollment(context.Background(), "admin@fermar.com.ar", cancelledID))

	consumedID, consumedHash := issue(t, r, "consumed")
	pub, fp = publicKey(t)
	identity, err := r.RedeemEnrollment(context.Background(), consumedHash, pub, fp, "aud")
	require.NoError(t, err)
	_, err = r.RedeemEnrollment(context.Background(), consumedHash, pub, fp, "aud")
	require.ErrorIs(t, err, dispositivo.ErrEnrollmentUnavailable)
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM device_enrollments WHERE enrollment_id=$1 AND consumed_at IS NOT NULL`, consumedID).Scan(&n))
	require.Equal(t, 1, n)
	require.NotEmpty(t, identity.DispositivoID)
}

func TestDeviceEnrollmentRollbackLeavesInvitationRedeemable(t *testing.T) {
	p := enrollmentPool(t)
	r := NewDeviceEnrollmentRepository(p)
	id, codeHash := issue(t, r, "rollback-"+testUUID())
	pub, fp := publicKey(t)
	_, err := r.RedeemEnrollment(context.Background(), codeHash, ed25519.PublicKey([]byte("short")), fp, "aud")
	require.Error(t, err)
	var consumed, devices int
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM device_enrollments WHERE enrollment_id=$1 AND consumed_at IS NOT NULL`, id).Scan(&consumed))
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM dispositivos`).Scan(&devices))
	require.Equal(t, 0, consumed)
	require.Equal(t, 0, devices)
	identity, err := r.RedeemEnrollment(context.Background(), codeHash, pub, fp, "aud")
	require.NoError(t, err)
	require.NotEmpty(t, identity.DispositivoID)
}

func TestDeviceReplayDurableAcrossRepositoryRestart(t *testing.T) {
	p := enrollmentPool(t)
	r := NewDeviceAuthRepository(p)
	fp, jti := testUUID(), testUUID()
	exp := time.Now().UTC().Add(time.Minute)
	iat := time.Now().UTC()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- r.AdmitEnrollment(context.Background(), fp, jti, iat, exp) }()
	}
	wg.Wait()
	close(errs)
	wins := 0
	for err := range errs {
		if err == nil {
			wins++
		} else {
			require.ErrorIs(t, err, deviceauth.ErrProofReplayed)
		}
	}
	require.Equal(t, 1, wins)
	restarted := NewDeviceAuthRepository(p)
	require.ErrorIs(t, restarted.AdmitEnrollment(context.Background(), fp, jti, iat, exp), deviceauth.ErrProofReplayed)
}

func TestAdmitOperationalRejectsAfterCommittedDisableWithoutReplay(t *testing.T) {
	p := enrollmentPool(t)
	r := NewDeviceEnrollmentRepository(p)
	auth := NewDeviceAuthRepository(p)
	pub, fp := publicKey(t)
	deviceID := testUUID()
	enrollmentID := testUUID()
	require.NoError(t, p.QueryRow(context.Background(), `INSERT INTO dispositivos(id,nombre,auth_status,current_key_fingerprint) VALUES($1,'admission-race','active',$2) RETURNING id`, deviceID, fp).Scan(&deviceID))
	var actor string
	require.NoError(t, p.QueryRow(context.Background(), `SELECT id FROM usuarios WHERE email='admin@fermar.com.ar'`).Scan(&actor))
	_, err := p.Exec(context.Background(), `INSERT INTO device_enrollments(enrollment_id,nombre,created_by,expires_at,consumed_at,result_dispositivo_id,result_key_fingerprint) VALUES($1,'admission', $2, clock_timestamp()+interval '1 hour',clock_timestamp(),$3,$4)`, enrollmentID, actor, deviceID, fp)
	require.NoError(t, err)
	_, err = p.Exec(context.Background(), `INSERT INTO device_credentials(fingerprint,dispositivo_id,public_key,enrollment_id) VALUES($1,$2,$3,$4)`, fp, deviceID, []byte(pub), enrollmentID)
	require.NoError(t, err)
	_, err = auth.LookupOperational(context.Background(), fp)
	require.NoError(t, err)
	_, err = r.Lifecycle(context.Background(), "admin@fermar.com.ar", deviceID, "disable")
	require.NoError(t, err)
	_, err = auth.AdmitOperational(context.Background(), fp, testUUID(), time.Now().UTC(), time.Now().UTC().Add(time.Minute))
	require.ErrorIs(t, err, deviceauth.ErrInvalidProof)
	var replayCount int
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM device_request_replays WHERE key_fingerprint=$1`, fp).Scan(&replayCount))
	require.Equal(t, 0, replayCount)
}

func TestRecoveryRejectsReplacedKeyAndReturnsDisabledCurrentKey(t *testing.T) {
	p := enrollmentPool(t)
	r := NewDeviceEnrollmentRepository(p)
	id, codeHash := issue(t, r, "key-a")
	pubA, fpA := publicKey(t)
	identityA, err := r.RedeemEnrollment(context.Background(), codeHash, pubA, fpA, "aud")
	require.NoError(t, err)
	require.Equal(t, id, identityA.EnrollmentID, "original enrollment history must be retained")
	deviceID := identityA.DispositivoID
	replacementID := testUUID()
	replacementHash := sha256.Sum256([]byte("key-b"))
	inv, err := r.Reprovision(context.Background(), "admin@fermar.com.ar", deviceID, dispositivo.EnrollmentCreateRequest{}, replacementID, replacementHash[:], time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)

	// A is revoked immediately when the replacement invitation is issued.
	_, err = r.RecoverEnrollment(context.Background(), fpA, "aud")
	require.ErrorIs(t, err, dispositivo.ErrCredentialRevoked)

	pubB, fpB := publicKey(t)
	identityB, err := r.RedeemEnrollment(context.Background(), replacementHash[:], pubB, fpB, "aud")
	require.NoError(t, err)
	require.Equal(t, inv.EnrollmentID, identityB.EnrollmentID)
	require.Equal(t, deviceID, identityB.DispositivoID, "reprovision must retain the same device UUID/history")

	// B recovers as active while it is the current non-revoked key.
	activeB, err := r.RecoverEnrollment(context.Background(), fpB, "aud")
	require.NoError(t, err)
	require.Equal(t, dispositivo.AuthActive, activeB.AuthStatus)
	require.Equal(t, deviceID, activeB.DispositivoID)
	require.Equal(t, fpB, activeB.KeyFingerprint)

	// A stays permanently rejected after B is activated.
	_, err = r.RecoverEnrollment(context.Background(), fpA, "aud")
	require.ErrorIs(t, err, dispositivo.ErrCredentialRevoked)

	_, err = r.Lifecycle(context.Background(), "admin@fermar.com.ar", deviceID, "disable")
	require.NoError(t, err)

	// B recovers as disabled (never reactivated) and A remains rejected.
	disabledB, err := r.RecoverEnrollment(context.Background(), fpB, "aud")
	require.NoError(t, err)
	require.Equal(t, dispositivo.AuthDisabled, disabledB.AuthStatus)
	require.Equal(t, deviceID, disabledB.DispositivoID)
	_, err = r.RecoverEnrollment(context.Background(), fpA, "aud")
	require.ErrorIs(t, err, dispositivo.ErrCredentialRevoked)

	var oldRevoked bool
	require.NoError(t, p.QueryRow(context.Background(), `SELECT revoked_at IS NOT NULL FROM device_credentials WHERE fingerprint=$1`, fpA).Scan(&oldRevoked))
	require.True(t, oldRevoked)
	var credentialCount, enrollmentCount int
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM device_credentials WHERE dispositivo_id=$1`, deviceID).Scan(&credentialCount))
	require.Equal(t, 2, credentialCount, "reprovision must retain the old credential history")
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM device_enrollments WHERE enrollment_id IN ($1,$2)`, id, replacementID).Scan(&enrollmentCount))
	require.Equal(t, 2, enrollmentCount, "both original and replacement enrollment history must be retained")
}

func TestEnrollmentAuditContainsActorIDsAndNoSecretMaterial(t *testing.T) {
	p := enrollmentPool(t)
	r := NewDeviceEnrollmentRepository(p)
	id, _ := issue(t, r, "audit-secret-code")
	require.NoError(t, r.CancelEnrollment(context.Background(), "admin@fermar.com.ar", id))
	_, codeHash := issue(t, r, "audit-redeem-code")
	pub, fp := publicKey(t)
	identity, err := r.RedeemEnrollment(context.Background(), codeHash, pub, fp, "aud")
	require.NoError(t, err)
	replaceID := testUUID()
	replaceHash := sha256.Sum256([]byte("replacement-secret-code"))
	_, err = r.Reprovision(context.Background(), "admin@fermar.com.ar", identity.DispositivoID, dispositivo.EnrollmentCreateRequest{}, replaceID, replaceHash[:], time.Now().UTC().Add(time.Minute))
	require.NoError(t, err)
	var count int
	var cancelledID string
	// The first invitation is the one that exercises cancellation; the second
	// invitation is consumed and provides the device for reprovisioning.
	require.NoError(t, p.QueryRow(context.Background(), `SELECT enrollment_id FROM device_lifecycle_audit WHERE action='cancel' LIMIT 1`).Scan(&cancelledID))
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM device_lifecycle_audit WHERE enrollment_id=$1 AND action='issue' AND dispositivo_id IS NULL`, cancelledID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM device_lifecycle_audit WHERE enrollment_id=$1 AND action='cancel'`, cancelledID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM device_lifecycle_audit WHERE enrollment_id=$1 AND action='reprovision' AND dispositivo_id=$2`, replaceID, identity.DispositivoID).Scan(&count))
	require.Equal(t, 1, count)
	rows, err := p.Query(context.Background(), `SELECT action,coalesce(enrollment_id,''),coalesce(old_status,''),coalesce(new_status,'') FROM device_lifecycle_audit ORDER BY created_at`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var action, enrollment, oldStatus, newStatus string
		require.NoError(t, rows.Scan(&action, &enrollment, &oldStatus, &newStatus))
		require.NotContains(t, enrollment, "audit-secret-code")
		require.NotContains(t, enrollment, "replacement-secret-code")
		require.NotEmpty(t, action)
		require.NotEmpty(t, oldStatus)
		require.NotEmpty(t, newStatus)
	}
	require.NoError(t, rows.Err())

	// The audit relation contains no columns capable of storing proofs, hashes,
	// plaintext invitation codes, or public key material.
	var secretColumns int
	require.NoError(t, p.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_name='device_lifecycle_audit' AND column_name IN ('code','code_hash','public_key','proof')`).Scan(&secretColumns))
	require.Equal(t, 0, secretColumns)
}

func TestDeviceReadsDefaultOfflineWithoutTelemetry(t *testing.T) {
	p := enrollmentPool(t)
	r := NewDeviceEnrollmentRepository(p)
	var id string
	require.NoError(t, p.QueryRow(context.Background(), `INSERT INTO dispositivos(nombre) VALUES('no-telemetry') RETURNING id`).Scan(&id))
	one, err := r.readOne(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, dispositivo.EstadoOffline, one.Estado)
	all, err := r.ListDeviceReads(context.Background())
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, dispositivo.EstadoOffline, all[0].Estado)
}

func TestLifecycleAndReprovisionDoNotDeadlock(t *testing.T) {
	p := enrollmentPool(t)
	r := NewDeviceEnrollmentRepository(p)
	id, hash := issue(t, r, "concurrency")
	pub, fp := publicKey(t)
	identity, err := r.RedeemEnrollment(context.Background(), hash, pub, fp, "aud")
	require.NoError(t, err)
	_ = id
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() {
		_, err := r.Lifecycle(ctx, "admin@fermar.com.ar", identity.DispositivoID, "disable")
		results <- err
	}()
	go func() {
		eid := testUUID()
		h := sha256.Sum256([]byte("replacement" + eid))
		_, err := r.Reprovision(ctx, "admin@fermar.com.ar", identity.DispositivoID, dispositivo.EnrollmentCreateRequest{}, eid, h[:], time.Now().UTC().Add(time.Minute))
		results <- err
	}()
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			continue
		}
		// Under contention the only acceptable failure is the deterministic
		// lifecycle rejection when reprovision won the race and revoked the
		// device first. Every other error — especially PostgreSQL 40P01
		// (deadlock detected), a lock timeout, or an auth store failure —
		// must fail the test instead of being silently accepted.
		require.True(t, errors.Is(err, dispositivo.ErrInvalidTransition), "unexpected error under lifecycle/reprovision contention: %v", err)
	}
	for i := 0; i < 2; i++ {
		eid := testUUID()
		h := sha256.Sum256([]byte("repeat-replacement" + eid))
		_, err := r.Reprovision(ctx, "admin@fermar.com.ar", identity.DispositivoID, dispositivo.EnrollmentCreateRequest{}, eid, h[:], time.Now().UTC().Add(time.Minute))
		require.NoError(t, err)
	}
}
