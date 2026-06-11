package controller

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote"
	"github.com/angelobenedetti29/smart-check-automation/internal/repository"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// LoteHandler handles HTTP requests for productive batch operations.
type LoteHandler struct {
	repo repository.LoteRepository
}

// NewLoteHandler creates a new LoteHandler with the given repository.
func NewLoteHandler(repo repository.LoteRepository) *LoteHandler {
	return &LoteHandler{repo: repo}
}

// HandleCreateLote processes POST /api/v1/lotes.
// It validates the API key, decodes the JSON body, applies business rules,
// and persists the batch via the repository.
func (h *LoteHandler) HandleCreateLote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	// 1. Validate X-API-Key header against environment secret
	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" || apiKey != os.Getenv("API_KEY_SECRET") {
		response.Error(w, http.StatusUnauthorized, "API key inválida o ausente", nil)
		return
	}

	// 2. Decode JSON body into LoteRequest
	var req lote.LoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "Formato JSON inválido", nil)
		return
	}

	// 3. Validate business rule: correctos + quemados must equal total_unidades
	if req.Correctos+req.Quemados != req.TotalUnidades {
		log.Printf("[AUDIT] Validación de lote fallida: correctos(%d) + quemados(%d) = %d ≠ total_unidades(%d)",
			req.Correctos, req.Quemados, req.Correctos+req.Quemados, req.TotalUnidades)
		response.Error(w, http.StatusBadRequest,
			"La suma de panes correctos y quemados debe ser igual al total de unidades", nil)
		return
	}

	// 4. Map request to domain entity
	domainLote := lote.MapLoteRequestToLote(req)

	// 5. Persist via repository
	if err := h.repo.Create(r.Context(), &domainLote); err != nil {
		log.Printf("[ERROR] Error al insertar lote en la base de datos: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al guardar el lote en la base de datos", nil)
		return
	}

	// 6. Respond 201 Created
	response.JSON(w, http.StatusCreated, true, "Lote creado exitosamente", domainLote, nil)
}
