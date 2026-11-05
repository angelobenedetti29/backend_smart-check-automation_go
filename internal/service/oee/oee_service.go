// Package oee expone la lógica de negocio del cálculo de OEE: parseo y
// validación del período, y cálculo puro de Availability/Performance/Quality,
// por sector y global.
package oee

import (
	"context"
	"math"
	"sort"
	"time"

	domain "github.com/angelobenedetti29/smart-check-automation/internal/domain/oee"
)

const (
	layoutFecha      = "2006-01-02"
	diasDefault      = 30
	diasMaximosRango = 366
)

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
	Desde    string
	Hasta    string
	SectorID string
}

// Service orquesta el cálculo.
type Service struct {
	repo domain.Repository
	now  func() time.Time
}

// NewService instancia el servicio con el repositorio real.
func NewService(repo domain.Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// OEE valida el período, delega la carga de datos y calcula el resumen global
// y por sector. No soporta filtros por producto ni turno.
func (s *Service) OEE(ctx context.Context, q Query) (*domain.Resumen, error) {
	periodo, err := s.parsePeriodo(q.Desde, q.Hasta)
	if err != nil {
		return nil, err
	}

	filtros := domain.Filtros{}
	if q.SectorID != "" {
		sector := q.SectorID
		filtros.SectorID = &sector
	}

	datos, err := s.repo.Datos(ctx, domain.Query{Periodo: periodo, Filtros: filtros})
	if err != nil {
		return nil, err
	}

	resumen := calcular(periodo, filtros, datos)
	resumen.Periodo = domain.PeriodoResumen{
		Desde:    periodo.Desde.Format(layoutFecha),
		Hasta:    periodo.Hasta.Format(layoutFecha),
		Timezone: domain.Timezone,
	}
	resumen.Filtros = domain.FiltrosResumen{SectorID: filtros.SectorID}
	return &resumen, nil
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

// calcular arma el bloque global y el de cada sector con datos en el período.
// Si se filtró un sector, se incluye aunque no tenga datos (calculable=false).
func calcular(periodo domain.Periodo, filtros domain.Filtros, datos *domain.Datos) domain.Resumen {
	hasta := periodo.Hasta.AddDate(0, 0, 1)

	resumen := domain.Resumen{PorSector: []domain.ResumenSector{}}
	resumen.Global = calcularSector("", "Todos", periodo.Desde, hasta, datos)

	ids := map[string]bool{}
	for _, r := range datos.Plan {
		ids[r.SectorID] = true
	}
	for _, r := range datos.Paradas {
		ids[r.SectorID] = true
	}
	for _, r := range datos.Lotes {
		ids[r.SectorID] = true
	}
	if filtros.SectorID != nil {
		ids[*filtros.SectorID] = true
	}

	orden := make([]string, 0, len(ids))
	for id := range ids {
		orden = append(orden, id)
	}
	sort.Strings(orden)
	for _, id := range orden {
		resumen.PorSector = append(resumen.PorSector,
			calcularSector(id, datos.Sectores[id], periodo.Desde, hasta, datos))
	}
	return resumen
}

// calcularSector acumula los datos del sector (vacío = todos, para el global).
func calcularSector(sectorID, nombre string, desde, hasta time.Time, datos *domain.Datos) domain.ResumenSector {
	s := domain.ResumenSector{
		SectorID:     sectorID,
		SectorNombre: nombre,
		Motivos:      []string{},
	}

	var segundosPlan, kgPlan float64
	horasPorProducto := map[string]float64{}
	for _, p := range filtrar(datos.Plan, sectorID, func(p domain.PlanOEE) string { return p.SectorID }) {
		seg := solapeSegundos(p.InicioAt, p.FinAt, desde, hasta)
		if seg <= 0 {
			continue
		}
		segundosPlan += seg
		if dur := p.FinAt.Sub(p.InicioAt).Seconds(); dur > 0 {
			kgPlan += p.KgPlan * seg / dur
		}
		horasPorProducto[p.ProductoID] += seg / 3600
	}

	var plannedDown, unplannedDown float64
	for _, p := range filtrar(datos.Paradas, sectorID, func(p domain.ParadaOEE) string { return p.SectorID }) {
		seg := solapeSegundos(p.InicioAt, p.FinAt, desde, hasta)
		switch {
		case seg <= 0:
		case p.Tipo == "PLANIFICADA":
			plannedDown += seg
		case p.Tipo == "NO_PLANIFICADA":
			unplannedDown += seg
		}
	}

	// La capacidad teórica se pondera por las horas planificadas de cada
	// producto. Un producto sin capacidad > 0 invalida performance.
	var capacidadTeorica float64
	faltantes := []string{}
	for producto, horas := range horasPorProducto {
		cap := datos.Capacidades[producto]
		if cap == nil || *cap <= 0 {
			faltantes = append(faltantes, producto)
			continue
		}
		capacidadTeorica += *cap * horas
	}
	sort.Strings(faltantes)

	var kgTotal, kgOK float64
	for _, l := range filtrar(datos.Lotes, sectorID, func(l domain.LoteOEE) string { return l.SectorID }) {
		kgTotal += l.KgTotal
		kgOK += l.KgOK
	}

	operating := math.Max(0, segundosPlan-unplannedDown)

	if segundosPlan > 0 {
		s.AvailabilityPct = pct(operating / segundosPlan * 100)
	} else {
		s.Motivos = append(s.Motivos, "sin_plan_produccion")
	}
	if capacidadTeorica > 0 && len(faltantes) == 0 {
		s.PerformancePct = pct(kgTotal / capacidadTeorica * 100)
	}
	for _, id := range faltantes {
		s.Motivos = append(s.Motivos, "sin_capacidad_producto:"+id)
	}
	if kgTotal > 0 {
		s.QualityPct = pct(kgOK / kgTotal * 100)
	} else {
		s.Motivos = append(s.Motivos, "sin_produccion")
	}
	if s.AvailabilityPct != nil && s.PerformancePct != nil && s.QualityPct != nil {
		valor := *s.AvailabilityPct * *s.PerformancePct * *s.QualityPct / 10000
		s.OEEPct = pct(valor)
	}
	s.Calculable = s.OEEPct != nil

	s.PlannedTimeMin = round1(segundosPlan / 60)
	s.PlannedDowntimeMin = round1(plannedDown / 60)
	s.UnplannedDowntimeMin = round1(unplannedDown / 60)
	s.OperatingTimeMin = round1(operating / 60)
	s.KgPlan = round3(kgPlan)
	s.KgProducidos = round3(kgTotal)
	s.KgOK = round3(kgOK)
	s.CapacidadTeoricaKg = round3(capacidadTeorica)

	return s
}

// solapeSegundos devuelve los segundos de [inicio, fin) dentro de [desde, hasta).
func solapeSegundos(inicio, fin, desde, hasta time.Time) float64 {
	if inicio.Before(desde) {
		inicio = desde
	}
	if fin.After(hasta) {
		fin = hasta
	}
	if !fin.After(inicio) {
		return 0
	}
	return fin.Sub(inicio).Seconds()
}

// filtrar recorta una colección al sector indicado (vacío = sin filtro).
func filtrar[T any](rows []T, sectorID string, get func(T) string) []T {
	if sectorID == "" {
		return rows
	}
	out := make([]T, 0, len(rows))
	for _, row := range rows {
		if get(row) == sectorID {
			out = append(out, row)
		}
	}
	return out
}

func pct(v float64) *float64 {
	p := round2(v)
	return &p
}

// round1/2/3 evitan el ruido de punto flotante en minutos, tasas y kg.
func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
