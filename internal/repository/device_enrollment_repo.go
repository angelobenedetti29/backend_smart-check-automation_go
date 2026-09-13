package repository

import (
	"context"
	"crypto/ed25519"
	"errors"
	"time"

	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeviceEnrollmentRepository stores the complete device admission state.
type DeviceEnrollmentRepository struct{ db *pgxpool.Pool }

// NewDeviceEnrollmentRepository creates the enrollment repository.
func NewDeviceEnrollmentRepository(db *pgxpool.Pool) *DeviceEnrollmentRepository {
	return &DeviceEnrollmentRepository{db: db}
}

func (r *DeviceEnrollmentRepository) actorID(ctx context.Context, tx pgx.Tx, email string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM usuarios WHERE email=$1 AND activo=true`, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("actor not found")
	}
	return id, err
}
func beginLocked(ctx context.Context, db *pgxpool.Pool, key string) (pgx.Tx, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

// CreateEnrollment issues one invitation and stores only its hash.
func (r *DeviceEnrollmentRepository) CreateEnrollment(ctx context.Context, email string, req dispositivo.EnrollmentCreateRequest, id string, hash []byte, expires time.Time) (*dispositivo.EnrollmentInvitation, error) {
	tx, err := beginLocked(ctx, r.db, "enrollment:create")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	actor, err := r.actorID(ctx, tx, email)
	if err != nil {
		return nil, err
	}
	var created time.Time
	var loc, whep *string
	if req.Ubicacion != "" {
		loc = &req.Ubicacion
	}
	if req.WhepURL != "" {
		whep = &req.WhepURL
	}
	err = tx.QueryRow(ctx, `INSERT INTO device_enrollments(enrollment_id,code_hash,nombre,ubicacion,whep_url,created_by,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`, id, hash, req.Nombre, loc, whep, actor, expires).Scan(&created)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO device_lifecycle_audit(actor_id,action,dispositivo_id,enrollment_id,old_status,new_status)
		VALUES($1,'issue',NULL,$2,'unenrolled','unenrolled')`, actor, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &dispositivo.EnrollmentInvitation{EnrollmentID: id, Nombre: req.Nombre, Ubicacion: req.Ubicacion, WhepURL: req.WhepURL, Status: "pending", Code: "", CreatedAt: created, ExpiresAt: expires}, nil
}

// ListPendingEnrollments returns unexpired invitations and never code material.
func (r *DeviceEnrollmentRepository) ListPendingEnrollments(ctx context.Context) ([]dispositivo.EnrollmentInvitation, error) {
	rows, err := r.db.Query(ctx, `SELECT enrollment_id,target_dispositivo_id,nombre,COALESCE(ubicacion,''),COALESCE(whep_url,''),created_at,expires_at FROM device_enrollments WHERE consumed_at IS NULL AND cancelled_at IS NULL AND expires_at>clock_timestamp() ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dispositivo.EnrollmentInvitation{}
	for rows.Next() {
		var x dispositivo.EnrollmentInvitation
		if err := rows.Scan(&x.EnrollmentID, &x.DispositivoID, &x.Nombre, &x.Ubicacion, &x.WhepURL, &x.CreatedAt, &x.ExpiresAt); err != nil {
			return nil, err
		}
		x.Status = "pending"
		out = append(out, x)
	}
	return out, rows.Err()
}

// CancelEnrollment cancels a pending invitation idempotently.
func (r *DeviceEnrollmentRepository) CancelEnrollment(ctx context.Context, email, id string) error {
	tx, err := beginLocked(ctx, r.db, "enrollment:"+id)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	actor, err := r.actorID(ctx, tx, email)
	if err != nil {
		return err
	}
	var target *string
	if err = tx.QueryRow(ctx, `SELECT target_dispositivo_id FROM device_enrollments WHERE enrollment_id=$1`, id).Scan(&target); errors.Is(err, pgx.ErrNoRows) {
		return dispositivo.ErrEnrollmentUnavailable
	} else if err != nil {
		return err
	}
	if target != nil {
		if _, err = tx.Exec(ctx, `SELECT id FROM dispositivos WHERE id=$1 FOR UPDATE`, *target); err != nil {
			return err
		}
	}
	var consumed, cancelled *time.Time
	err = tx.QueryRow(ctx, `SELECT consumed_at,cancelled_at FROM device_enrollments WHERE enrollment_id=$1 FOR UPDATE`, id).Scan(&consumed, &cancelled)
	if errors.Is(err, pgx.ErrNoRows) {
		return dispositivo.ErrEnrollmentUnavailable
	}
	if err != nil {
		return err
	}
	if consumed != nil {
		return dispositivo.ErrEnrollmentConsumed
	}
	if cancelled == nil {
		_, err = tx.Exec(ctx, `UPDATE device_enrollments SET cancelled_at=clock_timestamp(),code_hash=NULL WHERE enrollment_id=$1`, id)
		if err != nil {
			return err
		}
		status := "unenrolled"
		if target != nil {
			if err = tx.QueryRow(ctx, `SELECT auth_status FROM dispositivos WHERE id=$1`, *target).Scan(&status); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO device_lifecycle_audit(actor_id,action,dispositivo_id,enrollment_id,old_status,new_status)
			VALUES($1,'cancel',$2,$3,$4,$4)`, actor, target, id, status); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// RedeemEnrollment atomically creates/activates the device identity.
func (r *DeviceEnrollmentRepository) RedeemEnrollment(ctx context.Context, codeHash []byte, pub ed25519.PublicKey, fp, aud string) (*dispositivo.DeviceIdentity, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var id string
	var targetHint *string
	err = tx.QueryRow(ctx, `SELECT enrollment_id,target_dispositivo_id FROM device_enrollments WHERE code_hash=$1`, codeHash).Scan(&id, &targetHint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, dispositivo.ErrEnrollmentUnavailable
	}
	if err != nil {
		return nil, err
	}
	// Existing-device redemption follows the same lock order as lifecycle and
	// reprovision: device row first, invitation row second. New enrollments
	// have no device row and lock the invitation alone.
	if targetHint != nil {
		if _, err = tx.Exec(ctx, `SELECT id FROM dispositivos WHERE id=$1 FOR UPDATE`, *targetHint); err != nil {
			return nil, err
		}
	}
	var target *string
	var nombre string
	var ubic, whep *string
	var expires time.Time
	var consumed, cancelled *time.Time
	err = tx.QueryRow(ctx, `SELECT target_dispositivo_id,nombre,ubicacion,whep_url,expires_at,consumed_at,cancelled_at FROM device_enrollments WHERE enrollment_id=$1 FOR UPDATE`, id).Scan(&target, &nombre, &ubic, &whep, &expires, &consumed, &cancelled)
	if err != nil {
		return nil, err
	}
	var dbNow time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		return nil, err
	}
	if consumed != nil || cancelled != nil || !dbNow.Before(expires) {
		return nil, dispositivo.ErrEnrollmentUnavailable
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM device_credentials WHERE fingerprint=$1)`, fp).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, dispositivo.ErrCredentialUsed
	}
	var deviceID string
	var oldFP *string
	if target != nil {
		if _, err = tx.Exec(ctx, `SELECT id FROM dispositivos WHERE id=$1 FOR UPDATE`, *target); err != nil {
			return nil, err
		}
		deviceID = *target
		_ = tx.QueryRow(ctx, `SELECT current_key_fingerprint FROM dispositivos WHERE id=$1`, deviceID).Scan(&oldFP)
		if oldFP != nil {
			_, err = tx.Exec(ctx, `UPDATE device_credentials SET revoked_at=clock_timestamp() WHERE fingerprint=$1`, *oldFP)
			if err != nil {
				return nil, err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE dispositivos SET auth_status='active',current_key_fingerprint=$1,auth_updated_at=clock_timestamp() WHERE id=$2`, fp, deviceID)
	} else {
		var loc, w *string
		if ubic != nil {
			loc = ubic
		}
		if whep != nil {
			w = whep
		}
		err = tx.QueryRow(ctx, `INSERT INTO dispositivos(nombre,ubicacion,whep_url,auth_status,current_key_fingerprint,auth_updated_at) VALUES($1,$2,$3,'active',$4,clock_timestamp()) RETURNING id`, nombre, loc, w, fp).Scan(&deviceID)
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO device_credentials(fingerprint,dispositivo_id,public_key,enrollment_id) VALUES($1,$2,$3,$4)`, fp, deviceID, []byte(pub), id); err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return nil, dispositivo.ErrCredentialUsed
		}
		return nil, err
	}
	var enrolled time.Time
	if err = tx.QueryRow(ctx, `UPDATE device_enrollments SET consumed_at=clock_timestamp(),result_dispositivo_id=$1,result_key_fingerprint=$2,code_hash=NULL WHERE enrollment_id=$3 RETURNING consumed_at`, deviceID, fp, id).Scan(&enrolled); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &dispositivo.DeviceIdentity{EnrollmentID: id, DispositivoID: deviceID, KeyFingerprint: fp, AuthStatus: dispositivo.AuthActive, EnrolledAt: enrolled, Audience: aud}, nil
}

// RecoverEnrollment looks up a completed identity by its unique fingerprint.
func (r *DeviceEnrollmentRepository) RecoverEnrollment(ctx context.Context, fp, aud string) (*dispositivo.DeviceIdentity, error) {
	var x dispositivo.DeviceIdentity
	var status string
	var revokedAt *time.Time
	var currentFP *string
	err := r.db.QueryRow(ctx, `SELECT e.enrollment_id,c.dispositivo_id,c.enrolled_at,c.revoked_at,d.current_key_fingerprint,d.auth_status FROM device_credentials c JOIN dispositivos d ON d.id=c.dispositivo_id JOIN device_enrollments e ON e.enrollment_id=c.enrollment_id WHERE c.fingerprint=$1`, fp).Scan(&x.EnrollmentID, &x.DispositivoID, &x.EnrolledAt, &revokedAt, &currentFP, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, dispositivo.ErrEnrollmentUnavailable
	}
	if err != nil {
		return nil, err
	}
	if revokedAt != nil || currentFP == nil || *currentFP != fp || (status != string(dispositivo.AuthActive) && status != string(dispositivo.AuthDisabled)) {
		return nil, dispositivo.ErrCredentialRevoked
	}
	x.KeyFingerprint = fp
	x.AuthStatus = dispositivo.AuthStatus(status)
	x.Audience = aud
	return &x, nil
}

// Lifecycle applies an idempotent state transition under the device row lock.
func (r *DeviceEnrollmentRepository) Lifecycle(ctx context.Context, email, id, action string) (*dispositivo.DeviceRead, error) {
	tx, err := beginLocked(ctx, r.db, "device:"+id)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	actor, err := r.actorID(ctx, tx, email)
	if err != nil {
		return nil, err
	}
	var old string
	err = tx.QueryRow(ctx, `SELECT auth_status FROM dispositivos WHERE id=$1 FOR UPDATE`, id).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, dispositivo.ErrDispositivoNotFound
	}
	if err != nil {
		return nil, err
	}
	newStatus := old
	switch action {
	case "disable":
		if old == "active" {
			newStatus = "disabled"
		} else if old != "disabled" {
			return nil, dispositivo.ErrInvalidTransition
		}
	case "enable":
		if old == "disabled" {
			newStatus = "active"
		} else if old != "active" {
			return nil, dispositivo.ErrInvalidTransition
		}
	case "revoke":
		if old == "active" || old == "disabled" {
			newStatus = "revoked"
		} else if old != "revoked" {
			return nil, dispositivo.ErrInvalidTransition
		}
	default:
		return nil, dispositivo.ErrInvalidTransition
	}
	if newStatus != old {
		_, err = tx.Exec(ctx, `UPDATE dispositivos SET auth_status=$1::varchar,auth_updated_at=clock_timestamp(),current_key_fingerprint=CASE WHEN $1::varchar='revoked' THEN NULL ELSE current_key_fingerprint END WHERE id=$2::uuid`, newStatus, id)
		if err != nil {
			return nil, err
		}
		if newStatus == "revoked" {
			_, err = tx.Exec(ctx, `UPDATE device_credentials SET revoked_at=clock_timestamp() WHERE dispositivo_id=$1 AND revoked_at IS NULL`, id)
			if err != nil {
				return nil, err
			}
		}
	}
	if action == "revoke" {
		if _, err = tx.Exec(ctx, `UPDATE device_enrollments SET cancelled_at=clock_timestamp(),code_hash=NULL WHERE target_dispositivo_id=$1 AND consumed_at IS NULL AND cancelled_at IS NULL`, id); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO device_lifecycle_audit(actor_id,action,dispositivo_id,old_status,new_status) VALUES($1,$2,$3,$4,$5)`, actor, action, id, old, newStatus); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.readOne(ctx, id)
}

// Reprovision revokes the current key and atomically replaces its invitation.
func (r *DeviceEnrollmentRepository) Reprovision(ctx context.Context, email, id string, req dispositivo.EnrollmentCreateRequest, eid string, hash []byte, expires time.Time) (*dispositivo.EnrollmentInvitation, error) {
	tx, err := beginLocked(ctx, r.db, "device:"+id)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	actor, err := r.actorID(ctx, tx, email)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM dispositivos WHERE id=$1 FOR UPDATE`, id); err != nil {
		return nil, err
	}
	var oldStatus string
	if err = tx.QueryRow(ctx, `SELECT auth_status FROM dispositivos WHERE id=$1`, id).Scan(&oldStatus); err != nil {
		return nil, err
	}
	if req.Nombre == "" {
		var currentLocation, currentWhep *string
		if err = tx.QueryRow(ctx, `SELECT nombre,ubicacion,whep_url FROM dispositivos WHERE id=$1`, id).Scan(&req.Nombre, &currentLocation, &currentWhep); err != nil {
			return nil, err
		}
		if currentLocation != nil {
			req.Ubicacion = *currentLocation
		}
		if currentWhep != nil {
			req.WhepURL = *currentWhep
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE device_enrollments SET cancelled_at=clock_timestamp(),code_hash=NULL WHERE target_dispositivo_id=$1 AND consumed_at IS NULL AND cancelled_at IS NULL`, id); err != nil {
		return nil, err
	}
	// Reprovision is an immediate security boundary: the old key stops working
	// when the replacement invitation is issued, not when it is redeemed.
	if _, err = tx.Exec(ctx, `UPDATE device_credentials SET revoked_at=clock_timestamp() WHERE dispositivo_id=$1 AND revoked_at IS NULL`, id); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE dispositivos SET auth_status='revoked',current_key_fingerprint=NULL,auth_updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return nil, err
	}
	var loc, w *string
	if req.Ubicacion != "" {
		loc = &req.Ubicacion
	}
	if req.WhepURL != "" {
		w = &req.WhepURL
	}
	var created time.Time
	if err = tx.QueryRow(ctx, `INSERT INTO device_enrollments(enrollment_id,code_hash,target_dispositivo_id,nombre,ubicacion,whep_url,created_by,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`, eid, hash, id, req.Nombre, loc, w, actor, expires).Scan(&created); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO device_lifecycle_audit(actor_id,action,dispositivo_id,enrollment_id,old_status,new_status)
		VALUES($1,'reprovision',$2,$3,$4,'revoked')`, actor, id, eid, oldStatus); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	did := id
	return &dispositivo.EnrollmentInvitation{EnrollmentID: eid, DispositivoID: &did, Nombre: req.Nombre, Ubicacion: req.Ubicacion, WhepURL: req.WhepURL, Status: "pending", CreatedAt: created, ExpiresAt: expires}, nil
}

func (r *DeviceEnrollmentRepository) readOne(ctx context.Context, id string) (*dispositivo.DeviceRead, error) {
	var x dispositivo.DeviceRead
	var status string
	var fp *string
	var enrolled, updated *time.Time
	err := r.db.QueryRow(ctx, `SELECT d.id,d.nombre,COALESCE(d.ubicacion,''),COALESCE(d.whep_url,''),d.auth_status,CASE WHEN d.auth_status IN ('active','disabled') THEN d.current_key_fingerprint ELSE NULL END,d.auth_updated_at,c.enrolled_at FROM dispositivos d LEFT JOIN device_credentials c ON c.fingerprint=d.current_key_fingerprint AND c.revoked_at IS NULL WHERE d.id=$1`, id).Scan(&x.DispositivoID, &x.Nombre, &x.Ubicacion, &x.WhepURL, &status, &fp, &updated, &enrolled)
	if err != nil {
		return nil, err
	}
	x.AuthStatus = dispositivo.AuthStatus(status)
	x.KeyFingerprint = fp
	x.EnrolledAt = enrolled
	x.AuthUpdatedAt = updated
	x.Estado = dispositivo.EstadoOffline
	return &x, nil
}

// ListDeviceReads returns DB-authoritative security fields for all devices.
func (r *DeviceEnrollmentRepository) ListDeviceReads(ctx context.Context) ([]dispositivo.DeviceRead, error) {
	rows, err := r.db.Query(ctx, `SELECT d.id,d.nombre,COALESCE(d.ubicacion,''),COALESCE(d.whep_url,''),d.auth_status,CASE WHEN d.auth_status IN ('active','disabled') THEN d.current_key_fingerprint ELSE NULL END,d.auth_updated_at,c.enrolled_at,e.enrollment_id,e.expires_at FROM dispositivos d LEFT JOIN device_credentials c ON c.fingerprint=d.current_key_fingerprint AND c.revoked_at IS NULL LEFT JOIN LATERAL (SELECT enrollment_id,expires_at FROM device_enrollments WHERE target_dispositivo_id=d.id AND consumed_at IS NULL AND cancelled_at IS NULL AND expires_at>clock_timestamp() ORDER BY created_at DESC LIMIT 1) e ON true ORDER BY d.nombre`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dispositivo.DeviceRead{}
	for rows.Next() {
		var x dispositivo.DeviceRead
		var status string
		var fp *string
		var enrolled, updated *time.Time
		var eid *string
		var exp *time.Time
		if err := rows.Scan(&x.DispositivoID, &x.Nombre, &x.Ubicacion, &x.WhepURL, &status, &fp, &updated, &enrolled, &eid, &exp); err != nil {
			return nil, err
		}
		x.AuthStatus = dispositivo.AuthStatus(status)
		x.KeyFingerprint = fp
		x.EnrolledAt = enrolled
		x.AuthUpdatedAt = updated
		x.Estado = dispositivo.EstadoOffline
		if eid != nil && exp != nil {
			x.PendingEnrollment = &dispositivo.PendingEnrollment{EnrollmentID: *eid, ExpiresAt: *exp}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
