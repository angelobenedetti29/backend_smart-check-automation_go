package user

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
)

// Service maneja la lógica de administración de usuarios y permisos.
type Service struct {
	repo user.Repository
}

// NewService crea una instancia de Service inyectando el repositorio.
func NewService(repo user.Repository) *Service {
	return &Service{repo: repo}
}

// ListUsers devuelve la lista completa de usuarios corporativos registrados.
func (s *Service) ListUsers(ctx context.Context) ([]*user.User, error) {
	return s.repo.FindAll(ctx)
}

// CreateUserRequest payload para la creación de un nuevo usuario desde el panel admin.
type CreateUserRequest struct {
	Email    string
	Nombre   string
	Rol      string
	Password string // Opcional: si está vacío, el usuario ingresará mediante Google OAuth
}

// CreateUser crea un nuevo usuario validando roles, duplicados y aplicando bcrypt si hay contraseña.
func (s *Service) CreateUser(ctx context.Context, req CreateUserRequest) (*user.User, error) {
	email := strings.TrimSpace(strings.ToLower(req.Email))
	nombre := strings.TrimSpace(req.Nombre)
	rol := strings.TrimSpace(req.Rol)

	if email == "" || nombre == "" || rol == "" {
		return nil, errors.New("email, nombre y rol son requeridos")
	}

	if !isValidRole(rol) {
		return nil, user.ErrInvalidRole
	}

	var passHash string
	if req.Password != "" {
		hashBytes, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("error al generar hash de contraseña: %w", err)
		}
		passHash = string(hashBytes)
	}

	newUser := &user.User{
		Email:        email,
		Nombre:       nombre,
		Rol:          rol,
		PasswordHash: passHash,
		Activo:       true,
	}

	if err := s.repo.Create(ctx, newUser); err != nil {
		return nil, err
	}

	return newUser, nil
}

// UpdateUserRequest payload para actualizar datos de un usuario.
type UpdateUserRequest struct {
	ID        string
	Nombre    string
	Rol       string
	Activo    bool
	AdminEmail string // Email del administrador que ejecuta la acción
}

// UpdateUser actualiza rol, estado activo y/o nombre de un usuario.
func (s *Service) UpdateUser(ctx context.Context, req UpdateUserRequest) (*user.User, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return nil, errors.New("ID de usuario requerido")
	}

	existingUser, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Prevenir que el admin se desactive a sí mismo
	if !req.Activo && strings.EqualFold(existingUser.Email, req.AdminEmail) {
		return nil, user.ErrCannotDeactivateSelf
	}

	if req.Rol != "" {
		if !isValidRole(req.Rol) {
			return nil, user.ErrInvalidRole
		}
		existingUser.Rol = req.Rol
	}

	if req.Nombre != "" {
		existingUser.Nombre = strings.TrimSpace(req.Nombre)
	}

	existingUser.Activo = req.Activo

	if err := s.repo.Update(ctx, existingUser); err != nil {
		return nil, err
	}

	return existingUser, nil
}

func isValidRole(r string) bool {
	return r == user.RoleAdmin || r == user.RoleSupervisor || r == user.RoleOperario
}
