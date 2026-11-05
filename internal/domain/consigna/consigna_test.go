package consigna

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseValidManualRequest returns a fully valid ConsignaManualRequest for use as a test baseline.
func baseValidManualRequest() ConsignaManualRequest {
	loteID := "lote-1"
	return ConsignaManualRequest{
		SectorID:               "horno-01",
		ProductoID:             "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		TemperaturaObjetivo:    170.00,
		VelocidadCintaObjetivo: 0.20,
		LoteID:                 &loteID,
	}
}

func TestValidate_ValidRequest(t *testing.T) {
	req := baseValidManualRequest()
	assert.NoError(t, req.Validate(), "un request válido no debe producir errores")
}

func TestValidate_ValidRequestWithoutSector(t *testing.T) {
	req := baseValidManualRequest()
	req.SectorID = ""
	req.HornoID = ""

	assert.NoError(t, req.Validate(), "el sector es opcional: se deduce del lote")
}

func TestValidate_MissingLoteID(t *testing.T) {
	req := baseValidManualRequest()
	req.LoteID = nil

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loteId")
}

func TestValidate_EmptyLoteID(t *testing.T) {
	req := baseValidManualRequest()
	vacio := "   "
	req.LoteID = &vacio

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loteId")
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
	assert.Contains(t, err.Error(), "loteId")
	assert.Contains(t, err.Error(), "productoId")
	assert.Contains(t, err.Error(), "temperaturaObjetivo")
	assert.Contains(t, err.Error(), "velocidadCintaObjetivo")
}

func TestOvenID_PrefersSectorID(t *testing.T) {
	req := ConsignaManualRequest{SectorID: " sector-1 ", HornoID: "horno-01"}
	assert.Equal(t, "sector-1", req.OvenID())

	req = ConsignaManualRequest{HornoID: " horno-01 "}
	assert.Equal(t, "horno-01", req.OvenID())

	req = ConsignaManualRequest{}
	assert.Equal(t, "", req.OvenID())
}
