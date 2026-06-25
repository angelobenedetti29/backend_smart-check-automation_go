package lote

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseValidRequest returns a fully valid LoteRequest for use as a test baseline.
func baseValidRequest() LoteRequest {
	now := time.Now().UTC()
	vel := 3.20
	return LoteRequest{
		ProductoID:     "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		Turno:          "mañana",
		InicioAt:       now.Add(-2 * time.Hour),
		FinAt:          now,
		TotalUnidades:  1200,
		Correctos:      1150,
		Quemados:       50,
		CorrectosKg:    138.00,
		QuemadosKg:     6.00,
		VelocidadCinta: &vel,
	}
}

func TestValidate_ValidRequest(t *testing.T) {
	req := baseValidRequest()
	assert.NoError(t, req.Validate(), "un request válido no debe producir errores")
}

func TestValidate_EmptyProductoID(t *testing.T) {
	req := baseValidRequest()
	req.ProductoID = ""

	err := req.Validate()
	require.Error(t, err)
	assert.True(t, IsValidationError(err))
	assert.Contains(t, err.Error(), "productoId")
}

func TestValidate_WhitespaceProductoID(t *testing.T) {
	req := baseValidRequest()
	req.ProductoID = "   "

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "productoId")
}

func TestValidate_InvalidTurno(t *testing.T) {
	req := baseValidRequest()
	req.Turno = "mediodia"

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "turno")
}

func TestValidate_AllValidTurnos(t *testing.T) {
	for _, turno := range []string{"mañana", "tarde", "noche"} {
		t.Run(turno, func(t *testing.T) {
			req := baseValidRequest()
			req.Turno = turno
			assert.NoError(t, req.Validate())
		})
	}
}

func TestValidate_FinBeforeInicio(t *testing.T) {
	req := baseValidRequest()
	req.InicioAt = time.Now()
	req.FinAt = req.InicioAt.Add(-1 * time.Hour) // FinAt before InicioAt

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "finAt")
}

func TestValidate_FinEqualsInicio(t *testing.T) {
	req := baseValidRequest()
	req.FinAt = req.InicioAt // FinAt == InicioAt is valid

	assert.NoError(t, req.Validate())
}

func TestValidate_NegativeTotalUnidades(t *testing.T) {
	req := baseValidRequest()
	req.TotalUnidades = -1
	req.Correctos = 0
	req.Quemados = 0

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "totalUnidades")
}

func TestValidate_NegativeCorrectos(t *testing.T) {
	req := baseValidRequest()
	req.Correctos = -1
	req.TotalUnidades = req.Quemados + req.Correctos // will still fail sum

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "correctos")
}

func TestValidate_SumExceedsTotal(t *testing.T) {
	req := baseValidRequest()
	req.TotalUnidades = 100 // correctos(1150) + quemados(50) = 1200 > 100

	err := req.Validate()
	require.Error(t, err)
	assert.True(t, IsValidationError(err))
	assert.Contains(t, err.Error(), "totalUnidades")
}

func TestValidate_SumWithinTotal(t *testing.T) {
	req := baseValidRequest()
	req.TotalUnidades = 1500 // correctos(1150) + quemados(50) = 1200 <= 1500

	assert.NoError(t, req.Validate())
}

func TestValidate_NegativeCorrectosKg(t *testing.T) {
	req := baseValidRequest()
	req.CorrectosKg = -1.5

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "correctosKg")
}

func TestValidate_NegativeQuemadosKg(t *testing.T) {
	req := baseValidRequest()
	req.QuemadosKg = -0.1

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quemadosKg")
}

func TestValidate_NegativeVelocidadCinta(t *testing.T) {
	req := baseValidRequest()
	neg := -3.0
	req.VelocidadCinta = &neg

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "velocidadCinta")
}

func TestValidate_MultipleErrors(t *testing.T) {
	req := baseValidRequest()
	req.ProductoID = ""
	req.Turno = "invalido"
	req.TotalUnidades = 100 // correctos(1150) + quemados(50) = 1200 > 100 → error de suma

	err := req.Validate()
	require.Error(t, err)

	ve, ok := err.(*ValidationError)
	require.True(t, ok, "debe ser un *ValidationError")
	// Debe reportar al menos 3 problemas: productoId, turno, suma
	assert.GreaterOrEqual(t, len(ve.Fields), 3)
}

func TestIsValidationError(t *testing.T) {
	req := baseValidRequest()
	req.ProductoID = ""

	err := req.Validate()
	assert.True(t, IsValidationError(err))
}
