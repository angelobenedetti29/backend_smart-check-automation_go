package lote_productivo

import "time"

// LoteProductivo representa una tanda de producción registrada en el sistema.
type LoteProductivo struct {
	ID             string     `json:"id"               db:"id"`
	ProductoID     string     `json:"productoId"       db:"producto_id"`
	ProductoNombre string     `json:"productoNombre"   db:"producto_nombre"`
	Turno          string     `json:"turno"            db:"turno"`
	InicioAt       time.Time  `json:"inicioAt"         db:"inicio_at"`
	FinAt          *time.Time `json:"finAt,omitempty"  db:"fin_at"`
	TotalUnidades  int        `json:"totalUnidades"    db:"total_unidades"`
	Correctos      int        `json:"correctos"        db:"correctos"`
	Quemados       int        `json:"quemados"         db:"quemados"`
	CorrectosKg    float64    `json:"correctosKg"      db:"correctos_kg"`
	QuemadosKg     float64    `json:"quemadosKg"       db:"quemados_kg"`
	TempHorno1     float64    `json:"tempHorno1"       db:"temp_horno_1"`
	TempCombHorno1 *float64   `json:"tempCombHorno1,omitempty" db:"temp_comb_horno_1"`
	TempHorno2     float64    `json:"tempHorno2"       db:"temp_horno_2"`
	TempCombHorno2 *float64   `json:"tempCombHorno2,omitempty" db:"temp_comb_horno_2"`
	VelocidadHorno float64    `json:"velocidadHorno"   db:"velocidad_horno"`
	CreatedAt      time.Time  `json:"createdAt"        db:"created_at"`
	UpdatedAt      time.Time  `json:"updatedAt"        db:"updated_at"`
}

// PaginatedResult encapsula una página de lotes junto con metadatos de paginación.
type PaginatedResult struct {
	Items      []LoteProductivo `json:"items"`
	Total      int              `json:"total"`
	Page       int              `json:"page"`
	PageSize   int              `json:"pageSize"`
	TotalPages int              `json:"totalPages"`
}

// Repository define el contrato de persistencia para LoteProductivo.
type Repository interface {
	GetAll(page, pageSize int) (*PaginatedResult, error)
	GetByID(id string) (*LoteProductivo, error)
}

// Service define las operaciones de negocio para lotes productivos.
type Service interface {
	GetAll(page, pageSize int) (*PaginatedResult, error)
}
