package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	loteSectorService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
)

// fakeMetricasRepo es un repo en memoria para tests del handler.
type fakeMetricasRepo struct {
	saved []metricashorno.MetricaHorno
}

func (f *fakeMetricasRepo) Save(_ context.Context, m *metricashorno.MetricaHorno) error {
	m.ID = "met-1"
	m.ReceivedAt = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	f.saved = append(f.saved, *m)
	return nil
}

func (f *fakeMetricasRepo) GetUltimasParaLote(_ context.Context, loteID string, _ int) ([]metricashorno.MetricaHorno, error) {
	out := []metricashorno.MetricaHorno{}
	for _, m := range f.saved {
		if m.LoteID == loteID {
			out = append(out, m)
		}
	}
	return out, nil
}

func TestMetricasHornoRequestValidate(t *testing.T) {
	confianza := -0.1
	r := metricashorno.MetricaHornoRequest{ConfianzaDeteccion: &confianza}
	require.Error(t, r.Validate())

	r = metricashorno.MetricaHornoRequest{}
	require.NoError(t, r.Validate())

	confianza = 0.9
	r.ConfianzaDeteccion = &confianza
	require.NoError(t, r.Validate())
}

func TestExtractLoteIDFromPath(t *testing.T) {
	cases := []struct {
		path    string
		want    string
		wantErr bool
	}{
		{"/api/v1/lotes/a1b2c3d4-5678-90ab-cdef-1234567890ab/metricas-horno", "a1b2c3d4-5678-90ab-cdef-1234567890ab", false},
		{"/api/v1/lotes//metricas-horno", "", true},
		{"/otro/path", "", true},
		{"/api/v1/lotes/abc/extra/metricas-horno", "", true},
	}
	for _, c := range cases {
		got, err := extractLoteIDFromPath(c.path, "/metricas-horno")
		if c.wantErr {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, c.want, got)
	}
}

// TestLookupServiceCompatibility verifica que el service real satisface la
// interfaz que el handler declara. Si la firma cambia, el build falla acá
// antes que en runtime.
func TestLookupServiceCompatibility(t *testing.T) {
	var _ LoteLookupService = (*loteSectorService.Service)(nil)
}
