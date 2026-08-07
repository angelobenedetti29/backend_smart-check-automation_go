package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
)

// JWTDuration define el tiempo de vida del token interno de sesión.
// Coincide con MaxAge de la cookie para que ambos expiren a la vez.
const JWTDuration = 8 * time.Hour

// Claims define el payload del JWT interno firmado por este backend.
// Solo incluye los datos necesarios para control de acceso en el frontend.
type Claims struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	jwt.RegisteredClaims
}

// AuthService orquesta la autenticación: verificación Google / credenciales locales → validación corporativa → JWT.
type AuthService struct {
	verifier  user.GoogleVerifier
	userRepo  user.Repository
	jwtSecret []byte // leído desde JWT_SECRET en env, nunca hardcodeado
}

// NewAuthService crea el servicio inyectando sus dependencias via interfaces.
func NewAuthService(v user.GoogleVerifier, r user.Repository, secret string) *AuthService {
	return &AuthService{
		verifier:  v,
		userRepo:  r,
		jwtSecret: []byte(secret),
	}
}

// LoginWithCredentials ejecuta el flujo de autenticación local con email y contraseña:
//  1. Valida los parámetros de entrada.
//  2. Busca el usuario en la base de datos por email.
//  3. Compara el hash bcrypt de la contraseña.
//  4. Emite el token JWT del sistema.
func (s *AuthService) LoginWithCredentials(ctx context.Context, email, password string) (string, error) {
	if email == "" || password == "" {
		return "", user.ErrInvalidCredentials
	}

	u, err := s.userRepo.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			// Retornar ErrInvalidCredentials genérico para no permitir enumeración de usuarios
			return "", user.ErrInvalidCredentials
		}
		return "", fmt.Errorf("error al buscar usuario para login local: %w", err)
	}

	if u.PasswordHash == "" {
		// El usuario existe pero no tiene contraseña registrada (ingresa por OAuth)
		return "", user.ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return "", user.ErrInvalidCredentials
	}

	return s.generateJWT(u)
}

// LoginWithGoogle ejecuta el flujo completo de autenticación con Google OAuth.
func (s *AuthService) LoginWithGoogle(ctx context.Context, googleToken string) (string, error) {
	// 1. Verificar firma y claims del token de Google
	googleClaims, err := s.verifier.Verify(ctx, googleToken)
	if err != nil {
		// Cualquier error del verifier se trata como token inválido
		return "", user.ErrInvalidToken
	}

	// 2. Verificar que el usuario corporativo esté autorizado en la DB
	u, err := s.userRepo.FindByEmail(ctx, googleClaims.Email)
	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			return "", user.ErrUserNotFound
		}
		// Error de infraestructura (BD caída, timeout, etc.)
		return "", fmt.Errorf("error al verificar usuario corporativo: %w", err)
	}

	return s.generateJWT(u)
}

func (s *AuthService) generateJWT(u *user.User) (string, error) {
	jwtClaims := Claims{
		Email: u.Email,
		Name:  u.Nombre,
		Role:  u.Rol,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(JWTDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "smart-check-automation",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwtClaims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("error al firmar token JWT: %w", err)
	}

	return signed, nil
}
