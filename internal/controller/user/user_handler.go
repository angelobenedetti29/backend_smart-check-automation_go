package user

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	userService "github.com/angelobenedetti29/smart-check-automation/internal/service/user"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

type UserHandler struct {
	svc *userService.Service
}

func NewUserHandler(svc *userService.Service) *UserHandler {
	return &UserHandler{svc: svc}
}

// UserResponse DTO para exponer usuarios en respuestas JSON (sin password_hash).
type UserResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Nombre    string `json:"nombre"`
	Rol       string `json:"rol"`
	Activo    bool   `json:"activo"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func toUserResponse(u *user.User) UserResponse {
	return UserResponse{
		ID:        u.ID,
		Email:     u.Email,
		Nombre:    u.Nombre,
		Rol:       u.Rol,
		Activo:    u.Activo,
		CreatedAt: u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: u.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// HandleUsers rutea GET (listar) y POST (crear) a /api/v1/admin/usuarios.
func (h *UserHandler) HandleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.ListUsers(w, r)
	case http.MethodPost:
		h.CreateUser(w, r)
	default:
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
	}
}

// HandleUserByID rutea PATCH (actualizar) a /api/v1/admin/usuarios/{id}.
func (h *UserHandler) HandleUserByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	h.UpdateUser(w, r)
}

func (h *UserHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "Error al obtener lista de usuarios", nil)
		return
	}

	var res []UserResponse
	for _, u := range users {
		res = append(res, toUserResponse(u))
	}

	response.JSON(w, http.StatusOK, true, "Usuarios obtenidos exitosamente", res, nil)
}

type createUserReqPayload struct {
	Email    string `json:"email"`
	Nombre   string `json:"nombre"`
	Rol      string `json:"rol"`
	Password string `json:"password"`
}

func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var payload createUserReqPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		response.Error(w, http.StatusBadRequest, "Body JSON inválido", nil)
		return
	}

	u, err := h.svc.CreateUser(r.Context(), userService.CreateUserRequest{
		Email:    payload.Email,
		Nombre:   payload.Nombre,
		Rol:      payload.Rol,
		Password: payload.Password,
	})

	if err != nil {
		if errors.Is(err, user.ErrDuplicateEmail) {
			response.Error(w, http.StatusConflict, err.Error(), nil)
			return
		}
		if errors.Is(err, user.ErrInvalidRole) {
			response.Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		response.Error(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	response.JSON(w, http.StatusCreated, true, "Usuario creado exitosamente", toUserResponse(u), nil)
}

type updateUserReqPayload struct {
	Nombre string `json:"nombre"`
	Rol    string `json:"rol"`
	Activo *bool  `json:"activo"`
}

func (h *UserHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	// Extraer ID de la URL: /api/v1/admin/usuarios/{id}
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 4 {
		response.Error(w, http.StatusBadRequest, "ID de usuario no especificado en la URL", nil)
		return
	}
	userID := pathParts[len(pathParts)-1]

	var payload updateUserReqPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		response.Error(w, http.StatusBadRequest, "Body JSON inválido", nil)
		return
	}

	claims := authController.GetClaimsFromContext(r.Context())
	adminEmail := ""
	if claims != nil {
		adminEmail = claims.Email
	}

	activoVal := true
	if payload.Activo != nil {
		activoVal = *payload.Activo
	}

	u, err := h.svc.UpdateUser(r.Context(), userService.UpdateUserRequest{
		ID:         userID,
		Nombre:     payload.Nombre,
		Rol:        payload.Rol,
		Activo:     activoVal,
		AdminEmail: adminEmail,
	})

	if err != nil {
		if errors.Is(err, user.ErrUserNotFound) {
			response.Error(w, http.StatusNotFound, "Usuario no encontrado", nil)
			return
		}
		if errors.Is(err, user.ErrInvalidRole) || errors.Is(err, user.ErrCannotDeactivateSelf) {
			response.Error(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		response.Error(w, http.StatusInternalServerError, "Error al actualizar usuario", nil)
		return
	}

	response.JSON(w, http.StatusOK, true, "Usuario actualizado exitosamente", toUserResponse(u), nil)
}
