package controller

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// maxRequestBodyBytes limita el tamaño del body a 1 MB para evitar DoS por memoria.
const maxRequestBodyBytes = 1 << 20 // 1 MB

// LoteHandler handles HTTP requests for productive batch operations.
type LoteHandler struct {
	repo lote.Repository
}

// NewLoteHandler creates a new LoteHandler with the given repository.
func NewLoteHandler(repo lote.Repository) *LoteHandler {
	return &LoteHandler{repo: repo}
}

// HandleCreateLote processes POST /api/v1/lotes.
// It validates the API key, decodes the JSON body, applies domain business rules,
// and persists the batch via the repository.
func (h *LoteHandler) HandleCreateLote(w http.ResponseWriter, r *http.Request) {
	// 0. Method check
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	// 1. Validate Content-Type
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, "Content-Type debe ser application/json", nil)
		return
	}

	// 2. Validate X-API-Key header using constant-time comparison (timing-attack safe)
	apiKey := r.Header.Get("X-API-Key")
	secret := os.Getenv("API_KEY_SECRET")
	if apiKey == "" || subtle.ConstantTimeCompare([]byte(apiKey), []byte(secret)) != 1 {
		response.Error(w, http.StatusUnauthorized, "API key inválida o ausente", nil)
		return
	}

	// 3. Limit body size to prevent memory-exhaustion DoS
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	// 4. Decode JSON body into LoteRequest
	var req lote.LoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "Formato JSON inválido o body demasiado grande", nil)
		return
	}

	// 5. Apply domain validation rules (all fields, business constraints)
	if err := req.Validate(); err != nil {
		log.Printf("[AUDIT] Validación de lote fallida: %v", err)
		response.Error(w, http.StatusUnprocessableEntity, "Datos del lote inválidos", err.Error())
		return
	}

	// 6. Map request to domain entity
	domainLote := lote.MapLoteRequestToLote(req)

	// 7. Persist via repository
	if err := h.repo.Create(r.Context(), &domainLote); err != nil {
		log.Printf("[ERROR] Error al insertar lote en la base de datos: %v", err)
		response.Error(w, http.StatusInternalServerError, "Error al guardar el lote en la base de datos", nil)
		return
	}

	// 8. Respond 201 Created
	response.JSON(w, http.StatusCreated, true, "Lote creado exitosamente", domainLote, nil)
}
