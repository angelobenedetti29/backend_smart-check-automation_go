// Package controller expone GET /api/v1/lotes/{id}/decisiones (JWT cookie),
// que devuelve el audit del MPC para un lote (las decisiones más recientes
// primero). El Loop de control guarda una fila por decisión aplicada o
// skipped; acá se leen para mostrarlas en el panel.
package controller

import (
	"context"
	"errors"
	"net/http"
	"strings"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
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
	if !rolValidoDecisiones(r) {
		response.Error(w, http.StatusForbidden, "Rol insuficiente", nil)
		return
	}
	loteID, err := extractLoteID(r.URL.Path, "/decisiones")
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
		"lote_id":   loteID,
		"decisiones": items,
		"total":     len(items),
	})
}

// nolint:unusedReceiverWordUsage
func rolValidoDecisiones(r *http.Request) bool {
	claims := authController.GetClaimsFromContext(r.Context())
	if claims == nil {
		return false
	}
	switch claims.Role {
	case user.RoleAdmin, user.RoleSupervisor, user.RoleOperario:
		return true
	}
	return false
}

// extractLoteID obtiene el {id} de /api/v1/lotes/{id}/<suffix>.
func extractLoteID(path, suffix string) (string, error) {
	const prefix = "/api/v1/lotes/"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", lotesector.ErrNotFound
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	mid = strings.Trim(mid, "/")
	if mid == "" || strings.Contains(mid, "/") {
		return "", lotesector.ErrNotFound
	}
	return mid, nil
}

// asegurar que usamos strings aunque no aparezca en el cuerpo
var _ = strings.HasPrefix