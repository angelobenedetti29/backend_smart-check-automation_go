// Package notificacion arma, encola y envía las notificaciones de alertas
// WARNING/CRITICAL a los usuarios activos, con reintentos persistentes.
package notificacion

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	domain "github.com/angelobenedetti29/smart-check-automation/internal/domain/notificacion"
)

const (
	maxPendientesPorCiclo = 20
	envioTimeout          = 10 * time.Second
	telegramMaxCaracteres = 3500
)

var (
	chatIDPattern = regexp.MustCompile(`^-?[0-9]{5,20}$`)
	artZone       = time.FixedZone("ART", -3*60*60)
)

// Configuracion describe qué canales están configurados en el proceso.
type Configuracion struct {
	EmailConfigurado    bool
	TelegramConfigurado bool
	Remitente           string
}

// Service implementa el encolado de alertas, el worker de envío y la
// administración de preferencias.
type Service struct {
	repo      domain.Repository
	email     domain.EnviadorEmail
	telegram  domain.EnviadorTelegram
	panelURL  string
	emailFrom string
	alertas   UltimaAlertaProvider
}

// UltimaAlertaProvider devuelve la alerta real más reciente de un tipo, o nil si
// todavía no hay ninguna. Lo usa el envío de prueba para no inventar datos.
type UltimaAlertaProvider interface {
	UltimaPorTipo(ctx context.Context, tipo string) (*alerta.Alerta, error)
}

// SetAlertas configura el origen de alertas reales para el envío de prueba.
// Nil lo deshabilita y se usa el ejemplo.
func (s *Service) SetAlertas(p UltimaAlertaProvider) {
	s.alertas = p
}

// NewService construye el service de notificaciones. Un enviador nil deshabilita
// ese canal.
func NewService(repo domain.Repository, email domain.EnviadorEmail, telegram domain.EnviadorTelegram, panelURL, emailFrom string) *Service {
	return &Service{
		repo:      repo,
		email:     email,
		telegram:  telegram,
		panelURL:  panelURL,
		emailFrom: emailFrom,
	}
}

// EncolarParaAlerta encola una notificación por cada canal habilitado de cada
// usuario activo. Sólo procesa alertas WARNING/CRITICAL.
func (s *Service) EncolarParaAlerta(ctx context.Context, a *alerta.Alerta) error {
	if a == nil || a.ID == "" ||
		(a.Severidad != alerta.SeveridadWarning && a.Severidad != alerta.SeveridadCritical) {
		return nil
	}

	destinatarios, err := s.repo.ListarDestinatarios(ctx)
	if err != nil {
		return fmt.Errorf("listar destinatarios: %w", err)
	}

	asunto, cuerpo, cuerpoHTML := RenderizarAlerta(a, s.panelURL)
	cuerpoTelegram := truncar(cuerpo, telegramMaxCaracteres)

	items := make([]domain.Notificacion, 0, len(destinatarios))
	for _, d := range destinatarios {
		if !d.Activo {
			continue
		}
		if s.email != nil && d.NotificarEmail && strings.TrimSpace(d.Email) != "" {
			items = append(items, domain.Notificacion{
				AlertaID:   a.ID,
				UsuarioID:  d.UsuarioID,
				Canal:      domain.CanalEmail,
				Destino:    d.Email,
				Asunto:     asunto,
				Cuerpo:     cuerpo,
				CuerpoHTML: cuerpoHTML,
			})
		}
		if s.telegram != nil && d.NotificarTelegram && d.TelegramChatID != nil && strings.TrimSpace(*d.TelegramChatID) != "" {
			items = append(items, domain.Notificacion{
				AlertaID:  a.ID,
				UsuarioID: d.UsuarioID,
				Canal:     domain.CanalTelegram,
				Destino:   *d.TelegramChatID,
				Asunto:    asunto,
				Cuerpo:    cuerpoTelegram,
			})
		}
	}

	if len(items) == 0 {
		return nil
	}
	if err := s.repo.Encolar(ctx, items); err != nil {
		return fmt.Errorf("encolar notificaciones: %w", err)
	}
	return nil
}

// ListarDestinatarios devuelve todos los usuarios con sus preferencias.
func (s *Service) ListarDestinatarios(ctx context.Context) ([]domain.Destinatario, error) {
	return s.repo.ListarDestinatarios(ctx)
}

// StartWorker procesa el outbox cada `intervalo` hasta que el contexto se cancele.
func (s *Service) StartWorker(ctx context.Context, intervalo time.Duration, maxIntentos int) {
	if intervalo <= 0 {
		intervalo = 15 * time.Second
	}
	log.Printf("[NOTIFICACIONES] Worker iniciado: cada %s, máximo %d intentos", intervalo, maxIntentos)

	ticker := time.NewTicker(intervalo)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := s.ProcesarPendientes(ctx, maxIntentos); err != nil {
				log.Printf("[NOTIFICACIONES] Error al procesar outbox: %v", err)
			}
		case <-ctx.Done():
			log.Println("[NOTIFICACIONES] Worker detenido")
			return
		}
	}
}

// ProcesarPendientes toma hasta 20 notificaciones listas y las intenta enviar.
func (s *Service) ProcesarPendientes(ctx context.Context, maxIntentos int) error {
	if maxIntentos <= 0 {
		maxIntentos = 5
	}

	pendientes, err := s.repo.Pendientes(ctx, maxPendientesPorCiclo)
	if err != nil {
		return fmt.Errorf("obtener pendientes: %w", err)
	}
	for _, n := range pendientes {
		s.procesar(ctx, n, maxIntentos)
	}
	return nil
}

func (s *Service) procesar(ctx context.Context, n domain.Notificacion, maxIntentos int) {
	var err error
	switch n.Canal {
	case domain.CanalEmail:
		if s.email == nil {
			s.fallar(ctx, n, domain.ErrCanalNoConfigurado, maxIntentos, true)
			return
		}
		sendCtx, cancel := context.WithTimeout(ctx, envioTimeout)
		defer cancel()
		err = s.email.Enviar(sendCtx, n.Destino, n.Asunto, n.Cuerpo, n.CuerpoHTML)
	case domain.CanalTelegram:
		if s.telegram == nil {
			s.fallar(ctx, n, domain.ErrCanalNoConfigurado, maxIntentos, true)
			return
		}
		sendCtx, cancel := context.WithTimeout(ctx, envioTimeout)
		defer cancel()
		err = s.telegram.Enviar(sendCtx, n.Destino, n.Cuerpo)
	default:
		err = domain.ErrCanalNoConfigurado
	}

	if err == nil {
		if markErr := s.repo.MarcarEnviada(ctx, n.ID); markErr != nil {
			log.Printf("[NOTIFICACIONES] No se pudo marcar enviada %s: %v", n.ID, markErr)
		}
		return
	}
	s.fallar(ctx, n, err, maxIntentos, false)
}

func (s *Service) fallar(ctx context.Context, n domain.Notificacion, sendErr error, maxIntentos int, forzarAgotada bool) {
	intentos := n.Intentos + 1
	agotada := forzarAgotada || intentos >= maxIntentos
	proximo := time.Now().Add(backoff(intentos))
	if err := s.repo.MarcarFallida(ctx, n.ID, sendErr.Error(), proximo, agotada); err != nil {
		log.Printf("[NOTIFICACIONES] No se pudo marcar fallida %s: %v", n.ID, err)
	}
	log.Printf("[NOTIFICACIONES] Envío fallido canal=%s notificacion=%s intento=%d: %v", n.Canal, n.ID, intentos, sendErr)
}

// backoff aplica 2^intentos minutos, con techo de 60 minutos.
func backoff(intentos int) time.Duration {
	if intentos >= 6 {
		return 60 * time.Minute
	}
	return time.Duration(1<<uint(intentos)) * time.Minute
}

// ActualizarPreferencias aplica un patch parcial sobre las preferencias actuales.
func (s *Service) ActualizarPreferencias(ctx context.Context, usuarioID string, patch domain.PreferenciasPatch) (*domain.Destinatario, error) {
	actual, err := s.repo.ObtenerDestinatario(ctx, usuarioID)
	if err != nil {
		return nil, err
	}

	pref := domain.Preferencias{
		UsuarioID:         actual.UsuarioID,
		TelegramChatID:    actual.TelegramChatID,
		NotificarEmail:    actual.NotificarEmail,
		NotificarTelegram: actual.NotificarTelegram,
	}

	if patch.TelegramChatID != nil {
		chatID := strings.TrimSpace(*patch.TelegramChatID)
		if chatID == "" {
			pref.TelegramChatID = nil
		} else {
			if !chatIDPattern.MatchString(chatID) {
				return nil, domain.ErrChatIDInvalido
			}
			pref.TelegramChatID = &chatID
		}
	}
	if patch.NotificarEmail != nil {
		pref.NotificarEmail = *patch.NotificarEmail
	}
	if patch.NotificarTelegram != nil {
		pref.NotificarTelegram = *patch.NotificarTelegram
	}

	return s.repo.GuardarPreferencias(ctx, pref)
}

// Probar envía un mensaje de prueba directo por el canal indicado.
func (s *Service) Probar(ctx context.Context, usuarioID string, canal domain.Canal) error {
	d, err := s.repo.ObtenerDestinatario(ctx, usuarioID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, envioTimeout)
	defer cancel()

	switch canal {
	case domain.CanalEmail:
		if s.email == nil {
			return domain.ErrCanalNoConfigurado
		}
		if strings.TrimSpace(d.Email) == "" {
			return domain.ErrSinDestino
		}
		texto, htmlBody := renderizarPruebaEmail()
		return s.email.Enviar(ctx, d.Email, "[SmartCheck] ✅ Notificación de prueba", texto, htmlBody)
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
		return s.telegram.Enviar(ctx, chatID,
			"✅ SmartCheck: este es un mensaje de prueba del canal de Telegram.")
	default:
		return domain.ErrCanalNoConfigurado
	}
}

// Configuracion informa qué canales están habilitados en el proceso.
func (s *Service) Configuracion() Configuracion {
	remitente := ""
	if s.email != nil {
		remitente = s.emailFrom
	}
	return Configuracion{
		EmailConfigurado:    s.email != nil,
		TelegramConfigurado: s.telegram != nil,
		Remitente:           remitente,
	}
}

// emojiSeveridad devuelve el emoji que acompaña la severidad en la notificación.
func emojiSeveridad(severidad string) string {
	switch severidad {
	case alerta.SeveridadCritical:
		return "🚨"
	case alerta.SeveridadWarning:
		return "⚠️"
	case alerta.SeveridadInfo:
		return "ℹ️"
	default:
		return "🔔"
	}
}

// emojiTipoAlerta devuelve el emoji que identifica el tipo de alerta.
func emojiTipoAlerta(tipo string) string {
	switch tipo {
	case alerta.TipoTemperaturaHorno:
		return "🌡️"
	case alerta.TipoPrecalentamientoTimeout:
		return "⏱️"
	case alerta.TipoPorcentajeQuemadosAlto:
		return "🔥"
	case alerta.TipoPorcentajeCrudosAlto:
		return "🥖"
	case alerta.TipoDispositivoOffline:
		return "📴"
	case alerta.TipoTelemetriaDesactualizada:
		return "📡"
	default:
		return "🔔"
	}
}

func tipoLegible(tipo string) string {
	switch tipo {
	case alerta.TipoTemperaturaHorno:
		return "Temperatura de horno"
	case alerta.TipoPrecalentamientoTimeout:
		return "Precalentamiento agotado"
	case alerta.TipoPorcentajeQuemadosAlto:
		return "Alto porcentaje de quemados"
	case alerta.TipoPorcentajeCrudosAlto:
		return "Alto porcentaje de crudos"
	case alerta.TipoDispositivoOffline:
		return "Dispositivo offline"
	case alerta.TipoTelemetriaDesactualizada:
		return "Telemetría desactualizada"
	default:
		return tipo
	}
}

func truncar(s string, max int) string {
	runas := []rune(s)
	if len(runas) <= max {
		return s
	}
	return string(runas[:max])
}
