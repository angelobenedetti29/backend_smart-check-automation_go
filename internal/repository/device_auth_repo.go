package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeviceAuthRepository es el adaptador PostgreSQL para la autenticación de
// dispositivos por secret Bearer.
type DeviceAuthRepository struct{ db *pgxpool.Pool }

// NewDeviceAuthRepository crea el repositorio de autenticación de dispositivos.
func NewDeviceAuthRepository(db *pgxpool.Pool) *DeviceAuthRepository {
	return &DeviceAuthRepository{db: db}
}

// LookupActiveBySecretHash resuelve el id del dispositivo activo cuyo secret
// coincide con el hash recibido. Devuelve deviceauth.ErrInvalidToken si no
// existe o si la consulta falla, para no distinguir el error hacia el cliente.
func (r *DeviceAuthRepository) LookupActiveBySecretHash(ctx context.Context, secretHash string) (string, error) {
	var id string
	err := r.db.QueryRow(ctx, `SELECT id FROM dispositivos WHERE secret_hash=$1 AND auth_status='active'`, secretHash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", deviceauth.ErrInvalidToken
	}
	if err != nil {
		return "", fmt.Errorf("%w: lookup device secret", deviceauth.ErrInvalidToken)
	}
	return id, nil
}
