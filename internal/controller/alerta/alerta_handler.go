// Package controller expone los endpoints de alertas del panel:
//
//	GET  /api/v1/alertas              (cualquier rol autenticado)
//	POST /api/v1/alertas/{id}/ack     (Supervisor/Administrador)
package controller

import (
	"errors"
	"net/http"
	"strings"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/helpers"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// AlertaDeps agrupa las dependencias de los handlers de alertas.
type AlertaDeps struct {
	Repo alerta.Repository
}

// estadosValidos es el enum aceptado por el filtro ?estado=.
var estadosValidos = map[string]bool{
	alerta.EstadoActiva:   true,
	alerta.EstadoAck:      true,
	alerta.EstadoResuelta: true,
}

// HandleListar maneja GET /api/v1/alertas. El `data` del envelope ES el array
// de alertas (el frontend lo consume como array).
func HandleListar(deps AlertaDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}

		estado := r.URL.Query().Get("estado")
		if estado != "" && !estadosValidos[estado] {
			response.Error(w, http.StatusBadRequest, "Estado de alerta inválido", nil)
			return
		}

		alertas, err := deps.Repo.Listar(r.Context(), estado)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, "Error al obtener las alertas", nil)
			return
		}
		if alertas == nil {
			alertas = []alerta.Alerta{}
		}
		response.OK(w, "Alertas obtenidas", alertas)
	}
}

// HandleAck maneja POST /api/v1/alertas/{id}/ack: reconoce una alerta ACTIVA y
// devuelve la fila actualizada como `data`.
func HandleAck(deps AlertaDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
			return
		}
		if !helpers.RolValido(r, user.RoleSupervisor, user.RoleAdmin) {
			response.Error(w, http.StatusForbidden, "Rol insuficiente: se requiere Supervisor o Administrador", nil)
			return
		}

		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/alertas/"), "/ack")
		id = strings.Trim(id, "/")
		if id == "" || strings.Contains(id, "/") {
			response.Error(w, http.StatusNotFound, "Alerta no encontrada", nil)
			return
		}

		claims := authController.GetClaimsFromContext(r.Context())
		if claims == nil || claims.Email == "" {
			response.Error(w, http.StatusUnauthorized, "Sesión requerida", nil)
			return
		}

		a, err := deps.Repo.Ack(r.Context(), id, alerta.Usuario{Email: claims.Email, Nombre: claims.Email})
		if err != nil {
			switch {
			case errors.Is(err, alerta.ErrNotFound):
				response.Error(w, http.StatusNotFound, "Alerta no encontrada", nil)
			case errors.Is(err, alerta.ErrYaReconocida):
				response.Error(w, http.StatusConflict, "La alerta ya fue reconocida", nil)
			default:
				response.Error(w, http.StatusInternalServerError, "Error al reconocer la alerta", nil)
			}
			return
		}
		response.OK(w, "Alerta reconocida", a)
	}
}
