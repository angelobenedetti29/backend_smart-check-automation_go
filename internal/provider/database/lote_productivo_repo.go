package database

import (
	"fmt"
	"sync"
	"time"

	lote "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_productivo"
)

// LoteProductivoRepository implementa lote_productivo.Repository con datos en memoria.
type LoteProductivoRepository struct {
	mu    sync.RWMutex
	lotes []lote.LoteProductivo
}

// NewLoteProductivoRepository inicializa el repositorio con datos de ejemplo.
func NewLoteProductivoRepository() *LoteProductivoRepository {
	now := time.Now()
	finAt := now.Add(-2 * time.Hour)

	tempComb1 := 320.50
	tempComb2 := 315.75
	temp1a := 185.30
	temp2a := 190.10
	temp1b := 192.00
	temp2b := 195.50
	vel1 := 1.20
	vel2 := 0.95

	return &LoteProductivoRepository{
		lotes: []lote.LoteProductivo{
			{
				ID:             "lote-001",
				ProductoID:     "prod-a1",
				ProductoNombre: "Ladrillo Hueco 8cm",
				Turno:          "mañana",
				InicioAt:       now.Add(-8 * time.Hour),
				FinAt:          &finAt,
				TotalUnidades:  1200,
				Correctos:      1150,
				Quemados:       50,
				Crudas:         nil,
				CorrectosKg:    2300.00,
				QuemadosKg:     100.00,
				CrudosKg:       nil,
				TempHorno1:     &temp1a,
				TempCombHorno1: &tempComb1,
				TempHorno2:     &temp2a,
				TempCombHorno2: &tempComb2,
				VelocidadCinta: &vel1,
				CreatedAt:      now.Add(-8 * time.Hour),
				UpdatedAt:      finAt,
			},
			{
				ID:             "lote-002",
				ProductoID:     "prod-b2",
				ProductoNombre: "Ladrillo Macizo 12cm",
				Turno:          "tarde",
				InicioAt:       now.Add(-4 * time.Hour),
				FinAt:          nil,
				TotalUnidades:  800,
				Correctos:      780,
				Quemados:       20,
				Crudas:         nil,
				CorrectosKg:    3120.00,
				QuemadosKg:     80.00,
				CrudosKg:       nil,
				TempHorno1:     &temp1b,
				TempCombHorno1: nil,
				TempHorno2:     &temp2b,
				TempCombHorno2: nil,
				VelocidadCinta: &vel2,
				CreatedAt:      now.Add(-4 * time.Hour),
				UpdatedAt:      now,
			},
		},
	}
}

// GetAll devuelve una página de lotes productivos ordenados por inicio_at descendente.
func (r *LoteProductivoRepository) GetAll(page, pageSize int) (*lote.PaginatedResult, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	total := len(r.lotes)

	start := (page - 1) * pageSize
	if start >= total {
		return &lote.PaginatedResult{
			Items:    []lote.LoteProductivo{},
			Total:    total,
			Page:     page,
			PageSize: pageSize,
		}, nil
	}

	end := start + pageSize
	if end > total {
		end = total
	}

	return &lote.PaginatedResult{
		Items:    r.lotes[start:end],
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// GetByID busca un lote productivo por su ID en el repositorio en memoria.
func (r *LoteProductivoRepository) GetByID(id string) (*lote.LoteProductivo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for i := range r.lotes {
		if r.lotes[i].ID == id {
			return &r.lotes[i], nil
		}
	}

	return nil, fmt.Errorf("lote productivo con id %s no encontrado", id)
}
