package consigna

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseValidManualRequest returns a fully valid ConsignaManualRequest for use as a test baseline.
func baseValidManualRequest() ConsignaManualRequest {
	return ConsignaManualRequest{
		HornoID:                "horno-01",
		ProductoID:             "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		TemperaturaObjetivo:    170.00,
		VelocidadCintaObjetivo: 0.20,
	}
}

func TestValidate_ValidRequest(t *testing.T) {
	req := baseValidManualRequest()
	assert.NoError(t, req.Validate(), "un request válido no debe producir errores")
}

func TestValidate_EmptyHornoID(t *testing.T) {
	req := baseValidManualRequest()
	req.HornoID = ""

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hornoId")
}

func TestValidate_WhitespaceHornoID(t *testing.T) {
	req := baseValidManualRequest()
	req.HornoID = "   "

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hornoId")
}

func TestValidate_EmptyProductoID(t *testing.T) {
	req := baseValidManualRequest()
	req.ProductoID = ""

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "productoId")
}

func TestValidate_ZeroTemperatura(t *testing.T) {
	req := baseValidManualRequest()
	req.TemperaturaObjetivo = 0

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temperaturaObjetivo")
}

func TestValidate_NegativeTemperatura(t *testing.T) {
	req := baseValidManualRequest()
	req.TemperaturaObjetivo = -10

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temperaturaObjetivo")
}

func TestValidate_ZeroVelocidad(t *testing.T) {
	req := baseValidManualRequest()
	req.VelocidadCintaObjetivo = 0

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "velocidadCintaObjetivo")
}

func TestValidate_NegativeVelocidad(t *testing.T) {
	req := baseValidManualRequest()
	req.VelocidadCintaObjetivo = -0.1

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "velocidadCintaObjetivo")
}

func TestValidate_MultipleErrorsAggregated(t *testing.T) {
	req := ConsignaManualRequest{}

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hornoId")
	assert.Contains(t, err.Error(), "productoId")
	assert.Contains(t, err.Error(), "temperaturaObjetivo")
	assert.Contains(t, err.Error(), "velocidadCintaObjetivo")
}
