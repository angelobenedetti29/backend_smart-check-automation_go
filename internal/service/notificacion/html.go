package notificacion

import (
	"fmt"
	"html"
	"strings"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
)

// RenderizarAlerta devuelve el asunto, el cuerpo de texto (fallback) y el HTML
// estilizado de una alerta. El texto conserva los emojis para clientes que no
// renderizan HTML.
func RenderizarAlerta(a *alerta.Alerta, panelURL string) (asunto, texto, htmlBody string) {
	tipo := tipoLegible(a.Tipo)
	emojiSev := emojiSeveridad(a.Severidad)
	emojiTipo := emojiTipoAlerta(a.Tipo)
	asunto = fmt.Sprintf("[SmartCheck] %s %s · %s", emojiSev, a.Severidad, tipo)

	var b strings.Builder
	fmt.Fprintf(&b, "%s Severidad: %s\n", emojiSev, a.Severidad)
	fmt.Fprintf(&b, "%s Tipo: %s\n", emojiTipo, tipo)
	fmt.Fprintf(&b, "📝 Mensaje: %s\n", a.Mensaje)
	if a.SectorID != nil && *a.SectorID != "" {
		fmt.Fprintf(&b, "🏭 Sector: %s\n", *a.SectorID)
	}
	if a.LoteID != nil && *a.LoteID != "" {
		fmt.Fprintf(&b, "🧾 Lote: %s\n", *a.LoteID)
	}
	if a.HornoID != "" {
		fmt.Fprintf(&b, "🔥 Horno: %s\n", a.HornoID)
	}
	fmt.Fprintf(&b, "🕒 Fecha: %s (ART)\n", a.CreadaEn.In(artZone).Format("02/01/2006 15:04"))
	if panelURL != "" {
		fmt.Fprintf(&b, "🔗 Panel: %s/alertas\n", strings.TrimRight(panelURL, "/"))
	}
	texto = strings.TrimRight(b.String(), "\n")

	return asunto, texto, renderizarHTMLAlerta(a, tipo, emojiSev, emojiTipo, panelURL)
}

// renderizarHTMLAlerta arma un email table-based con estilos inline (Gmail y
// Outlook ignoran parte del CSS externo). Todos los valores dinámicos pasan por
// html.EscapeString.
func renderizarHTMLAlerta(a *alerta.Alerta, tipo, emojiSev, emojiTipo, panelURL string) string {
	headerColor, badgeBg := colorSeveridad(a.Severidad)

	var filas strings.Builder
	if a.SectorID != nil && *a.SectorID != "" {
		escribirFilaDetalle(&filas, "🏭 Sector", *a.SectorID)
	}
	if a.LoteID != nil && *a.LoteID != "" {
		escribirFilaDetalle(&filas, "🧾 Lote", *a.LoteID)
	}
	if a.HornoID != "" {
		escribirFilaDetalle(&filas, "🔥 Horno", a.HornoID)
	}
	escribirFilaDetalle(&filas, "🕒 Fecha", a.CreadaEn.In(artZone).Format("02/01/2006 15:04")+" (ART)")

	var cta string
	if panelURL != "" {
		url := html.EscapeString(strings.TrimRight(panelURL, "/") + "/alertas")
		cta = fmt.Sprintf(`
      <tr><td style="padding:8px 24px 24px 24px;">
        <a href="%s" style="display:inline-block;background:#111827;color:#ffffff;text-decoration:none;font-family:Arial,Helvetica,sans-serif;font-size:14px;font-weight:bold;padding:12px 20px;border-radius:6px;">Ver en el panel</a>
      </td></tr>`, url)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="es">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>%s</title>
</head>
<body style="margin:0;padding:0;background:#f3f4f6;">
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background:#f3f4f6;padding:24px 12px;">
    <tr><td align="center">
      <table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%%;max-width:600px;background:#ffffff;border-radius:8px;overflow:hidden;">
        <tr>
          <td style="background:%s;padding:20px 24px;color:#ffffff;font-family:Arial,Helvetica,sans-serif;">
            <div style="font-size:12px;letter-spacing:1px;text-transform:uppercase;opacity:0.9;">%s %s</div>
            <h1 style="margin:8px 0 0 0;font-size:20px;line-height:1.3;">%s %s</h1>
          </td>
        </tr>
        <tr>
          <td style="padding:24px 24px 8px 24px;font-family:Arial,Helvetica,sans-serif;">
            <div style="display:inline-block;background:%s;color:#ffffff;font-size:11px;font-weight:bold;padding:4px 10px;border-radius:999px;letter-spacing:0.5px;">%s</div>
            <div style="margin-top:16px;padding:14px 16px;background:#f9fafb;border-left:4px solid %s;border-radius:4px;color:#111827;font-size:14px;line-height:1.5;">%s</div>
          </td>
        </tr>
        <tr>
          <td style="padding:8px 24px 0 24px;">
            <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="font-family:Arial,Helvetica,sans-serif;font-size:14px;color:#374151;">
%s            </table>
          </td>
        </tr>
%s
        <tr>
          <td style="padding:16px 24px;background:#f9fafb;border-top:1px solid #e5e7eb;color:#6b7280;font-size:12px;font-family:Arial,Helvetica,sans-serif;">
            Notificación automática de SmartCheck Automation
          </td>
        </tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`,
		html.EscapeString(fmt.Sprintf("%s %s · %s", emojiSev, a.Severidad, tipo)),
		headerColor,
		emojiSev, html.EscapeString(a.Severidad),
		emojiTipo, html.EscapeString(tipo),
		badgeBg, html.EscapeString(a.Severidad),
		headerColor, html.EscapeString(a.Mensaje),
		filas.String(),
		cta,
	)
}

func escribirFilaDetalle(b *strings.Builder, etiqueta, valor string) {
	fmt.Fprintf(b, `              <tr><td style="padding:8px 0;border-bottom:1px solid #f3f4f6;width:120px;color:#6b7280;">%s</td><td style="padding:8px 0;border-bottom:1px solid #f3f4f6;color:#111827;word-break:break-word;">%s</td></tr>`+"\n",
		html.EscapeString(etiqueta), html.EscapeString(valor))
}

// colorSeveridad devuelve el color del header y del badge para la severidad.
func colorSeveridad(severidad string) (header, badge string) {
	switch severidad {
	case alerta.SeveridadCritical:
		return "#b91c1c", "#991b1b"
	case alerta.SeveridadWarning:
		return "#b45309", "#92400e"
	case alerta.SeveridadInfo:
		return "#1d4ed8", "#1e40af"
	default:
		return "#374151", "#1f2937"
	}
}

// renderizarPruebaEmail arma el email de prueba del canal (texto + HTML).
func renderizarPruebaEmail() (texto, htmlBody string) {
	texto = "✅ Este es un mensaje de prueba del canal de email de SmartCheck."
	htmlBody = `<!DOCTYPE html>
<html lang="es">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Notificación de prueba</title></head>
<body style="margin:0;padding:0;background:#f3f4f6;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#f3f4f6;padding:24px 12px;">
    <tr><td align="center">
      <table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%;max-width:600px;background:#ffffff;border-radius:8px;overflow:hidden;">
        <tr><td style="background:#15803d;padding:20px 24px;color:#ffffff;font-family:Arial,Helvetica,sans-serif;">
          <h1 style="margin:0;font-size:20px;">✅ Notificación de prueba</h1>
        </td></tr>
        <tr><td style="padding:24px;color:#111827;font-family:Arial,Helvetica,sans-serif;font-size:14px;line-height:1.5;">
          Este es un mensaje de prueba del canal de email de SmartCheck. Si lo recibiste, la configuración del proveedor es correcta.
        </td></tr>
        <tr><td style="padding:16px 24px;background:#f9fafb;border-top:1px solid #e5e7eb;color:#6b7280;font-size:12px;font-family:Arial,Helvetica,sans-serif;">
          Notificación automática de SmartCheck Automation
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`
	return texto, htmlBody
}
