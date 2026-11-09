package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/controller/requestjson"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
	loteSectorService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// LoteLookup es el mínimo que el handler necesita para validar que el lote
// pertenece al sector del dispositivo y sigue abierto.
type LoteLookup interface {
	SectorDelDispositivo(ctx context.Context, deviceID string) (*loteSectorService.SectorInfo, error)
	GetByID(ctx context.Context, loteID string) (*lotesector.Lote, error)
}

// AlertaCalidadHandler maneja POST /api/v1/lotes/{id}/alertas-calidad: la Pi
// reporta que el % de crudos/quemados superó su umbral y el backend crea la
// alerta real (que encola email y Telegram con los datos recibidos).
type AlertaCalidadHandler struct {
	repo    alerta.Repository
	lookup  LoteLookup
	maxBody int64
}

// NewAlertaCalidadHandler instancia el handler.
func NewAlertaCalidadHandler(repo alerta.Repository, lookup LoteLookup) *AlertaCalidadHandler {
	return &AlertaCalidadHandler{repo: repo, lookup: lookup, maxBody: 1 << 16}
}

// alertaCalidadRequest es el body del reporte de la Pi.
type alertaCalidadRequest struct {
	Tipo            string  `json:"tipo"`
	Porcentaje      float64 `json:"porcentaje"`
	Umbral          float64 `json:"umbral"`
	VentanaSegundos int     `json:"ventana_segundos"`
}

// etiquetaCalidad devuelve el texto del mensaje para un tipo de alerta de calidad.
func etiquetaCalidad(tipo string) (string, bool) {
	switch tipo {
	case alerta.TipoPorcentajeQuemadosAlto:
		return "quemados", true
	case alerta.TipoPorcentajeCrudosAlto:
		return "crudos", true
	}
	return "", false
}

// Handle es el entrypoint HTTP (Bearer de dispositivo, ya validado por BeforeMux).
func (h *AlertaCalidadHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	principal, ok := deviceauth.PrincipalFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Token de dispositivo inválido", map[string]string{"code": "invalid_device_token"})
		return
	}
	loteID, ok := loteIDDePath(r.URL.Path)
	if !ok {
		response.Error(w, http.StatusNotFound, "Lote no encontrado", map[string]string{"code": "lote_no_encontrado"})
		return
	}

	var req alertaCalidadRequest
	if !requestjson.Decode(w, r, h.maxBody, &req) {
		return
	}
	req.Tipo = strings.ToUpper(strings.TrimSpace(req.Tipo))
	etiqueta, valido := etiquetaCalidad(req.Tipo)
	if !valido {
		response.Error(w, http.StatusBadRequest, "tipo inválido: use PORCENTAJE_QUEMADOS_ALTO o PORCENTAJE_CRUDOS_ALTO", map[string]string{"code": "tipo_invalido"})
		return
	}
	if math.IsNaN(req.Porcentaje) || math.IsInf(req.Porcentaje, 0) || req.Porcentaje < 0 || req.Porcentaje > 100 ||
		math.IsNaN(req.Umbral) || math.IsInf(req.Umbral, 0) || req.Umbral < 0 || req.Umbral > 100 ||
		req.VentanaSegundos < 0 {
		response.Error(w, http.StatusBadRequest, "porcentaje y umbral deben estar entre 0 y 100, y la ventana no puede ser negativa", map[string]string{"code": "payload_invalido"})
		return
	}

	lote, err := h.lookup.GetByID(r.Context(), loteID)
	if err != nil {
		escribirErrorCalidad(w, err)
		return
	}
	info, err := h.lookup.SectorDelDispositivo(r.Context(), principal.DeviceID)
	if err != nil {
		escribirErrorCalidad(w, err)
		return
	}
	if lote.SectorID != info.SectorID {
		escribirErrorCalidad(w, lotesector.ErrAjeno)
		return
	}
	if lote.Estado != lotesector.EstadoAbierto {
		response.Error(w, http.StatusConflict, "El lote no está abierto", map[string]string{"code": "lote_no_abierto"})
		return
	}

	sectorID, loteRef := lote.SectorID, lote.ID
	mensaje := fmt.Sprintf("Porcentaje de %s %.1f%% supera el umbral de aceptación %.1f%%", etiqueta, req.Porcentaje, req.Umbral)
	if req.VentanaSegundos > 0 {
		mensaje += fmt.Sprintf(" en la ventana de %ds", req.VentanaSegundos)
	}
	a := &alerta.Alerta{
		SectorID:  &sectorID,
		LoteID:    &loteRef,
		Tipo:      req.Tipo,
		Severidad: alerta.SeveridadWarning,
		Estado:    alerta.EstadoActiva,
		Mensaje:   mensaje,
	}
	if err := h.repo.Save(a); err != nil {
		response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
		return
	}
	// Save devuelve nil y deja ID vacío cuando ya existe una alerta ACTIVA
	// equivalente (índice parcial por lote y tipo): no se vuelve a notificar.
	if a.ID == "" {
		response.OK(w, "Alerta ya activa para este lote", map[string]any{"creada": false})
		return
	}
	response.JSON(w, http.StatusCreated, true, "Alerta registrada", map[string]any{"creada": true, "id": a.ID}, nil)
}

// loteIDDePath extrae {id} de /api/v1/lotes/{id}/alertas-calidad.
func loteIDDePath(path string) (string, bool) {
	const prefix, suffix = "/api/v1/lotes/", "/alertas-calidad"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	mid := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix), "/")
	if mid == "" || strings.Contains(mid, "/") {
		return "", false
	}
	return mid, true
}

func escribirErrorCalidad(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lotesector.ErrNotFound):
		response.Error(w, http.StatusNotFound, "Lote no encontrado", map[string]string{"code": "lote_no_encontrado"})
	case errors.Is(err, lotesector.ErrAjeno):
		response.Error(w, http.StatusForbidden, "El lote pertenece a otro sector", map[string]string{"code": "lote_ajeno"})
	case errors.Is(err, sector.ErrSinSector):
		response.Error(w, http.StatusNotFound, "El dispositivo no pertenece a un sector", map[string]string{"code": "sin_sector"})
	default:
		response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
	}
}
