// Package service implementa la importación CSV del plan de producción y las
// paradas (fase 1 de OEE).
package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "time/tzdata" // tzdata embebida para ART fuera de la imagen Docker.

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/planificacion"
)

const (
	fechaLayout     = "2006-01-02"
	fechaHoraLayout = "2006-01-02 15:04"

	// Reglas del listado: período ART inclusivo, default 30 días, máximo 366.
	diasDefaultListado = 30
	diasMaximosListado = 366
	limiteDefault      = 500
	limiteMaximo       = 2000
)

var uuidRegexp = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var (
	headersPlan   = []string{"sector_id", "fecha", "turno", "producto_id", "inicio", "fin", "kg_plan"}
	headersParada = []string{"sector_id", "lote_id", "causa_codigo", "tipo", "inicio", "fin", "notas"}

	turnosValidos = map[string]bool{"mañana": true, "tarde": true, "noche": true}
	tiposParada   = map[string]bool{"PLANIFICADA": true, "NO_PLANIFICADA": true}

	artOnce sync.Once
	artLoc  *time.Location
)

// zonaART resuelve America/Argentina/Buenos_Aires; si el sistema no trae
// tzdata cae al offset fijo -03 (Argentina no aplica horario de verano).
func zonaART() *time.Location {
	artOnce.Do(func() {
		loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
		if err != nil {
			loc = time.FixedZone("ART", -3*60*60)
		}
		artLoc = loc
	})
	return artLoc
}

// Service orquesta parseo, validación y persistencia de las importaciones CSV.
type Service struct {
	repo planificacion.Repository
	now  func() time.Time
}

// NewService instancia el servicio inyectando el repositorio de planificación.
func NewService(repo planificacion.Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// Query son los parámetros crudos de un listado (strings vacíos = ausentes).
type Query struct {
	Desde    string
	Hasta    string
	SectorID string
	Limite   string
}

// ListarPlan devuelve el plan del período para el panel.
func (s *Service) ListarPlan(ctx context.Context, q Query) (planificacion.Listado[planificacion.PlanItem], error) {
	f, err := s.parseFiltro(q)
	if err != nil {
		return planificacion.Listado[planificacion.PlanItem]{}, err
	}
	return s.repo.ListPlanItems(ctx, f)
}

// ListarParadas devuelve las paradas del período para el panel.
func (s *Service) ListarParadas(ctx context.Context, q Query) (planificacion.Listado[planificacion.ParadaItem], error) {
	f, err := s.parseFiltro(q)
	if err != nil {
		return planificacion.Listado[planificacion.ParadaItem]{}, err
	}
	return s.repo.ListParadaItems(ctx, f)
}

// EliminarPlan borra una fila del plan por id.
func (s *Service) EliminarPlan(ctx context.Context, id string) error {
	return s.eliminar(ctx, id, s.repo.DeletePlan)
}

// EliminarParada borra un evento de parada por id.
func (s *Service) EliminarParada(ctx context.Context, id string) error {
	return s.eliminar(ctx, id, s.repo.DeleteParada)
}

func (s *Service) eliminar(ctx context.Context, id string, del func(context.Context, string) (bool, error)) error {
	if !uuidRegexp.MatchString(id) {
		return planificacion.ErrIDInvalido
	}
	ok, err := del(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return planificacion.ErrNoEncontrado
	}
	return nil
}

// parseFiltro valida el período (día ART inclusive) y el límite, y arma el
// filtro del repositorio con Hasta exclusivo.
func (s *Service) parseFiltro(q Query) (planificacion.FiltroListado, error) {
	hoy := s.now().In(zonaART())
	hasta := time.Date(hoy.Year(), hoy.Month(), hoy.Day(), 0, 0, 0, 0, zonaART())
	if q.Hasta != "" {
		parseada, err := time.ParseInLocation(fechaLayout, q.Hasta, zonaART())
		if err != nil {
			return planificacion.FiltroListado{}, planificacion.ErrFechaInvalida
		}
		hasta = parseada
	}

	desde := hasta.AddDate(0, 0, -(diasDefaultListado - 1))
	if q.Desde != "" {
		parseada, err := time.ParseInLocation(fechaLayout, q.Desde, zonaART())
		if err != nil {
			return planificacion.FiltroListado{}, planificacion.ErrFechaInvalida
		}
		desde = parseada
	}

	if desde.After(hasta) {
		return planificacion.FiltroListado{}, planificacion.ErrDesdeMayorHasta
	}
	if int(hasta.Sub(desde).Hours()/24)+1 > diasMaximosListado {
		return planificacion.FiltroListado{}, planificacion.ErrRangoExcedido
	}

	limite, err := parseLimite(q.Limite)
	if err != nil {
		return planificacion.FiltroListado{}, err
	}

	return planificacion.FiltroListado{
		SectorID: q.SectorID,
		Desde:    desde,
		Hasta:    hasta.AddDate(0, 0, 1),
		Limite:   limite,
	}, nil
}

// parseLimite replica las reglas de reportes: no numérico es error, no
// positivo cae al default y por encima del tope se recorta.
func parseLimite(raw string) (int, error) {
	if raw == "" {
		return limiteDefault, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, planificacion.ErrLimiteInvalido
	}
	if n <= 0 {
		return limiteDefault, nil
	}
	if n > limiteMaximo {
		return limiteMaximo, nil
	}
	return n, nil
}

// ImportarPlan procesa un CSV de plan de producción.
func (s *Service) ImportarPlan(ctx context.Context, input io.Reader) (planificacion.ImportResult, error) {
	records, err := leerCSV(input, headersPlan)
	res := planificacion.ImportResult{Procesadas: len(records), Errores: []planificacion.ErrorImportacion{}}
	if err != nil {
		return res, err
	}

	type pendiente struct {
		fila int
		row  planificacion.PlanRow
	}
	var pendientes []pendiente
	for i, rec := range records {
		row, msg := parsePlanRow(rec, len(headersPlan))
		if msg != "" {
			res.Errores = append(res.Errores, planificacion.ErrorImportacion{Fila: i + 2, Mensaje: msg})
			continue
		}
		pendientes = append(pendientes, pendiente{fila: i + 2, row: row})
	}

	idsSector := make([]string, 0, len(pendientes))
	idsProducto := make([]string, 0, len(pendientes))
	for _, p := range pendientes {
		idsSector = append(idsSector, p.row.SectorID)
		idsProducto = append(idsProducto, p.row.ProductoID)
	}
	sectores, err := s.repo.SectoresExistentes(ctx, uniqueStrings(idsSector))
	if err != nil {
		return res, err
	}
	productos, err := s.repo.ProductosExistentes(ctx, uniqueStrings(idsProducto))
	if err != nil {
		return res, err
	}

	validas := make([]planificacion.PlanRow, 0, len(pendientes))
	validadas := make([]pendiente, 0, len(pendientes))
	for _, p := range pendientes {
		switch {
		case !sectores[p.row.SectorID]:
			res.Errores = append(res.Errores, planificacion.ErrorImportacion{Fila: p.fila, Mensaje: "sector_id inexistente"})
		case !productos[p.row.ProductoID]:
			res.Errores = append(res.Errores, planificacion.ErrorImportacion{Fila: p.fila, Mensaje: "producto_id inexistente"})
		default:
			validadas = append(validadas, p)
		}
	}

	// Solapes por sector: se traen los planes existentes del rango completo de
	// cada sector y se comparan contra ellos y contra las filas ya aceptadas.
	rangos := map[string][2]time.Time{}
	ordenSectores := make([]string, 0, len(validadas))
	for _, p := range validadas {
		r, ok := rangos[p.row.SectorID]
		if !ok {
			rangos[p.row.SectorID] = [2]time.Time{p.row.InicioAt, p.row.FinAt}
			ordenSectores = append(ordenSectores, p.row.SectorID)
			continue
		}
		if p.row.InicioAt.Before(r[0]) {
			r[0] = p.row.InicioAt
		}
		if p.row.FinAt.After(r[1]) {
			r[1] = p.row.FinAt
		}
		rangos[p.row.SectorID] = r
	}
	existentes := make(map[string][]planificacion.PlanRow, len(ordenSectores))
	for _, sector := range ordenSectores {
		r := rangos[sector]
		planes, err := s.repo.PlanSolapados(ctx, sector, r[0], r[1])
		if err != nil {
			return res, err
		}
		existentes[sector] = planes
	}

	aceptadas := make(map[string][]planificacion.PlanRow, len(ordenSectores))
	for _, p := range validadas {
		if solapaPlan(p.row, existentes[p.row.SectorID], aceptadas[p.row.SectorID]) {
			res.Errores = append(res.Errores, planificacion.ErrorImportacion{
				Fila: p.fila, Mensaje: "solape con otro plan del sector",
			})
			continue
		}
		aceptadas[p.row.SectorID] = append(aceptadas[p.row.SectorID], p.row)
		validas = append(validas, p.row)
	}
	ordenarErrores(&res)

	if len(validas) == 0 {
		return res, nil
	}
	res.Insertadas, res.Actualizadas, err = s.repo.UpsertPlan(ctx, validas)
	if err != nil {
		return res, err
	}
	return res, nil
}

// ImportarParadas procesa un CSV de eventos de parada.
func (s *Service) ImportarParadas(ctx context.Context, input io.Reader) (planificacion.ImportResult, error) {
	records, err := leerCSV(input, headersParada)
	res := planificacion.ImportResult{Procesadas: len(records), Errores: []planificacion.ErrorImportacion{}}
	if err != nil {
		return res, err
	}

	type pendiente struct {
		fila int
		row  planificacion.ParadaRow
	}
	var pendientes []pendiente
	for i, rec := range records {
		row, msg := parseParadaRow(rec, len(headersParada))
		if msg != "" {
			res.Errores = append(res.Errores, planificacion.ErrorImportacion{Fila: i + 2, Mensaje: msg})
			continue
		}
		pendientes = append(pendientes, pendiente{fila: i + 2, row: row})
	}

	idsSector := make([]string, 0, len(pendientes))
	idsLote := make([]string, 0, len(pendientes))
	for _, p := range pendientes {
		idsSector = append(idsSector, p.row.SectorID)
		if p.row.LoteID != "" {
			idsLote = append(idsLote, p.row.LoteID)
		}
	}
	sectores, err := s.repo.SectoresExistentes(ctx, uniqueStrings(idsSector))
	if err != nil {
		return res, err
	}
	lotes, err := s.repo.LotesExistentes(ctx, uniqueStrings(idsLote))
	if err != nil {
		return res, err
	}

	validas := make([]planificacion.ParadaRow, 0, len(pendientes))
	for _, p := range pendientes {
		switch {
		case !sectores[p.row.SectorID]:
			res.Errores = append(res.Errores, planificacion.ErrorImportacion{Fila: p.fila, Mensaje: "sector_id inexistente"})
		case p.row.LoteID != "" && !lotes[p.row.LoteID]:
			res.Errores = append(res.Errores, planificacion.ErrorImportacion{Fila: p.fila, Mensaje: "lote_id inexistente"})
		default:
			validas = append(validas, p.row)
		}
	}
	ordenarErrores(&res)

	if len(validas) == 0 {
		return res, nil
	}
	res.Insertadas, res.Actualizadas, err = s.repo.UpsertParadas(ctx, validas)
	if err != nil {
		return res, err
	}
	return res, nil
}

// leerCSV valida la cabecera exacta (tolerando BOM UTF-8) y devuelve las filas
// de datos. Un error de estructura se envuelve con ErrFormatoCSV.
func leerCSV(input io.Reader, esperada []string) ([][]string, error) {
	br := bufio.NewReader(input)
	if bom, err := br.Peek(3); err == nil && bytes.Equal(bom, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = br.Discard(3)
	}

	reader := csv.NewReader(br)
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: cabecera ilegible", planificacion.ErrFormatoCSV)
	}
	if !headersIguales(header, esperada) {
		return nil, fmt.Errorf("%w: se esperaba %s", planificacion.ErrFormatoCSV, strings.Join(esperada, ","))
	}

	records := [][]string{}
	for {
		rec, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", planificacion.ErrFormatoCSV, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

// headersIguales compara la cabecera normalizada contra la esperada.
func headersIguales(header, esperada []string) bool {
	if len(header) != len(esperada) {
		return false
	}
	for i := range esperada {
		if strings.TrimSpace(header[i]) != esperada[i] {
			return false
		}
	}
	return true
}

// parsePlanRow valida y normaliza una fila de plan. Devuelve el mensaje de
// error de la primera regla que falla, o "" si la fila es válida.
func parsePlanRow(rec []string, columnas int) (planificacion.PlanRow, string) {
	var row planificacion.PlanRow
	if len(rec) != columnas {
		return row, fmt.Sprintf("cantidad de columnas inválida (se esperaban %d)", columnas)
	}

	row.SectorID = strings.TrimSpace(rec[0])
	if row.SectorID == "" {
		return row, "sector_id es requerido"
	}
	fecha, err := time.ParseInLocation(fechaLayout, strings.TrimSpace(rec[1]), zonaART())
	if err != nil {
		return row, "fecha inválida (se esperaba YYYY-MM-DD)"
	}
	row.Fecha = fecha
	row.Turno = strings.TrimSpace(rec[2])
	if !turnosValidos[row.Turno] {
		return row, "turno inválido (mañana, tarde o noche)"
	}
	row.ProductoID = strings.TrimSpace(rec[3])
	if row.ProductoID == "" {
		return row, "producto_id es requerido"
	}
	if row.InicioAt, err = parseTimestamp(rec[4]); err != nil {
		return row, "inicio inválido (YYYY-MM-DD HH:MM o RFC3339)"
	}
	if row.FinAt, err = parseTimestamp(rec[5]); err != nil {
		return row, "fin inválido (YYYY-MM-DD HH:MM o RFC3339)"
	}
	if !row.FinAt.After(row.InicioAt) {
		return row, "fin debe ser posterior a inicio"
	}
	row.KgPlan, err = strconv.ParseFloat(strings.TrimSpace(rec[6]), 64)
	if err != nil || row.KgPlan <= 0 {
		return row, "kg_plan debe ser un número mayor a 0"
	}
	return row, ""
}

// solapaPlan indica si row solapa algún plan vecino con la regla half-open
// (a.inicio < b.fin && b.inicio < a.fin). La misma clave de upsert se omite:
// reimportar esa fila actualiza en lugar de fallar.
func solapaPlan(row planificacion.PlanRow, vecinos ...[]planificacion.PlanRow) bool {
	for _, lista := range vecinos {
		for _, o := range lista {
			if mismaClavePlan(o, row) {
				continue
			}
			if o.InicioAt.Before(row.FinAt) && row.InicioAt.Before(o.FinAt) {
				return true
			}
		}
	}
	return false
}

func mismaClavePlan(a, b planificacion.PlanRow) bool {
	return a.SectorID == b.SectorID && a.InicioAt.Equal(b.InicioAt) && a.ProductoID == b.ProductoID
}

// parseParadaRow valida y normaliza una fila de parada.
func parseParadaRow(rec []string, columnas int) (planificacion.ParadaRow, string) {
	var row planificacion.ParadaRow
	if len(rec) != columnas {
		return row, fmt.Sprintf("cantidad de columnas inválida (se esperaban %d)", columnas)
	}

	row.SectorID = strings.TrimSpace(rec[0])
	if row.SectorID == "" {
		return row, "sector_id es requerido"
	}
	row.LoteID = strings.TrimSpace(rec[1])
	row.CausaCodigo = strings.TrimSpace(rec[2])
	if row.CausaCodigo == "" {
		return row, "causa_codigo es requerido"
	}
	row.Tipo = strings.TrimSpace(rec[3])
	if !tiposParada[row.Tipo] {
		return row, "tipo inválido (PLANIFICADA o NO_PLANIFICADA)"
	}
	var err error
	if row.InicioAt, err = parseTimestamp(rec[4]); err != nil {
		return row, "inicio inválido (YYYY-MM-DD HH:MM o RFC3339)"
	}
	if row.FinAt, err = parseTimestamp(rec[5]); err != nil {
		return row, "fin inválido (YYYY-MM-DD HH:MM o RFC3339)"
	}
	if !row.FinAt.After(row.InicioAt) {
		return row, "fin debe ser posterior a inicio"
	}
	row.Notas = strings.TrimSpace(rec[6])
	return row, ""
}

// parseTimestamp acepta RFC3339 o el formato local YYYY-MM-DD HH:MM (ART).
func parseTimestamp(raw string) (time.Time, error) {
	s := strings.TrimSpace(raw)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.ParseInLocation(fechaHoraLayout, s, zonaART())
}

// ordenarErrores deja los avisos en orden de fila para una respuesta estable.
func ordenarErrores(res *planificacion.ImportResult) {
	sort.SliceStable(res.Errores, func(i, j int) bool {
		return res.Errores[i].Fila < res.Errores[j].Fila
	})
}

// uniqueStrings deduplica preservando el orden de aparición.
func uniqueStrings(in []string) []string {
	vistos := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || vistos[s] {
			continue
		}
		vistos[s] = true
		out = append(out, s)
	}
	return out
}
