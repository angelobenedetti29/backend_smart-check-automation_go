package lote

import "time"

// LoteRequest represents the incoming JSON payload from the Raspberry Pi
// for registering a productive batch in the industrial oven line.
type LoteRequest struct {
	ID              string    `json:"id"`
	ProductoID      string    `json:"productoId"`
	ProductoNombre  string    `json:"productoNombre"`
	Turno           string    `json:"turno"`
	InicioAt        time.Time `json:"inicioAt"`
	FinAt           time.Time `json:"finAt"`
	TotalUnidades   int       `json:"totalUnidades"`
	Correctos       int       `json:"correctos"`
	Quemados        int       `json:"quemados"`
	CorrectosKg     float64   `json:"correctosKg"`
	QuemadosKg      float64   `json:"quemadosKg"`
	TempHorno1      float64   `json:"tempHorno1"`
	TempCombHorno1  *float64  `json:"tempCombHorno1"`
	TempHorno2      float64   `json:"tempHorno2"`
	TempCombHorno2  *float64  `json:"tempCombHorno2"`
	VelocidadHorno  float64   `json:"velocidadHorno"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// Lote represents the domain entity for a productive batch as stored
// in the PostgreSQL database.
type Lote struct {
	ID              string    `json:"id"`
	ProductoID      string    `json:"producto_id"`
	Turno           string    `json:"turno"`
	InicioAt        time.Time `json:"inicio_at"`
	FinAt           time.Time `json:"fin_at"`
	TotalUnidades   int       `json:"total_unidades"`
	Correctos       int       `json:"correctos"`
	Quemados        int       `json:"quemados"`
	CorrectosKg     float64   `json:"correctos_kg"`
	QuemadosKg      float64   `json:"quemados_kg"`
	TempHorno1      float64   `json:"temp_horno_1"`
	TempCombHorno1  *float64  `json:"temp_comb_horno_1"`
	TempHorno2      float64   `json:"temp_horno_2"`
	TempCombHorno2  *float64  `json:"temp_comb_horno_2"`
	VelocidadHorno  float64   `json:"velocidad_horno"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// MapLoteRequestToLote converts a LoteRequest (API DTO from Raspberry Pi)
// into a Lote (domain entity). The ProductoNombre field is discarded
// since it belongs to the productos catalog, not the batch entity.
func MapLoteRequestToLote(req LoteRequest) Lote {
	return Lote{
		ID:             req.ID,
		ProductoID:     req.ProductoID,
		Turno:          req.Turno,
		InicioAt:       req.InicioAt,
		FinAt:          req.FinAt,
		TotalUnidades:  req.TotalUnidades,
		Correctos:      req.Correctos,
		Quemados:       req.Quemados,
		CorrectosKg:    req.CorrectosKg,
		QuemadosKg:     req.QuemadosKg,
		TempHorno1:     req.TempHorno1,
		TempCombHorno1: req.TempCombHorno1,
		TempHorno2:     req.TempHorno2,
		TempCombHorno2: req.TempCombHorno2,
		VelocidadHorno: req.VelocidadHorno,
		CreatedAt:      req.CreatedAt,
		UpdatedAt:      req.UpdatedAt,
	}
}
