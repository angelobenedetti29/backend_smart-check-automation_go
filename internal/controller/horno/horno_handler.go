package controller

import (
	"encoding/json"
	"net/http"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/horno"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// HornoHandler handles incoming HTTP presentation layers for Kilns/Ovens.
type HornoHandler struct {
	service    horno.Service
	alertaRepo alerta.Repository
}

// NewHornoHandler initializes a new HornoHandler.
func NewHornoHandler(svc horno.Service, aRepo alerta.Repository) *HornoHandler {
	return &HornoHandler{
		service:    svc,
		alertaRepo: aRepo,
	}
}

// GetHornoStatus handles GET /api/v1/horno?id=horno-01
func (h *HornoHandler) GetHornoStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		response.Error(w, http.StatusBadRequest, "El parámetro query 'id' es requerido", nil)
		return
	}

	// 1. Trigger service logic (which triggers YOLO checks under the hood)
	hEntity, err := h.service.CheckStatus(id)
	if err != nil {
		response.Error(w, http.StatusNotFound, "Horno no encontrado", err.Error())
		return
	}

	// 2. Fetch recent alerts for reporting details
	alerts, _ := h.alertaRepo.GetByHornoID(id)

	// 3. Render integrated presentation DTO
	data := map[string]interface{}{
		"horno":             hEntity,
		"alertas_recientes": alerts,
		"conveyor_checked":  true,
		"saludo":            "Hola Mundo desde el controlador de Horno en Arquitectura de Capas Go!",
	}

	response.OK(w, "Estado de Horno verificado exitosamente", data)
}

// UpdateTemperatureInput represents the JSON body schema for updating temperature.
type UpdateTemperatureInput struct {
	ID          string  `json:"id"`
	Temperatura float64 `json:"temperatura"`
}

// UpdateTemperature handles POST /api/v1/horno/temperatura
func (h *HornoHandler) UpdateTemperature(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	var input UpdateTemperatureInput
	err := json.NewDecoder(r.Body).Decode(&input)
	if err != nil || input.ID == "" {
		response.Error(w, http.StatusBadRequest, "Formato JSON inválido o 'id' ausente", nil)
		return
	}

	// 1. Trigger temperature update service check (applies thresholds & raises db alerts)
	hEntity, err := h.service.UpdateTemperature(input.ID, input.Temperatura)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "Error al actualizar temperatura", err.Error())
		return
	}

	// 2. Output result and alert summary
	alerts, _ := h.alertaRepo.GetByHornoID(input.ID)
	
	var latestAlert *alerta.Alerta
	if len(alerts) > 0 {
		latestAlert = &alerts[len(alerts)-1]
	}

	data := map[string]interface{}{
		"horno":           hEntity,
		"ultima_alerta":   latestAlert,
		"alertas_totales": len(alerts),
	}

	response.OK(w, "Temperatura del horno actualizada transaccionalmente", data)
}
