package notificacion

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
)

func TestRenderizarAlerta_HTMLEscapaYContieneDetalles(t *testing.T) {
	sector := `linea<script>alert(1)</script>`
	lote := "11111111-1111-1111-1111-111111111111"
	a := &alerta.Alerta{
		ID:        "22222222-2222-2222-2222-222222222222",
		SectorID:  &sector,
		LoteID:    &lote,
		HornoID:   "horno-1",
		Tipo:      alerta.TipoPorcentajeQuemadosAlto,
		Severidad: alerta.SeveridadWarning,
		Mensaje:   `Porcentaje <script>alert("xss")</script> alto`,
		CreadaEn:  time.Date(2026, 11, 6, 14, 3, 0, 0, artZone),
	}

	asunto, texto, htmlBody := RenderizarAlerta(a, "https://panel.test/")

	require.Contains(t, asunto, "Alto porcentaje de quemados")
	require.Contains(t, texto, "⚠️ Severidad: WARNING")
	require.Contains(t, texto, "Sector: linea<script>")
	require.Contains(t, texto, "https://panel.test/alertas")

	require.Contains(t, htmlBody, "<!DOCTYPE html>")
	require.Contains(t, htmlBody, "#b45309", "WARNING usa el color ámbar")
	require.Contains(t, htmlBody, "Alto porcentaje de quemados")
	require.Contains(t, htmlBody, "Ver en el panel")
	require.Contains(t, htmlBody, "https://panel.test/alertas")
	require.Contains(t, htmlBody, "Notificación automática de SmartCheck Automation")
	require.Contains(t, htmlBody, "06/11/2026 14:03 (ART)")

	// Ningún valor dinámico puede inyectar etiquetas.
	require.NotContains(t, htmlBody, "<script>")
	require.Contains(t, htmlBody, "&lt;script&gt;")
	require.Contains(t, htmlBody, "&lt;script&gt;alert(1)&lt;/script&gt;")
}

func TestRenderizarAlerta_SinPanelNoMuestraCTA(t *testing.T) {
	a := &alerta.Alerta{
		Tipo:      alerta.TipoTemperaturaHorno,
		Severidad: alerta.SeveridadCritical,
		Mensaje:   "T1 alta",
		CreadaEn:  time.Now(),
	}
	_, _, htmlBody := RenderizarAlerta(a, "")

	require.NotContains(t, htmlBody, "Ver en el panel")
	require.Contains(t, htmlBody, "#b91c1c", "CRITICAL usa el color rojo")
}
