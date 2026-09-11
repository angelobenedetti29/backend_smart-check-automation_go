package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
)


// --- Mocks ---

type mockGoogleVerifier struct {
	verifyFunc func(ctx context.Context, idToken string) (*user.GoogleClaims, error)
}

func (m *mockGoogleVerifier) Verify(ctx context.Context, idToken string) (*user.GoogleClaims, error) {
	return m.verifyFunc(ctx, idToken)
}

type mockUserRepo struct {
	findByEmailFunc func(ctx context.Context, email string) (*user.User, error)
}

func (m *mockUserRepo) FindByEmail(ctx context.Context, email string) (*user.User, error) {
	return m.findByEmailFunc(ctx, email)
}

func (m *mockUserRepo) FindByID(_ context.Context, _ string) (*user.User, error) {
	return nil, user.ErrUserNotFound
}

func (m *mockUserRepo) FindAll(_ context.Context) ([]*user.User, error) {
	return nil, nil
}

func (m *mockUserRepo) Create(_ context.Context, _ *user.User) error {
	return nil
}

func (m *mockUserRepo) Update(_ context.Context, _ *user.User) error {
	return nil
}

// jwtTestSecret es un secret de 32 bytes válido solo para tests.
const jwtTestSecret = "test-secret-32-chars-exactly!!!"

func newTestService(verifier user.GoogleVerifier, repo user.Repository) *AuthService {
	return NewAuthService(verifier, repo, jwtTestSecret)
}

// --- Tests ---

func TestLoginWithGoogle_TokenGoogleInvalido(t *testing.T) {
	// DADO: un Google verifier que rechaza el token
	verifier := &mockGoogleVerifier{
		verifyFunc: func(ctx context.Context, idToken string) (*user.GoogleClaims, error) {
			return nil, user.ErrInvalidToken
		},
	}
	// El repo nunca debe ser llamado si el token es inválido
	repo := &mockUserRepo{
		findByEmailFunc: func(ctx context.Context, email string) (*user.User, error) {
			t.Error("FindByEmail no debería llamarse con token inválido")
			return nil, nil
		},
	}

	svc := newTestService(verifier, repo)
	_, err := svc.LoginWithGoogle(context.Background(), "token-adulterado")

	assert.ErrorIs(t, err, user.ErrInvalidToken)
}

func TestLoginWithGoogle_UsuarioNoCorporativo(t *testing.T) {
	// DADO: token de Google válido pero email no registrado en DB
	verifier := &mockGoogleVerifier{
		verifyFunc: func(ctx context.Context, idToken string) (*user.GoogleClaims, error) {
			return &user.GoogleClaims{Email: "externo@gmail.com", Name: "Externo"}, nil
		},
	}
	repo := &mockUserRepo{
		findByEmailFunc: func(ctx context.Context, email string) (*user.User, error) {
			assert.Equal(t, "externo@gmail.com", email)
			return nil, user.ErrUserNotFound
		},
	}

	svc := newTestService(verifier, repo)
	_, err := svc.LoginWithGoogle(context.Background(), "valid-google-token")

	assert.ErrorIs(t, err, user.ErrUserNotFound)
}

func TestLoginWithGoogle_Exitoso_RetornaJWTFirmado(t *testing.T) {
	// DADO: token válido y usuario corporativo existente
	verifier := &mockGoogleVerifier{
		verifyFunc: func(ctx context.Context, idToken string) (*user.GoogleClaims, error) {
			return &user.GoogleClaims{Email: "admin@fermar.com.ar", Name: "Admin Google"}, nil
		},
	}
	repo := &mockUserRepo{
		findByEmailFunc: func(ctx context.Context, email string) (*user.User, error) {
			return &user.User{
				ID:     "uuid-1",
				Email:  "admin@fermar.com.ar",
				Nombre: "Administrador Fermar", // nombre de la DB, no de Google
				Rol:    user.RoleAdmin,
			}, nil
		},
	}

	svc := newTestService(verifier, repo)
	tokenStr, err := svc.LoginWithGoogle(context.Background(), "valid-google-token")

	require.NoError(t, err)
	assert.NotEmpty(t, tokenStr, "el JWT no debe estar vacío")
}

func TestLoginWithGoogle_ClaimsContienenDatosCorporativos(t *testing.T) {
	// Verificar que los claims del JWT provienen de la DB, no de Google.
	// El nombre/rol del usuario corporativo tiene precedencia sobre el token de Google.
	verifier := &mockGoogleVerifier{
		verifyFunc: func(ctx context.Context, idToken string) (*user.GoogleClaims, error) {
			return &user.GoogleClaims{Email: "op@fermar.com.ar", Name: "Nombre En Google"}, nil
		},
	}
	repo := &mockUserRepo{
		findByEmailFunc: func(ctx context.Context, email string) (*user.User, error) {
			return &user.User{
				ID:     "uuid-2",
				Email:  "op@fermar.com.ar",
				Nombre: "Operario Fermar", // nombre corporativo definido en DB, no el de Google
				Rol:    user.RoleOperario,
			}, nil
		},
	}

	svc := newTestService(verifier, repo)
	tokenStr, err := svc.LoginWithGoogle(context.Background(), "valid-google-token")
	require.NoError(t, err)

	// Parsear el JWT con la misma clave para verificar los claims
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(jwtTestSecret), nil
	})

	require.NoError(t, err)
	assert.True(t, token.Valid)
	assert.Equal(t, "op@fermar.com.ar", claims.Email)
	assert.Equal(t, "Operario Fermar", claims.Name)   // nombre de la DB, no de Google
	assert.Equal(t, user.RoleOperario, claims.Role)
	assert.Equal(t, "smart-check-automation", claims.Issuer)
}


func TestLoginWithGoogle_ErrorDeInfraestructura(t *testing.T) {
	// Error de BD que no es ErrUserNotFound — debe propagarse como error interno
	verifier := &mockGoogleVerifier{
		verifyFunc: func(ctx context.Context, idToken string) (*user.GoogleClaims, error) {
			return &user.GoogleClaims{Email: "user@fermar.com.ar", Name: "User"}, nil
		},
	}
	infraError := errors.New("connection timeout")
	repo := &mockUserRepo{
		findByEmailFunc: func(ctx context.Context, email string) (*user.User, error) {
			return nil, infraError
		},
	}

	svc := newTestService(verifier, repo)
	_, err := svc.LoginWithGoogle(context.Background(), "valid-google-token")

	assert.Error(t, err)
	assert.NotErrorIs(t, err, user.ErrUserNotFound)
	assert.NotErrorIs(t, err, user.ErrInvalidToken)
}

func TestLoginWithGoogle_DeadlineExcedido(t *testing.T) {
	// El verifier agota el deadline: el service debe propagar el error de
	// contexto en vez de enmascararlo como token inválido.
	verifier := &mockGoogleVerifier{
		verifyFunc: func(ctx context.Context, idToken string) (*user.GoogleClaims, error) {
			return nil, context.DeadlineExceeded
		},
	}
	svc := newTestService(verifier, &mockUserRepo{})

	_, err := svc.LoginWithGoogle(context.Background(), "valid-google-token")

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotErrorIs(t, err, user.ErrInvalidToken)
}

func TestLoginWithCredentials_DeadlineExcedido(t *testing.T) {
	// El repo agota el deadline: el error de contexto debe llegar envuelto (%w)
	// hasta el caller para que el handler responda un timeout controlado.
	repo := &mockUserRepo{
		findByEmailFunc: func(ctx context.Context, email string) (*user.User, error) {
			return nil, context.DeadlineExceeded
		},
	}
	svc := newTestService(nil, repo)

	_, err := svc.LoginWithCredentials(context.Background(), "user@fermar.com.ar", "password123")

	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestLoginWithCredentials_CamposVacios(t *testing.T) {
	svc := newTestService(nil, nil)

	_, err := svc.LoginWithCredentials(context.Background(), "", "password")
	assert.ErrorIs(t, err, user.ErrInvalidCredentials)

	_, err = svc.LoginWithCredentials(context.Background(), "user@test.com", "")
	assert.ErrorIs(t, err, user.ErrInvalidCredentials)
}

func TestLoginWithCredentials_UsuarioNoEncontrado(t *testing.T) {
	repo := &mockUserRepo{
		findByEmailFunc: func(ctx context.Context, email string) (*user.User, error) {
			return nil, user.ErrUserNotFound
		},
	}
	svc := newTestService(nil, repo)

	_, err := svc.LoginWithCredentials(context.Background(), "inexistente@fermar.com.ar", "password123")
	assert.ErrorIs(t, err, user.ErrInvalidCredentials)
}

func TestLoginWithCredentials_UsuarioSinContrasenaHash(t *testing.T) {
	repo := &mockUserRepo{
		findByEmailFunc: func(ctx context.Context, email string) (*user.User, error) {
			return &user.User{
				Email:        "oauthonly@fermar.com.ar",
				Nombre:       "OAuth User",
				Rol:          user.RoleSupervisor,
				PasswordHash: "", // usuario solo registrado via Google OAuth
			}, nil
		},
	}
	svc := newTestService(nil, repo)

	_, err := svc.LoginWithCredentials(context.Background(), "oauthonly@fermar.com.ar", "password123")
	assert.ErrorIs(t, err, user.ErrInvalidCredentials)
}

func TestLoginWithCredentials_ContrasenaIncorrecta(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	validHash := string(hash)
	repo := &mockUserRepo{
		findByEmailFunc: func(ctx context.Context, email string) (*user.User, error) {
			return &user.User{
				Email:        "supervisor@fermar.com.ar",
				Nombre:       "Supervisor",
				Rol:          user.RoleSupervisor,
				PasswordHash: validHash,
			}, nil
		},
	}
	svc := newTestService(nil, repo)

	_, err := svc.LoginWithCredentials(context.Background(), "supervisor@fermar.com.ar", "wrongpassword")
	assert.ErrorIs(t, err, user.ErrInvalidCredentials)
}

func TestLoginWithCredentials_Exitoso(t *testing.T) {
	hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	validHash := string(hash)
	repo := &mockUserRepo{
		findByEmailFunc: func(ctx context.Context, email string) (*user.User, error) {
			return &user.User{
				Email:        "supervisor@fermar.com.ar",
				Nombre:       "Supervisor Fermar",
				Rol:          user.RoleSupervisor,
				PasswordHash: validHash,
			}, nil
		},
	}
	svc := newTestService(nil, repo)

	tokenStr, err := svc.LoginWithCredentials(context.Background(), "supervisor@fermar.com.ar", "password123")
	require.NoError(t, err)
	assert.NotEmpty(t, tokenStr)

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		return []byte(jwtTestSecret), nil
	})
	require.NoError(t, err)
	assert.True(t, token.Valid)
	assert.Equal(t, "supervisor@fermar.com.ar", claims.Email)
	assert.Equal(t, "Supervisor Fermar", claims.Name)
	assert.Equal(t, user.RoleSupervisor, claims.Role)
}
