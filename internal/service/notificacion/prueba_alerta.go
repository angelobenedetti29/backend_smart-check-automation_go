package notificacion

import (
	"context"
	"strings"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	domain "github.com/angelobenedetti29/smart-check-automation/internal/domain/notificacion"
)

// TiposAlertaPrueba lista los tipos de alerta que ProbarAlerta sabe simular.
func TiposAlertaPrueba() []string {
	return []string{
		alerta.TipoTemperaturaHorno,
		alerta.TipoPrecalentamientoTimeout,
		alerta.TipoPorcentajeQuemadosAlto,
		alerta.TipoPorcentajeCrudosAlto,
		alerta.TipoDispositivoOffline,
		alerta.TipoTelemetriaDesactualizada,
	}
}

// alertaEjemplo arma una alerta ficticia (no se persiste) con la severidad real
// del tipo y un mensaje marcado como prueba.
func alertaEjemplo(tipo string) (*alerta.Alerta, bool) {
	sector := "horno-1"
	lote := "00000000-0000-0000-0000-000000000000"
	a := &alerta.Alerta{
		ID:        "prueba",
		SectorID:  &sector,
		Tipo:      tipo,
		Severidad: alerta.SeveridadWarning,
		Estado:    alerta.EstadoActiva,
		CreadaEn:  time.Now(),
	}
	switch tipo {
	case alerta.TipoTemperaturaHorno:
		a.LoteID = &lote
		a.Mensaje = "[PRUEBA] Temperatura T1 en 214.0 °C, objetivo 220.0 °C (tolerancia 2.0 °C)"
	case alerta.TipoPrecalentamientoTimeout:
		a.LoteID = &lote
		a.Severidad = alerta.SeveridadCritical
		a.Mensaje = "[PRUEBA] El horno no alcanzó la temperatura objetivo tras 30 minutos de precalentamiento"
	case alerta.TipoPorcentajeQuemadosAlto:
		a.LoteID = &lote
		a.Mensaje = "[PRUEBA] Porcentaje de quemados alto: 8.4 % en la ventana de 10s"
	case alerta.TipoPorcentajeCrudosAlto:
		a.LoteID = &lote
		a.Mensaje = "[PRUEBA] Porcentaje de crudos alto: 7.1 % en la ventana de 10s"
	case alerta.TipoDispositivoOffline:
		a.Mensaje = "[PRUEBA] El dispositivo raspberry-entrada dejó de reportar hace más de 90 s"
	case alerta.TipoTelemetriaDesactualizada:
		a.LoteID = &lote
		a.Mensaje = "[PRUEBA] El lote abierto lleva más de 30 s sin muestras de telemetría del horno"
	default:
		return nil, false
	}
	return a, true
}

// ProbarAlerta envía al usuario, por el canal indicado, el mensaje de la última
// alerta real de ese tipo (o datos de ejemplo si nunca se generó una). No crea
// ninguna alerta ni outbox.
func (s *Service) ProbarAlerta(ctx context.Context, usuarioID string, canal domain.Canal, tipo string) error {
	tipo = strings.ToUpper(strings.TrimSpace(tipo))
	a, ok := alertaEjemplo(tipo)
	if !ok {
		return domain.ErrTipoAlertaInvalido
	}
	// Preferimos la última alerta real del tipo; el ejemplo es solo fallback.
	if s.alertas != nil {
		if real, err := s.alertas.UltimaPorTipo(ctx, tipo); err == nil && real != nil {
			a = real
		}
	}
	d, err := s.repo.ObtenerDestinatario(ctx, usuarioID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, envioTimeout)
	defer cancel()

	asunto, texto, htmlBody := RenderizarAlerta(a, s.panelURL)
	switch canal {
	case domain.CanalEmail:
		if s.email == nil {
			return domain.ErrCanalNoConfigurado
		}
		if strings.TrimSpace(d.Email) == "" {
			return domain.ErrSinDestino
		}
		return s.email.Enviar(ctx, d.Email, asunto, texto, htmlBody)
	case domain.CanalTelegram:
		if s.telegram == nil {
			return domain.ErrCanalNoConfigurado
		}
		if d.TelegramChatID == nil || strings.TrimSpace(*d.TelegramChatID) == "" {
			return domain.ErrSinDestino
		}
		chatID := strings.TrimSpace(*d.TelegramChatID)
		if !chatIDPattern.MatchString(chatID) {
			return domain.ErrChatIDInvalido
		}
		return s.telegram.Enviar(ctx, chatID, truncar(texto, telegramMaxCaracteres))
	default:
		return domain.ErrCanalNoConfigurado
	}
}
