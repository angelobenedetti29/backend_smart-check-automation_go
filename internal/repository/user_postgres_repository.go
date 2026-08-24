package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
)

// UserPostgresRepository implementa user.Repository contra PostgreSQL real.
type UserPostgresRepository struct {
	db *pgxpool.Pool
}

// NewUserPostgresRepository crea el repositorio de usuarios con el pool de conexiones.
func NewUserPostgresRepository(db *pgxpool.Pool) *UserPostgresRepository {
	return &UserPostgresRepository{db: db}
}

// FindByEmail busca un usuario activo por su email corporativo.
func (r *UserPostgresRepository) FindByEmail(ctx context.Context, email string) (*user.User, error) {
	const query = `
		SELECT id, email, nombre, rol, COALESCE(password_hash, ''), activo, created_at, updated_at
		FROM usuarios
		WHERE email = $1
		  AND activo = true
		LIMIT 1`

	var u user.User
	err := r.db.QueryRow(ctx, query, email).Scan(
		&u.ID, &u.Email, &u.Nombre, &u.Rol, &u.PasswordHash, &u.Activo, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, user.ErrUserNotFound
		}
		return nil, err
	}

	return &u, nil
}

// FindByID busca cualquier usuario (activo o inactivo) por su ID UUID.
func (r *UserPostgresRepository) FindByID(ctx context.Context, id string) (*user.User, error) {
	const query = `
		SELECT id, email, nombre, rol, COALESCE(password_hash, ''), activo, created_at, updated_at
		FROM usuarios
		WHERE id = $1
		LIMIT 1`

	var u user.User
	err := r.db.QueryRow(ctx, query, id).Scan(
		&u.ID, &u.Email, &u.Nombre, &u.Rol, &u.PasswordHash, &u.Activo, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, user.ErrUserNotFound
		}
		return nil, err
	}

	return &u, nil
}

// FindAll devuelve todos los usuarios registrados ordenados por fecha de creación desc.
func (r *UserPostgresRepository) FindAll(ctx context.Context) ([]*user.User, error) {
	const query = `
		SELECT id, email, nombre, rol, COALESCE(password_hash, ''), activo, created_at, updated_at
		FROM usuarios
		ORDER BY created_at DESC`

	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("error al listar usuarios: %w", err)
	}
	defer rows.Close()

	var users []*user.User
	for rows.Next() {
		var u user.User
		err := rows.Scan(
			&u.ID, &u.Email, &u.Nombre, &u.Rol, &u.PasswordHash, &u.Activo, &u.CreatedAt, &u.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("error al escanear usuario: %w", err)
		}
		users = append(users, &u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error en iteración de usuarios: %w", err)
	}

	return users, nil
}

// Create inserta un nuevo usuario corporativo en PostgreSQL.
func (r *UserPostgresRepository) Create(ctx context.Context, u *user.User) error {
	const query = `
		INSERT INTO usuarios (email, nombre, rol, password_hash, activo)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		RETURNING id, created_at, updated_at`

	err := r.db.QueryRow(ctx, query, u.Email, u.Nombre, u.Rol, u.PasswordHash, u.Activo).Scan(
		&u.ID, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return user.ErrDuplicateEmail
		}
		return fmt.Errorf("error al crear usuario en BD: %w", err)
	}

	return nil
}

// Update actualiza nombre, rol, password_hash, activo y updated_at del usuario.
func (r *UserPostgresRepository) Update(ctx context.Context, u *user.User) error {
	const query = `
		UPDATE usuarios
		SET nombre = $1,
		    rol = $2,
		    password_hash = CASE WHEN $3 = '' THEN password_hash ELSE $3 END,
		    activo = $4,
		    updated_at = NOW()
		WHERE id = $5
		RETURNING updated_at`

	err := r.db.QueryRow(ctx, query, u.Nombre, u.Rol, u.PasswordHash, u.Activo, u.ID).Scan(&u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return user.ErrUserNotFound
		}
		return fmt.Errorf("error al actualizar usuario en BD: %w", err)
	}

	return nil
}
