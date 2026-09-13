package repository

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeviceAuthRepository is the durable PostgreSQL adapter for device proof state.
type DeviceAuthRepository struct{ db *pgxpool.Pool }

// NewDeviceAuthRepository creates the device authentication repository.
func NewDeviceAuthRepository(db *pgxpool.Pool) *DeviceAuthRepository {
	return &DeviceAuthRepository{db: db}
}

// LookupOperational gets the current public key without trusting request data.
func (r *DeviceAuthRepository) LookupOperational(ctx context.Context, fp string) (deviceauth.Credential, error) {
	var c deviceauth.Credential
	var key []byte
	var status string
	err := r.db.QueryRow(ctx, `SELECT dispositivo_id,fingerprint,public_key,auth_status FROM device_credentials c JOIN dispositivos d ON d.id=c.dispositivo_id WHERE c.fingerprint=$1 AND c.revoked_at IS NULL`, fp).Scan(&c.DeviceID, &c.Fingerprint, &key, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, deviceauth.ErrInvalidProof
	}
	if err != nil {
		return c, fmt.Errorf("%w: lookup device credential", deviceauth.ErrAuthStore)
	}
	if len(key) != ed25519.PublicKeySize {
		return c, deviceauth.ErrInvalidProof
	}
	c.PublicKey = ed25519.PublicKey(key)
	c.Status = deviceStatus(status)
	return c, nil
}

// AdmitOperational atomically serializes the device row and records a replay.
func (r *DeviceAuthRepository) AdmitOperational(ctx context.Context, fp, jti string, iat, exp time.Time) (deviceauth.Credential, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return deviceauth.Credential{}, fmt.Errorf("%w: begin", deviceauth.ErrAuthStore)
	}
	defer tx.Rollback(ctx)
	var c deviceauth.Credential
	var key []byte
	var status string
	var deviceID string
	err = tx.QueryRow(ctx, `SELECT dispositivo_id FROM device_credentials WHERE fingerprint=$1`, fp).Scan(&deviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, deviceauth.ErrInvalidProof
	}
	if err != nil {
		return c, fmt.Errorf("%w: credential device", deviceauth.ErrAuthStore)
	}
	// Lifecycle operations serialize on the device row. Lock it before
	// reloading the credential so a stale LookupOperational result cannot
	// bypass a committed disable or revoke.
	if err = tx.QueryRow(ctx, `SELECT id FROM dispositivos WHERE id=$1 FOR UPDATE`, deviceID).Scan(&c.DeviceID); errors.Is(err, pgx.ErrNoRows) {
		return c, deviceauth.ErrInvalidProof
	} else if err != nil {
		return c, fmt.Errorf("%w: device lock", deviceauth.ErrAuthStore)
	}
	err = tx.QueryRow(ctx, `
		SELECT c.dispositivo_id,c.fingerprint,c.public_key,d.auth_status
		FROM device_credentials c
		JOIN dispositivos d ON d.id=c.dispositivo_id
		WHERE c.fingerprint=$1
		  AND c.revoked_at IS NULL
		  AND d.current_key_fingerprint=$1
		  AND d.auth_status='active'`, fp).Scan(&c.DeviceID, &c.Fingerprint, &key, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, deviceauth.ErrInvalidProof
	}
	if err != nil {
		return c, fmt.Errorf("%w: credential revalidation", deviceauth.ErrAuthStore)
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return c, fmt.Errorf("%w: clock", deviceauth.ErrAuthStore)
	}
	if !now.Before(exp) || iat.After(now.Add(30*time.Second)) || iat.Before(now.Add(-90*time.Second)) || exp.Sub(iat) <= 0 || exp.Sub(iat) > 60*time.Second {
		return c, deviceauth.ErrInvalidProof
	}
	if _, err = tx.Exec(ctx, `DELETE FROM device_request_replays WHERE expires_at < clock_timestamp()-interval '30 seconds'`); err != nil {
		return c, fmt.Errorf("%w: cleanup", deviceauth.ErrAuthStore)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO device_request_replays(key_fingerprint,jti,expires_at) VALUES($1,$2,$3)`, fp, jti, exp); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return c, deviceauth.ErrProofReplayed
		}
		return c, fmt.Errorf("%w: replay", deviceauth.ErrAuthStore)
	}
	if err = tx.Commit(ctx); err != nil {
		return c, fmt.Errorf("%w: commit", deviceauth.ErrAuthStore)
	}
	c.PublicKey = ed25519.PublicKey(key)
	c.Status = deviceStatus(status)
	return c, nil
}

// AdmitEnrollment records a verified enrollment proof durably before redemption.
func (r *DeviceAuthRepository) AdmitEnrollment(ctx context.Context, fp, jti string, iat, exp time.Time) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w: begin", deviceauth.ErrAuthStore)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('device-proof:'||$1,0))`, fp); err != nil {
		return fmt.Errorf("%w: lock", deviceauth.ErrAuthStore)
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return fmt.Errorf("%w: clock", deviceauth.ErrAuthStore)
	}
	if !now.Before(exp) || iat.After(now.Add(30*time.Second)) || iat.Before(now.Add(-90*time.Second)) || exp.Sub(iat) <= 0 || exp.Sub(iat) > 60*time.Second {
		return deviceauth.ErrInvalidProof
	}
	if _, err = tx.Exec(ctx, `DELETE FROM device_request_replays WHERE expires_at < clock_timestamp()-interval '30 seconds'`); err != nil {
		return fmt.Errorf("%w: cleanup", deviceauth.ErrAuthStore)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO device_request_replays(key_fingerprint,jti,expires_at) VALUES($1,$2,$3)`, fp, jti, exp); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return deviceauth.ErrProofReplayed
		}
		return fmt.Errorf("%w: replay", deviceauth.ErrAuthStore)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit", deviceauth.ErrAuthStore)
	}
	return nil
}
func deviceStatus(s string) (x dispositivo.AuthStatus) { return dispositivo.AuthStatus(s) }
