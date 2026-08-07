package user_test

import (
	"context"
	"testing"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	userService "github.com/angelobenedetti29/smart-check-automation/internal/service/user"
)

type mockUserRepo struct {
	users []*user.User
}

func (m *mockUserRepo) FindByEmail(ctx context.Context, email string) (*user.User, error) {
	for _, u := range m.users {
		if u.Email == email && u.Activo {
			return u, nil
		}
	}
	return nil, user.ErrUserNotFound
}

func (m *mockUserRepo) FindByID(ctx context.Context, id string) (*user.User, error) {
	for _, u := range m.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, user.ErrUserNotFound
}

func (m *mockUserRepo) FindAll(ctx context.Context) ([]*user.User, error) {
	return m.users, nil
}

func (m *mockUserRepo) Create(ctx context.Context, u *user.User) error {
	for _, existing := range m.users {
		if existing.Email == u.Email {
			return user.ErrDuplicateEmail
		}
	}
	u.ID = "usr-123"
	u.CreatedAt = time.Now()
	u.UpdatedAt = time.Now()
	m.users = append(m.users, u)
	return nil
}

func (m *mockUserRepo) Update(ctx context.Context, u *user.User) error {
	for i, existing := range m.users {
		if existing.ID == u.ID {
			m.users[i] = u
			m.users[i].UpdatedAt = time.Now()
			return nil
		}
	}
	return user.ErrUserNotFound
}

func TestUserService_CreateUser(t *testing.T) {
	repo := &mockUserRepo{}
	svc := userService.NewService(repo)

	t.Run("Creación exitosa de usuario", func(t *testing.T) {
		req := userService.CreateUserRequest{
			Email:  "nuevo@fermar.com.ar",
			Nombre: "Nuevo Usuario",
			Rol:    user.RoleSupervisor,
		}

		u, err := svc.CreateUser(context.Background(), req)
		if err != nil {
			t.Fatalf("se esperaba error nil, se obtuvo: %v", err)
		}

		if u.Email != "nuevo@fermar.com.ar" {
			t.Errorf("se esperaba email nuevo@fermar.com.ar, se obtuvo %s", u.Email)
		}
		if u.Rol != user.RoleSupervisor {
			t.Errorf("se esperaba rol Supervisor, se obtuvo %s", u.Rol)
		}
	})

	t.Run("Error al crear usuario duplicado", func(t *testing.T) {
		req := userService.CreateUserRequest{
			Email:  "nuevo@fermar.com.ar",
			Nombre: "Otro Usuario",
			Rol:    user.RoleOperario,
		}

		_, err := svc.CreateUser(context.Background(), req)
		if err == nil {
			t.Fatal("se esperaba error de email duplicado, se obtuvo nil")
		}
	})

	t.Run("Error con rol inválido", func(t *testing.T) {
		req := userService.CreateUserRequest{
			Email:  "invalido@fermar.com.ar",
			Nombre: "Juan",
			Rol:    "RolInexistente",
		}

		_, err := svc.CreateUser(context.Background(), req)
		if err == nil {
			t.Fatal("se esperaba error de rol inválido, se obtuvo nil")
		}
	})
}

func TestUserService_UpdateUser(t *testing.T) {
	repo := &mockUserRepo{
		users: []*user.User{
			{
				ID:     "usr-admin",
				Email:  "admin@fermar.com.ar",
				Nombre: "Admin",
				Rol:    user.RoleAdmin,
				Activo: true,
			},
			{
				ID:     "usr-operario",
				Email:  "op@fermar.com.ar",
				Nombre: "Operario",
				Rol:    user.RoleOperario,
				Activo: true,
			},
		},
	}
	svc := userService.NewService(repo)

	t.Run("Prevenir deshabilitar la propia cuenta", func(t *testing.T) {
		req := userService.UpdateUserRequest{
			ID:         "usr-admin",
			Activo:     false,
			AdminEmail: "admin@fermar.com.ar",
		}

		_, err := svc.UpdateUser(context.Background(), req)
		if err == nil {
			t.Fatal("se esperaba error al intentar desactivar la propia cuenta admin")
		}
	})

	t.Run("Actualizar rol de operario a supervisor", func(t *testing.T) {
		req := userService.UpdateUserRequest{
			ID:         "usr-operario",
			Rol:        user.RoleSupervisor,
			Activo:     true,
			AdminEmail: "admin@fermar.com.ar",
		}

		u, err := svc.UpdateUser(context.Background(), req)
		if err != nil {
			t.Fatalf("se esperaba error nil, se obtuvo: %v", err)
		}

		if u.Rol != user.RoleSupervisor {
			t.Errorf("se esperaba rol Supervisor, se obtuvo %s", u.Rol)
		}
	})
}
