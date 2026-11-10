// Package service implementa la lógica de negocio del CRUD de sectores:
// generación del id legible, validación del nombre y coordinación con el
// repositorio para el bloqueo de borrado cuando hay lotes asociados.
package service

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
)

// maxSectorIDLength es el largo máximo del id de sector, coherente con el
// VARCHAR(50) de la tabla sectores (database/schema.sql).
const maxSectorIDLength = 50

// maxIntentosID acota los reintentos de generación de id ante colisiones.
const maxIntentosID = 100

// Service orquesta el CRUD de sectores sobre el repositorio.
type Service struct {
	repo sector.Repository
}

// NewService instancia el servicio de sectores.
func NewService(repo sector.Repository) *Service {
	return &Service{repo: repo}
}

// List devuelve todos los sectores ordenados por nombre.
func (s *Service) List(ctx context.Context) ([]sector.Sector, error) {
	return s.repo.List(ctx)
}

// Create da de alta un sector generando un id legible a partir del nombre
// (slug en minúsculas, sin acentos y con guiones). Ante colisión de id agrega un
// sufijo numérico (-2, -3, ...) hasta maxIntentosID.
func (s *Service) Create(ctx context.Context, req sector.CreateSectorRequest) (*sector.Sector, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	nombre := strings.TrimSpace(req.Nombre)
	base := slugify(nombre)

	for intento := 1; intento <= maxIntentosID; intento++ {
		candidato := base
		if intento > 1 {
			candidato = withSuffix(base, intento)
		}
		sec := &sector.Sector{ID: candidato, Nombre: nombre}
		if err := s.repo.Create(ctx, sec); err != nil {
			if errors.Is(err, sector.ErrSectorIDExists) {
				continue
			}
			return nil, err
		}
		return sec, nil
	}
	return nil, errors.New("sector: no se pudo generar un id único")
}

// Update modifica el nombre de un sector existente. El id es inmutable.
func (s *Service) Update(ctx context.Context, id string, req sector.UpdateSectorRequest) (*sector.Sector, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	sec := &sector.Sector{ID: strings.TrimSpace(id), Nombre: strings.TrimSpace(req.Nombre)}
	if err := s.repo.Update(ctx, sec); err != nil {
		return nil, err
	}
	return sec, nil
}

// Delete elimina un sector sin lotes asociados. Propaga ErrSectorNotFound si no
// existe y ErrSectorConLotes si todavía tiene lotes productivos.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, strings.TrimSpace(id))
}

// withSuffix agrega "-N" al slug recortándolo si es necesario para no superar
// maxSectorIDLength.
func withSuffix(base string, n int) string {
	suffix := "-" + strconv.Itoa(n)
	runes := []rune(base)
	max := maxSectorIDLength - len([]rune(suffix))
	if max < 1 {
		max = 1
	}
	if len(runes) > max {
		runes = runes[:max]
	}
	return strings.Trim(string(runes), "-") + suffix
}

// acentos mapea vocales acentuadas y la eñe a su equivalente ASCII.
var acentos = map[rune]rune{
	'á': 'a', 'à': 'a', 'ä': 'a', 'â': 'a', 'ã': 'a',
	'é': 'e', 'è': 'e', 'ë': 'e', 'ê': 'e',
	'í': 'i', 'ì': 'i', 'ï': 'i', 'î': 'i',
	'ó': 'o', 'ò': 'o', 'ö': 'o', 'ô': 'o', 'õ': 'o',
	'ú': 'u', 'ù': 'u', 'ü': 'u', 'û': 'u',
	'ñ': 'n', 'ç': 'c',
}

// slugify convierte un nombre en un id legible: minúsculas, sin acentos, con
// guiones en lugar de separadores, sin guiones repetidos ni en los extremos y de
// hasta maxSectorIDLength runes. Devuelve "sector" si no queda ningún carácter.
func slugify(nombre string) string {
	var b strings.Builder
	prevGuion := false
	for _, r := range strings.ToLower(strings.TrimSpace(nombre)) {
		if rep, ok := acentos[r]; ok {
			r = rep
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevGuion = false
			continue
		}
		if !prevGuion {
			b.WriteRune('-')
			prevGuion = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "sector"
	}
	if len([]rune(out)) > maxSectorIDLength {
		out = string([]rune(out)[:maxSectorIDLength])
	}
	return strings.Trim(out, "-")
}
