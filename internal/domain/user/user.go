package user

import (
	"context"
	"errors"
	"time"
)

// Roles disponibles en el sistema. Usar estas constantes para comparar roles,
// no strings literales, para evitar typos y facilitar refactors.
const (
	RoleAdmin      = "Administrador"
	RoleSupervisor = "Supervisor"
	RoleOperario   = "Operario"
)

// Errores centinela — usar errors.Is() en el service y el handler.
// Los mensajes son genéricos intencionalmente para no filtrar información al cliente.
var (
	// ErrUserNotFound se retorna cuando el email no está registrado en la DB
	// o el usuario está marcado como activo=false.
	ErrUserNotFound = errors.New("usuario no registrado o inactivo")

	// ErrInvalidToken se retorna cuando el Google ID Token no pasa la verificación
	// de firma, expiración o audience.
	ErrInvalidToken = errors.New("token de google inválido o expirado")

	// ErrInvalidCredentials se retorna cuando el email o la contraseña en el login local son incorrectos.
	ErrInvalidCredentials = errors.New("credenciales inválidas")

	// ErrDuplicateEmail se retorna al intentar crear un usuario con un email ya registrado.
	ErrDuplicateEmail = errors.New("el correo electrónico ya se encuentra registrado")

	// ErrInvalidRole se retorna cuando se intenta asignar un rol no reconocido.
	ErrInvalidRole = errors.New("rol de usuario no válido")

	// ErrCannotDeactivateSelf previene que un administrador desactive su propia cuenta en sesión.
	ErrCannotDeactivateSelf = errors.New("no puedes desactivar tu propia cuenta de usuario")
)

// User es la entidad de dominio del usuario corporativo autorizado.
type User struct {
	ID           string
	Email        string
	Nombre       string
	Rol          string
	PasswordHash string
	Activo       bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Repository define el contrato de persistencia para usuarios.
// La implementación real vive en internal/repository/.
type Repository interface {
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByID(ctx context.Context, id string) (*User, error)
	FindAll(ctx context.Context) ([]*User, error)
	Create(ctx context.Context, u *User) error
	Update(ctx context.Context, u *User) error
}

// GoogleVerifier define el contrato para validar un Google ID Token.
// La implementación vive en internal/provider/google/.
type GoogleVerifier interface {
	Verify(ctx context.Context, idToken string) (*GoogleClaims, error)
}

// GoogleClaims contiene los datos extraídos del token de Google tras la verificación exitosa.
type GoogleClaims struct {
	Email string
	Name  string
}
