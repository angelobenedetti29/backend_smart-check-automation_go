package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/registro"
)

// RegistrationRepository implementa registro.Repository usando pgxpool.
// Persiste las solicitudes de alta de nodos, su resolución por un supervisor y
// la entrega única del secret de autenticación.
type RegistrationRepository struct {
	db *pgxpool.Pool
}

// NewRegistrationRepository crea el repositorio real de solicitudes de registro.
func NewRegistrationRepository(db *pgxpool.Pool) *RegistrationRepository {
	return &RegistrationRepository{db: db}
}

// beginRegistrationLocked abre una transacción y toma un advisory lock
// transaccional para serializar operaciones sobre la misma clave lógica.
func beginRegistrationLocked(ctx context.Context, db *pgxpool.Pool, key string) (pgx.Tx, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

// registrationActorID resuelve el UUID del usuario activo por email. Se usa
// como resolved_by en la solicitud y como actor_id en la auditoría.
func (r *RegistrationRepository) registrationActorID(ctx context.Context, tx pgx.Tx, email string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM usuarios WHERE email=$1 AND activo=true`, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errors.New("actor not found")
	}
	return id, err
}

// Issue crea la solicitud PENDING de un hostname con su tipo de dispositivo. Si
// ya existe una solicitud vigente para el mismo hostname, devuelve la misma
// (idempotente) actualizando el tipo al último solicitado. Las solicitudes
// vencidas se marcan EXPIRED antes de decidir.
func (r *RegistrationRepository) Issue(ctx context.Context, hostname, tipo, requestID string, expiresAt time.Time) (*registro.IssueResponse, error) {
	tx, err := beginRegistrationLocked(ctx, r.db, "registration-issue:"+hostname)
	if err != nil {
		return nil, fmt.Errorf("issue registration: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var tipoVal *string
	if tipo != "" {
		tipoVal = &tipo
	}

	// Las solicitudes vencidas dejan de bloquear el hostname.
	if _, err = tx.Exec(ctx, `
		UPDATE registration_requests
		SET status='EXPIRED', resolved_at=clock_timestamp()
		WHERE hostname=$1 AND status='PENDING' AND expires_at<=clock_timestamp()`, hostname); err != nil {
		return nil, fmt.Errorf("issue registration: expire stale: %w", err)
	}

	var existing string
	var existingTipo *string
	err = tx.QueryRow(ctx, `
		SELECT request_id, tipo FROM registration_requests
		WHERE hostname=$1 AND status='PENDING' AND expires_at>clock_timestamp()
		ORDER BY created_at DESC LIMIT 1`, hostname).Scan(&existing, &existingTipo)
	if err == nil {
		// Último tipo gana: si la solicitud PENDING ya existente trae otro tipo,
		// se actualiza para reflejar el pedido más reciente del nodo.
		if tipoValue(existingTipo) != tipoValue(tipoVal) {
			if _, uerr := tx.Exec(ctx,
				`UPDATE registration_requests SET tipo=$1 WHERE request_id=$2`, tipoVal, existing); uerr != nil {
				return nil, fmt.Errorf("issue registration: update pending tipo: %w", uerr)
			}
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("issue registration: commit: %w", err)
		}
		return &registro.IssueResponse{RequestID: existing, Status: registro.StatusPending}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("issue registration: lookup pending: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO registration_requests (request_id, hostname, tipo, expires_at)
		VALUES ($1, $2, $3, $4)`, requestID, hostname, tipoVal, expiresAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// El índice único parcial ganó la carrera: una transacción ya falló,
			// así que se descarta y se lee la solicitud existente en una conexión
			// nueva (una consulta posterior en una tx abortada fallaría).
			_ = tx.Rollback(ctx)
			var won string
			var wonTipo *string
			if qerr := r.db.QueryRow(ctx, `
				SELECT request_id, tipo FROM registration_requests
				WHERE hostname=$1 AND status='PENDING'
				ORDER BY created_at DESC LIMIT 1`, hostname).Scan(&won, &wonTipo); qerr != nil {
				return nil, fmt.Errorf("issue registration: resolve duplicate: %w", qerr)
			}
			if tipoValue(wonTipo) != tipoValue(tipoVal) {
				if _, uerr := r.db.Exec(ctx,
					`UPDATE registration_requests SET tipo=$1 WHERE request_id=$2`, tipoVal, won); uerr != nil {
					return nil, fmt.Errorf("issue registration: update duplicate tipo: %w", uerr)
				}
			}
			return &registro.IssueResponse{RequestID: won, Status: registro.StatusPending}, nil
		}
		return nil, fmt.Errorf("issue registration: insert: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("issue registration: commit: %w", err)
	}
	return &registro.IssueResponse{RequestID: requestID, Status: registro.StatusPending}, nil
}

// tipoValue normaliza un *string nullable a string vacío para comparaciones.
func tipoValue(t *string) string {
	if t == nil {
		return ""
	}
	return *t
}

// ListPending devuelve las solicitudes PENDING vigentes, más recientes primero.
func (r *RegistrationRepository) ListPending(ctx context.Context) ([]registro.RegistrationRequest, error) {
	rows, err := r.db.Query(ctx, `
		SELECT request_id, hostname, tipo, status, device_id, created_at, expires_at
		FROM registration_requests
		WHERE status='PENDING' AND expires_at>clock_timestamp()
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list pending registrations: %w", err)
	}
	defer rows.Close()

	out := []registro.RegistrationRequest{}
	for rows.Next() {
		var x registro.RegistrationRequest
		var deviceID *string
		var tipo *string
		if err := rows.Scan(&x.RequestID, &x.Hostname, &tipo, &x.Status, &deviceID, &x.CreatedAt, &x.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan pending registration: %w", err)
		}
		x.Type = tipo
		x.DeviceID = deviceID
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending registrations: %w", err)
	}
	return out, nil
}

// Approve resuelve la solicitud como APPROVED: valida vigencia, crea el
// dispositivo activo con el hash del secret, extiende el vencimiento a la
// ventana de pickup y deja traza de auditoría. Todo en una transacción.
func (r *RegistrationRepository) Approve(ctx context.Context, actor, requestID, secret, secretHash string, pickupExpiresAt time.Time) (*registro.Approval, error) {
	tx, err := beginRegistrationLocked(ctx, r.db, "registration:"+requestID)
	if err != nil {
		return nil, fmt.Errorf("approve registration: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	actorID, err := r.registrationActorID(ctx, tx, actor)
	if err != nil {
		return nil, fmt.Errorf("approve registration: %w", err)
	}

	var status string
	var expiresAt time.Time
	var tipo *string
	err = tx.QueryRow(ctx, `SELECT status, expires_at, tipo FROM registration_requests WHERE request_id=$1 FOR UPDATE`, requestID).Scan(&status, &expiresAt, &tipo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, registro.ErrRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("approve registration: load request: %w", err)
	}

	var dbNow time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		return nil, fmt.Errorf("approve registration: clock: %w", err)
	}
	if status == registro.StatusExpired || !dbNow.Before(expiresAt) {
		if _, err = tx.Exec(ctx, `UPDATE registration_requests SET status='EXPIRED', resolved_at=clock_timestamp() WHERE request_id=$1`, requestID); err != nil {
			return nil, fmt.Errorf("approve registration: persist expiry: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("approve registration: commit expiry: %w", err)
		}
		return nil, registro.ErrRequestExpired
	}
	if status != registro.StatusPending {
		return nil, registro.ErrRequestNotPending
	}

	var hostname string
	if err = tx.QueryRow(ctx, `SELECT hostname FROM registration_requests WHERE request_id=$1`, requestID).Scan(&hostname); err != nil {
		return nil, fmt.Errorf("approve registration: hostname: %w", err)
	}

	var deviceID string
	if err = tx.QueryRow(ctx, `
		INSERT INTO dispositivos (nombre, tipo, auth_status, secret_hash, auth_updated_at)
		VALUES ($1, $2, 'active', $3, clock_timestamp())
		RETURNING id`, hostname, tipo, secretHash).Scan(&deviceID); err != nil {
		return nil, fmt.Errorf("approve registration: create device: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE registration_requests
		SET status='APPROVED', device_id=$1, secret=$2, resolved_by=$3, resolved_at=clock_timestamp(), expires_at=$4
		WHERE request_id=$5`, deviceID, secret, actorID, pickupExpiresAt, requestID); err != nil {
		return nil, fmt.Errorf("approve registration: update request: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO device_lifecycle_audit (actor_id, action, dispositivo_id, request_id, old_status, new_status)
		VALUES ($1, 'approve', $2, $3, 'unenrolled', 'active')`, actorID, deviceID, requestID); err != nil {
		return nil, fmt.Errorf("approve registration: audit: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("approve registration: commit: %w", err)
	}
	return &registro.Approval{RequestID: requestID, Status: registro.StatusApproved, DeviceID: deviceID}, nil
}

// Reject resuelve la solicitud como REJECTED, descarta cualquier secret y deja
// traza de auditoría. Todo en una transacción.
func (r *RegistrationRepository) Reject(ctx context.Context, actor, requestID string) error {
	tx, err := beginRegistrationLocked(ctx, r.db, "registration:"+requestID)
	if err != nil {
		return fmt.Errorf("reject registration: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	actorID, err := r.registrationActorID(ctx, tx, actor)
	if err != nil {
		return fmt.Errorf("reject registration: %w", err)
	}

	var status string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT status, expires_at FROM registration_requests WHERE request_id=$1 FOR UPDATE`, requestID).Scan(&status, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return registro.ErrRequestNotFound
	}
	if err != nil {
		return fmt.Errorf("reject registration: load request: %w", err)
	}

	var dbNow time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		return fmt.Errorf("reject registration: clock: %w", err)
	}
	if status == registro.StatusExpired || !dbNow.Before(expiresAt) {
		if _, err = tx.Exec(ctx, `UPDATE registration_requests SET status='EXPIRED', resolved_at=clock_timestamp() WHERE request_id=$1`, requestID); err != nil {
			return fmt.Errorf("reject registration: persist expiry: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("reject registration: commit expiry: %w", err)
		}
		return registro.ErrRequestExpired
	}
	if status != registro.StatusPending {
		return registro.ErrRequestNotPending
	}

	if _, err = tx.Exec(ctx, `
		UPDATE registration_requests
		SET status='REJECTED', resolved_by=$1, resolved_at=clock_timestamp(), secret=NULL
		WHERE request_id=$2`, actorID, requestID); err != nil {
		return fmt.Errorf("reject registration: update request: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO device_lifecycle_audit (actor_id, action, dispositivo_id, request_id, old_status, new_status)
		VALUES ($1, 'reject', NULL, $2, 'unenrolled', 'unenrolled')`, actorID, requestID); err != nil {
		return fmt.Errorf("reject registration: audit: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("reject registration: commit: %w", err)
	}
	return nil
}

// Pickup entrega el secret de una solicitud aprobada una única vez. Usa un
// UPDATE ... RETURNING con CTE para que solo un poll concurrente obtenga el
// secret; en los demás casos devuelve el estado actual sin secret.
func (r *RegistrationRepository) Pickup(ctx context.Context, requestID string) (*registro.Pickup, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pickup registration: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var deviceID, secret string
	var tipo *string
	err = tx.QueryRow(ctx, `
		WITH picked AS (
			SELECT request_id, device_id, secret, status, expires_at, tipo
			FROM registration_requests
			WHERE request_id=$1
			FOR UPDATE
		)
		UPDATE registration_requests r
		SET secret=NULL
		FROM picked p
		WHERE r.request_id=p.request_id
		  AND p.status='APPROVED'
		  AND p.secret IS NOT NULL
		  AND p.expires_at>clock_timestamp()
		RETURNING r.device_id, p.secret, p.tipo`, requestID).Scan(&deviceID, &secret, &tipo)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("pickup registration: commit delivery: %w", err)
		}
		return &registro.Pickup{Status: registro.StatusApproved, DeviceID: &deviceID, Secret: &secret, Type: tipo}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("pickup registration: deliver: %w", err)
	}

	var status string
	var device *string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT status, device_id, expires_at, tipo FROM registration_requests WHERE request_id=$1`, requestID).Scan(&status, &device, &expiresAt, &tipo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, registro.ErrRequestNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pickup registration: reload: %w", err)
	}

	var dbNow time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		return nil, fmt.Errorf("pickup registration: clock: %w", err)
	}
	if status == registro.StatusPending && !dbNow.Before(expiresAt) {
		if _, err = tx.Exec(ctx, `UPDATE registration_requests SET status='EXPIRED', resolved_at=clock_timestamp() WHERE request_id=$1`, requestID); err != nil {
			return nil, fmt.Errorf("pickup registration: persist expiry: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("pickup registration: commit expiry: %w", err)
		}
		return &registro.Pickup{Status: registro.StatusExpired, Type: tipo}, nil
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pickup registration: commit: %w", err)
	}
	result := &registro.Pickup{Status: status, Type: tipo}
	if status == registro.StatusApproved {
		result.DeviceID = device
	}
	return result, nil
}
