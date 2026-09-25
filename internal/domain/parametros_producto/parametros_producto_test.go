package parametros_producto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseValidRequest returns a fully valid ParametroProductoRequest for use as a test baseline.
func baseValidRequest() ParametroProductoRequest {
	return ParametroProductoRequest{
		ProductoID:            "a1b2c3d4-5678-90ab-cdef-1234567890ab",
		PesoReferenciaKg:      0.030,
		ToleranciaPesoPct:     10.00,
		DimensionBaseCm:       8.00,
		ToleranciaDimensionCm: 0.50,
		TempMin:               160.00,
		TempMax:               180.00,
		VelocidadCintaMin:     0.10,
		VelocidadCintaMax:     0.30,
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
	assert.Contains(t, err.Error(), "productoId")
}

func TestValidate_WhitespaceProductoID(t *testing.T) {
	req := baseValidRequest()
	req.ProductoID = "   "

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "productoId")
}

func TestValidate_ZeroPesoReferencia(t *testing.T) {
	req := baseValidRequest()
	req.PesoReferenciaKg = 0

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pesoReferenciaKg")
}

func TestValidate_NegativePesoReferencia(t *testing.T) {
	req := baseValidRequest()
	req.PesoReferenciaKg = -0.5

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pesoReferenciaKg")
}

func TestValidate_NegativeToleranciaPeso(t *testing.T) {
	req := baseValidRequest()
	req.ToleranciaPesoPct = -1

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "toleranciaPesoPct")
}

func TestValidate_ZeroToleranciaPesoIsValid(t *testing.T) {
	req := baseValidRequest()
	req.ToleranciaPesoPct = 0

	assert.NoError(t, req.Validate())
}

func TestValidate_ZeroDimensionBase(t *testing.T) {
	req := baseValidRequest()
	req.DimensionBaseCm = 0

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dimensionBaseCm")
}

func TestValidate_NegativeToleranciaDimension(t *testing.T) {
	req := baseValidRequest()
	req.ToleranciaDimensionCm = -0.1

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "toleranciaDimensionCm")
}

func TestValidate_TempMaxNotGreaterThanMin(t *testing.T) {
	req := baseValidRequest()
	req.TempMin = 180
	req.TempMax = 160

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tempMax")
}

func TestValidate_TempMaxEqualsMin(t *testing.T) {
	req := baseValidRequest()
	req.TempMin = 170
	req.TempMax = 170

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tempMax")
}

func TestValidate_VelocidadMaxNotGreaterThanMin(t *testing.T) {
	req := baseValidRequest()
	req.VelocidadCintaMin = 0.30
	req.VelocidadCintaMax = 0.10

	err := req.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "velocidadCintaMax")
}

func TestValidate_MultipleErrors(t *testing.T) {
	req := baseValidRequest()
	req.ProductoID = ""
	req.PesoReferenciaKg = 0
	req.TempMax = req.TempMin // rango de temperatura inválido

	err := req.Validate()
	require.Error(t, err)

	ve, ok := err.(*ValidationError)
	require.True(t, ok, "debe ser un *ValidationError")
	assert.GreaterOrEqual(t, len(ve.Fields), 3)
}

func TestMapRequestToParametroProducto(t *testing.T) {
	req := baseValidRequest()
	p := MapRequestToParametroProducto(req)

	assert.Equal(t, req.ProductoID, p.ProductoID)
	assert.Equal(t, req.PesoReferenciaKg, p.PesoReferenciaKg)
	assert.Equal(t, req.TempMin, p.TempMin)
	assert.Equal(t, req.TempMax, p.TempMax)
	assert.True(t, p.Activo, "un nuevo registro debe crearse activo por defecto")
}
