// Package controller expone el endpoint HTTP que persiste los heartbeats de
// telemetría del horno reportados por la Pi ENTRADA.
//
// POST /api/v1/lotes/{id}/metricas-horno (Bearer dispositivo)
//
// La Pi envía un MetricaHornoRequest con las lecturas actuales. El servidor
// completa lote_id (de la URL), dispositivo_id (del principal autenticado) y
// sector_id (del dispositivo), valida que el lote pertenezca al sector del
// dispositivo y que el lote esté ABIERTO, y persiste append-only en
// metricas_horno. La respuesta es 201 con el id asignado y el received_at
// del servidor (que es la fuente de verdad para ordenar heartbeats).
package controller

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/controller/requestjson"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
	loteSectorService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// LoteLookupService es el mínimo que el handler necesita para validar el
// heartbeat: el sector del dispositivo autenticado y el lote por id. La
// implementación real la satisface *lote_sector.Service, que ya expone
// SectorDelDispositivo y GetByID en su paquete.
type LoteLookupService interface {
	SectorDelDispositivo(ctx context.Context, deviceID string) (*loteSectorService.SectorInfo, error)
	GetByID(ctx context.Context, loteID string) (*lotesector.Lote, error)
}

// MetricasHornoService es el contrato que consume el handler para persistir.
type MetricasHornoService interface {
	Save(ctx context.Context, m *metricashorno.MetricaHorno) error
}

// MetricasHornoHandler maneja POST /api/v1/lotes/{id}/metricas-horno.
type MetricasHornoHandler struct {
	repo    MetricasHornoService
	lookup  LoteLookupService
	maxBody int64
}

// NewMetricasHornoHandler instancia el handler inyectando el repo de
// métricas y el lookup de lotes (que valida pertenencia y estado).
func NewMetricasHornoHandler(repo MetricasHornoService, lookup LoteLookupService) *MetricasHornoHandler {
	return &MetricasHornoHandler{
		repo:    repo,
		lookup:  lookup,
		maxBody: 1 << 20, // 1 MB; un heartbeat son pocos cientos de bytes.
	}
}

// Handle es el entrypoint HTTP. Sólo POST; cualquier otro método es 405.
func (h *MetricasHornoHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}
	principal, ok := deviceauth.PrincipalFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "Token de dispositivo inválido", map[string]string{"code": "invalid_device_token"})
		return
	}

	loteID, err := extractLoteIDFromPath(r.URL.Path, "/metricas-horno")
	if err != nil {
		writeMetricasError(w, err)
		return
	}

	var req metricashorno.MetricaHornoRequest
	if !requestjson.Decode(w, r, h.maxBody, &req) {
		return
	}
	if err := req.Validate(); err != nil {
		response.Error(w, http.StatusBadRequest, err.Error(), map[string]string{"code": "payload_invalido"})
		return
	}

	// Validar que el lote existe, está ABIERTO y pertenece al sector del
	// dispositivo autenticado.
	lote, err := h.lookup.GetByID(r.Context(), loteID)
	if err != nil {
		writeMetricasError(w, err)
		return
	}
	info, err := h.lookup.SectorDelDispositivo(r.Context(), principal.DeviceID)
	if err != nil {
		writeMetricasError(w, err)
		return
	}
	if lote.SectorID != info.SectorID {
		writeMetricasError(w, lotesector.ErrAjeno)
		return
	}
	if lote.Estado != lotesector.EstadoAbierto {
		writeMetricasError(w, metricashorno.ErrLoteNoAbierto)
		return
	}

	m := &metricashorno.MetricaHorno{
		LoteID:             loteID,
		DispositivoID:      principal.DeviceID,
		SectorID:           info.SectorID,
		TempHorno1:         req.TempHorno1,
		TempHorno2:         req.TempHorno2,
		TempComb1:          req.TempComb1,
		TempComb2:          req.TempComb2,
		HumedadHorno:       req.HumedadHorno,
		TempAmbiente:       req.TempAmbiente,
		HumedadAmbiente:    req.HumedadAmbiente,
		VelocidadCinta:     req.VelocidadCinta,
		TempObjetivo:       req.TempObjetivo,
		VelObjetivo:        req.VelObjetivo,
		ConfianzaDeteccion: req.ConfianzaDeteccion,
	}
	if req.ReceivedAt != nil {
		m.ReceivedAt = *req.ReceivedAt
	}
	if err := h.repo.Save(r.Context(), m); err != nil {
		writeMetricasError(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, true, "Métrica registrada", map[string]interface{}{
		"id":          m.ID,
		"received_at": m.ReceivedAt,
	}, nil)
}

// extractLoteIDFromPath obtiene el {id} de /api/v1/lotes/{id}/metricas-horno.
// Devuelve lotesector.ErrNotFound si el path no parsea.
func extractLoteIDFromPath(path, suffix string) (string, error) {
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

// writeMetricasError mapea errores de dominio a HTTP. Mantenido local para
// no contaminar el paquete con errores de otros controllers.
func writeMetricasError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lotesector.ErrNotFound):
		response.Error(w, http.StatusNotFound, "Lote no encontrado", map[string]string{"code": "lote_no_encontrado"})
	case errors.Is(err, lotesector.ErrAjeno):
		response.Error(w, http.StatusForbidden, "El lote pertenece a otro sector", map[string]string{"code": "lote_ajeno"})
	case errors.Is(err, metricashorno.ErrLoteNoAbierto):
		response.Error(w, http.StatusConflict, "El lote no está abierto", map[string]string{"code": "lote_no_abierto"})
	case errors.Is(err, sector.ErrSinSector):
		response.Error(w, http.StatusNotFound, "El dispositivo no pertenece a un sector", map[string]string{"code": "sin_sector"})
	default:
		response.Error(w, http.StatusInternalServerError, "Error interno del servidor", nil)
	}
}