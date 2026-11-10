// Package reportes expone la lógica de negocio de los reportes: parseo y
// validación del período/filtros y orquestación del repositorio para resumen,
// lotes, trazabilidad y export de alertas.
package reportes

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	domain "github.com/angelobenedetti29/smart-check-automation/internal/domain/reportes"
	lotesectorSvc "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
)

const (
	layoutFecha      = "2006-01-02"
	diasDefault      = 30
	diasMaximosRango = 366
	// limiteDefault y limiteMaximo de GET /api/v1/reportes/lotes.
	limiteDefault = 100
	limiteMaximo  = 1000
	// Default y tope del export de alertas, que no comparte el tope operativo.
	limiteAlertasDefault = 1000
	limiteAlertasMaximo  = 5000
)

var uuidRegexp = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// zonaART resuelve la zona de negocio; si el sistema no trae tzdata cae al
// offset fijo de Argentina (sin DST desde 2009).
var zonaART = func() *time.Location {
	if loc, err := time.LoadLocation(domain.Timezone); err == nil {
		return loc
	}
	return time.FixedZone("ART", -3*60*60)
}()

// Query son los parámetros crudos de la request (strings vacíos = ausentes).
type Query struct {
	Desde      string
	Hasta      string
	SectorID   string
	ProductoID string
	Turno      string
	// Estado sólo aplica al export de alertas (ACTIVA|ACK|RESUELTA).
	Estado string
	// Limite y AntesDe sólo aplican al detalle paginado de lotes.
	Limite  string
	AntesDe string
	// LoteID sólo aplica a la trazabilidad de un lote.
	LoteID string
}

// Service orquesta el resumen.
type Service struct {
	repo domain.Repository
	now  func() time.Time
}

// NewService instancia el servicio con el repositorio real.
func NewService(repo domain.Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// Resumen valida el período y los filtros, delega en el repositorio y completa
// la metadata (período/filtros) que devuelve el endpoint.
func (s *Service) Resumen(ctx context.Context, q Query) (*domain.Resumen, error) {
	periodo, err := s.parsePeriodo(q.Desde, q.Hasta)
	if err != nil {
		return nil, err
	}

	filtros, err := parseFiltros(q)
	if err != nil {
		return nil, err
	}

	resumen, err := s.repo.Resumen(ctx, periodo, filtros)
	if err != nil {
		return nil, err
	}
	resumen.Periodo = domain.PeriodoResumen{
		Desde:    periodo.Desde.Format(layoutFecha),
		Hasta:    periodo.Hasta.Format(layoutFecha),
		Timezone: domain.Timezone,
	}
	resumen.Filtros = domain.FiltrosResumen{
		SectorID:   filtros.SectorID,
		ProductoID: filtros.ProductoID,
		Turno:      filtros.Turno,
	}
	return resumen, nil
}

func (s *Service) parsePeriodo(desdeRaw, hastaRaw string) (domain.Periodo, error) {
	hoy := s.now().In(zonaART)
	hasta := time.Date(hoy.Year(), hoy.Month(), hoy.Day(), 0, 0, 0, 0, zonaART)

	if hastaRaw != "" {
		parsed, err := time.ParseInLocation(layoutFecha, hastaRaw, zonaART)
		if err != nil {
			return domain.Periodo{}, domain.ErrFechaInvalida
		}
		hasta = parsed
	}

	desde := hasta.AddDate(0, 0, -(diasDefault - 1))
	if desdeRaw != "" {
		parsed, err := time.ParseInLocation(layoutFecha, desdeRaw, zonaART)
		if err != nil {
			return domain.Periodo{}, domain.ErrFechaInvalida
		}
		desde = parsed
	}

	if desde.After(hasta) {
		return domain.Periodo{}, domain.ErrDesdeMayorHasta
	}
	if int(hasta.Sub(desde).Hours()/24)+1 > diasMaximosRango {
		return domain.Periodo{}, domain.ErrRangoExcedido
	}

	return domain.Periodo{Desde: desde, Hasta: hasta}, nil
}

func parseFiltros(q Query) (domain.Filtros, error) {
	filtros := domain.Filtros{}
	if q.SectorID != "" {
		sector := q.SectorID
		filtros.SectorID = &sector
	}
	if q.ProductoID != "" {
		if !uuidRegexp.MatchString(q.ProductoID) {
			return domain.Filtros{}, domain.ErrProductoIDInvalido
		}
		producto := q.ProductoID
		filtros.ProductoID = &producto
	}
	if q.Turno != "" {
		if !domain.TurnosValidos[q.Turno] {
			return domain.Filtros{}, domain.ErrTurnoInvalido
		}
		turno := q.Turno
		filtros.Turno = &turno
	}
	return filtros, nil
}

// Lotes valida el período, los filtros, el límite y el cursor, y devuelve una
// página del detalle de lotes junto al total sin cursor. Reutiliza el cursor
// opaco de lote_sector (base64url de "<inicio_at>|<id>").
func (s *Service) Lotes(ctx context.Context, q Query) (*domain.PaginaLotes, error) {
	periodo, err := s.parsePeriodo(q.Desde, q.Hasta)
	if err != nil {
		return nil, err
	}

	filtros, err := parseFiltros(q)
	if err != nil {
		return nil, err
	}

	limite, err := parseLimiteRango(q.Limite, limiteDefault, limiteMaximo)
	if err != nil {
		return nil, err
	}

	params := domain.LotesParams{
		Periodo: periodo,
		Filtros: filtros,
		Limite:  limite,
	}
	if q.AntesDe != "" {
		antesDe, antesDeID, err := lotesectorSvc.DecodeCursor(q.AntesDe)
		if err != nil {
			return nil, domain.ErrCursorInvalido
		}
		params.AntesDe = &antesDe
		params.AntesDeID = antesDeID
	}

	lotes, total, err := s.repo.Lotes(ctx, params)
	if err != nil {
		return nil, err
	}

	pagina := &domain.PaginaLotes{Lotes: lotes, Total: total, Limite: limite}
	// Mismo criterio que HandleHistorial: sólo hay página siguiente si se
	// llenó el límite; el cursor apunta al último lote devuelto.
	if len(lotes) == limite {
		ultimo := lotes[len(lotes)-1]
		pagina.SiguienteCursor = lotesectorSvc.EncodeCursor(ultimo.AbiertoEn, ultimo.ID)
	}
	return pagina, nil
}

// Trazabilidad valida el lote_id y delega el snapshot completo del lote al
// repositorio. El timestamp de generación lo fija el reloj del servicio.
func (s *Service) Trazabilidad(ctx context.Context, q Query) (*domain.Trazabilidad, error) {
	loteID := strings.TrimSpace(q.LoteID)
	if !uuidRegexp.MatchString(loteID) {
		return nil, domain.ErrLoteIDInvalido
	}

	trazabilidad, err := s.repo.Trazabilidad(ctx, loteID)
	if err != nil {
		return nil, err
	}
	trazabilidad.GeneradoEn = s.now()
	return trazabilidad, nil
}

// Alertas valida período, filtros, estado y límite, y devuelve el export de
// alertas del período con el total sin límite para marcar la truncación.
func (s *Service) Alertas(ctx context.Context, q Query) (*domain.PaginaAlertas, error) {
	periodo, err := s.parsePeriodo(q.Desde, q.Hasta)
	if err != nil {
		return nil, err
	}

	filtros, err := parseFiltros(q)
	if err != nil {
		return nil, err
	}

	var estado *string
	if q.Estado != "" {
		if !domain.EstadosAlertaValidos[q.Estado] {
			return nil, domain.ErrEstadoInvalido
		}
		e := q.Estado
		estado = &e
	}

	limite, err := parseLimiteRango(q.Limite, limiteAlertasDefault, limiteAlertasMaximo)
	if err != nil {
		return nil, err
	}

	alertas, total, err := s.repo.Alertas(ctx, domain.AlertasParams{
		Periodo: periodo,
		Filtros: filtros,
		Estado:  estado,
		Limite:  limite,
	})
	if err != nil {
		return nil, err
	}
	return &domain.PaginaAlertas{
		Total:    total,
		Truncada: total > len(alertas),
		Alertas:  alertas,
	}, nil
}

// parseLimiteRango normaliza un límite: ausente o no positivo usa el default y
// el excedente se recorta al máximo. Un valor no numérico es un 400.
func parseLimiteRango(raw string, def, maximo int) (int, error) {
	if raw == "" {
		return def, nil
	}
	limite, err := strconv.Atoi(raw)
	if err != nil {
		return 0, domain.ErrLimiteInvalido
	}
	if limite <= 0 {
		return def, nil
	}
	if limite > maximo {
		return maximo, nil
	}
	return limite, nil
}
