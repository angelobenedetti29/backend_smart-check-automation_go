package controller

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

// SectorLookup resuelve el sector dueño del horno.
type SectorLookup interface {
	GetByID(ctx context.Context, sectorID string) (*sector.Sector, error)
}

// LoteAbiertoLookup resuelve el lote ABIERTO del sector (nil si no hay).
type LoteAbiertoLookup interface {
	GetAbiertoBySector(ctx context.Context, sectorID string) (*lote_sector.Lote, error)
}

// MetricasLookup lee la telemetría real del horno por lote.
type MetricasLookup interface {
	GetUltimasParaLote(ctx context.Context, loteID string, limite int) ([]metricashorno.MetricaHorno, error)
}

// Estados del horno expuestos por GET /api/v1/horno.
const (
	EstadoSinLote         = "SIN_LOTE"
	EstadoSinTelemetria   = "SIN_TELEMETRIA"
	EstadoSinDatos        = "SIN_DATOS"
	EstadoActivo          = "ACTIVO"
	EstadoDesactualizado  = "DESACTUALIZADO"
	FuenteSinLote         = "sin_lote"
	FuenteSinTelemetria   = "sin_telemetria"
	FuenteTelemetria      = "telemetria"
	frescuraTelemetriaSeg = 30
)

// HornoStatusDTO es la proyección de solo lectura del horno: telemetría real
// (metricas_horno) + lote abierto. Los campos sin dato viajan null.
type HornoStatusDTO struct {
	ID                 string     `json:"id"`
	Nombre             string     `json:"nombre"`
	SectorID           string     `json:"sector_id"`
	LoteID             *string    `json:"lote_id"`
	ProductoID         *string    `json:"producto_id"`
	Temperatura        *float64   `json:"temperatura"`
	VelocidadCinta     *float64   `json:"velocidad_cinta"`
	TempComb1          *float64   `json:"temp_comb_1"`
	HumedadHorno       *float64   `json:"humedad_horno"`
	TempAmbiente       *float64   `json:"temp_ambiente"`
	HumedadAmbiente    *float64   `json:"humedad_ambiente"`
	TempObjetivo       *float64   `json:"temp_objetivo"`
	VelObjetivo        *float64   `json:"vel_objetivo"`
	ConfianzaDeteccion *float64   `json:"confianza_deteccion"`
	Estado             string     `json:"estado"`
	Fuente             string     `json:"fuente"`
	UltimoCheck        *time.Time `json:"ultimo_check"`
	AntiguedadSegundos *int       `json:"antiguedad_segundos"`
}

// HornoHandler handles incoming HTTP presentation layers for Kilns/Ovens.
type HornoHandler struct {
	alertaRepo alerta.Repository
	sectores   SectorLookup
	lotes      LoteAbiertoLookup
	metricas   MetricasLookup
}

// NewHornoHandler initializes a new HornoHandler.
func NewHornoHandler(aRepo alerta.Repository, sectores SectorLookup, lotes LoteAbiertoLookup, metricas MetricasLookup) *HornoHandler {
	return &HornoHandler{
		alertaRepo: aRepo,
		sectores:   sectores,
		lotes:      lotes,
		metricas:   metricas,
	}
}

// GetHornoStatus handles GET /api/v1/horno?sector_id=horno-1 (alias legacy: ?id=).
// Proyecta la telemetría real (metricas_horno) del lote abierto. No hay horno
// en memoria: el backend no simula estado ni aplica consignas.
func (h *HornoHandler) GetHornoStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		response.Error(w, http.StatusMethodNotAllowed, "Método no permitido", nil)
		return
	}

	sectorID := r.URL.Query().Get("sector_id")
	if sectorID == "" {
		sectorID = r.URL.Query().Get("id") // alias legacy
	}
	if sectorID == "" {
		response.Error(w, http.StatusBadRequest, "El parámetro query 'sector_id' es requerido", nil)
		return
	}

	sectorEntity, err := h.sectores.GetByID(r.Context(), sectorID)
	if err != nil {
		if errors.Is(err, sector.ErrSectorNotFound) {
			response.Error(w, http.StatusNotFound, "Sector no encontrado", nil)
			return
		}
		response.Error(w, http.StatusInternalServerError, "Error al consultar el sector", err.Error())
		return
	}

	hornoDTO, err := h.proyectarHorno(r, sectorEntity)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "Error al consultar la telemetría del horno", err.Error())
		return
	}

	alerts, _ := h.alertaRepo.GetByHornoID(sectorID)

	data := map[string]interface{}{
		"horno":             hornoDTO,
		"alertas_recientes": alerts,
	}

	response.OK(w, "Estado de Horno verificado exitosamente", data)
}

// proyectarHorno arma el DTO a partir del lote abierto y su última muestra.
func (h *HornoHandler) proyectarHorno(r *http.Request, s *sector.Sector) (*HornoStatusDTO, error) {
	dto := &HornoStatusDTO{
		ID:       s.ID,
		Nombre:   s.Nombre,
		SectorID: s.ID,
	}

	lote, err := h.lotes.GetAbiertoBySector(r.Context(), s.ID)
	if err != nil {
		return nil, err
	}
	if lote == nil {
		dto.Estado = EstadoSinLote
		dto.Fuente = FuenteSinLote
		return dto, nil
	}

	loteID := lote.ID
	dto.LoteID = &loteID
	if lote.ProductoID != "" {
		productoID := lote.ProductoID
		dto.ProductoID = &productoID
	}

	muestras, err := h.metricas.GetUltimasParaLote(r.Context(), lote.ID, 1)
	if err != nil {
		return nil, err
	}
	if len(muestras) == 0 {
		dto.Estado = EstadoSinTelemetria
		dto.Fuente = FuenteSinTelemetria
		return dto, nil
	}

	m := muestras[0]
	dto.Fuente = FuenteTelemetria
	dto.Temperatura = m.TempHorno1
	dto.VelocidadCinta = m.VelocidadCinta
	dto.TempComb1 = m.TempComb1
	dto.HumedadHorno = m.HumedadHorno
	dto.TempAmbiente = m.TempAmbiente
	dto.HumedadAmbiente = m.HumedadAmbiente
	dto.TempObjetivo = m.TempObjetivo
	dto.VelObjetivo = m.VelObjetivo
	dto.ConfianzaDeteccion = m.ConfianzaDeteccion

	ultimoCheck := m.ReceivedAt
	antiguedad := int(time.Since(m.ReceivedAt).Seconds())
	if antiguedad < 0 {
		antiguedad = 0
	}
	dto.UltimoCheck = &ultimoCheck
	dto.AntiguedadSegundos = &antiguedad

	switch {
	case m.TempHorno1 == nil && m.VelocidadCinta == nil:
		dto.Estado = EstadoSinDatos
	case antiguedad > frescuraTelemetriaSeg: // la Pi publica cada 5 s
		dto.Estado = EstadoDesactualizado
	default:
		dto.Estado = EstadoActivo
	}

	return dto, nil
}
