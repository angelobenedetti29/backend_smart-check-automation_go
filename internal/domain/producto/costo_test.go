package producto

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestCostoFabricacionValidacion(t *testing.T) {
	for _, value := range []float64{0, 0.1, 125.50, 9999999999.99} {
		req := ProductoRequest{CostoFabricacionUnitarioARS: &value}
		err := req.Validate().(*ValidationError) // los demás campos están vacíos
		require.NotContains(t, err.Fields, "costoFabricacionUnitarioArs")
	}
	for _, value := range []float64{-1, 0.001, 10000000000, math.NaN(), math.Inf(1)} {
		req := ProductoRequest{CostoFabricacionUnitarioARS: &value}
		err := req.Validate().(*ValidationError)
		require.Contains(t, err.Fields, "costoFabricacionUnitarioArs")
	}
}

func TestCostoFabricacionDistingueAusenteNullYCero(t *testing.T) {
	var req ProductoRequest
	require.NoError(t, json.Unmarshal([]byte(`{"nombre":"Pan"}`), &req))
	require.False(t, req.CostoFabricacionInformado)
	require.NoError(t, json.Unmarshal([]byte(`{"costoFabricacionUnitarioArs":null}`), &req))
	require.True(t, req.CostoFabricacionInformado)
	require.Nil(t, req.CostoFabricacionUnitarioARS)
	require.NoError(t, json.Unmarshal([]byte(`{"costoFabricacionUnitarioArs":0}`), &req))
	require.True(t, req.CostoFabricacionInformado)
	require.Equal(t, 0.0, *req.CostoFabricacionUnitarioARS)
	require.Error(t, json.Unmarshal([]byte(`{"costoFabricacionUnitarioArs":"125"}`), &req))
}
