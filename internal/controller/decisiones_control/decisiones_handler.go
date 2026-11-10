// Package controller expone GET /api/v1/lotes/{id}/decisiones (JWT cookie),
// que devuelve el audit del MPC para un lote (las decisiones más recientes
// primero). El Loop de control guarda una fila por decisión aplicada o
// skipped; acá se leen para mostrarlas en el panel.
package controller

import (
	"context"
	"errors"
	"net/http"

	"github.com/angelobenedetti29/smart-check-automation/internal/controller/helpers"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// DecisionesRepo es el mínimo que necesita el handler para listar.
type DecisionesRepo interface {
	ListByLote(ctx context.Context, loteID string, limite int) ([]decisiones_control.DecisionControl, error)
}

// DecisionesHandler lista las decisiones del MPC para un lote.
type DecisionesHandler struct {
	repo DecisionesRepo
}

// NewDecisionesHandler instancia el handler.
func NewDecisionesHandler(repo DecisionesRepo) *DecisionesHandler {
	return &DecisionesHandler{repo: repo}
}

// Handle expone GET /api/v1/lotes/{id}/decisiones.
func (h *DecisionesHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	if !helpers.RolValido(r, user.RoleAdmin, user.RoleSupervisor, user.RoleOperario) {
		response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
		return
	}
	loteID, err := helpers.ExtractLoteID(r.URL.Path, "/decisiones")
	if err != nil {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
		return
	}
	items, err := h.repo.ListByLote(r.Context(), loteID, 100)
	if err != nil {
		if errors.Is(err, lotesector.ErrNotFound) {
			response.Error(w, http.StatusNotFound, "Lote no encontrado", nil)
			return
		}
		response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
		return
	}
	response.OK(w, "Decisiones del lote", map[string]interface{}{
		"lote_id":    loteID,
		"decisiones": items,
		"total":      len(items),
	})
}
