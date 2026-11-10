package notificacion

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/alerta"
	domain "github.com/angelobenedetti29/smart-check-automation/internal/domain/notificacion"
)

type fakeRepo struct {
	destinatarios    []domain.Destinatario
	destinatariosErr error
	encoladas        []domain.Notificacion
	encolarErr       error
	pendientes       []domain.Notificacion
	pendientesErr    error
	enviadas         []string
	fallidas         []fallidaCall
	preferencias     *domain.Preferencias
}

type fallidaCall struct {
	id      string
	err     string
	proximo time.Time
	agotada bool
}

func (f *fakeRepo) ListarDestinatarios(ctx context.Context) ([]domain.Destinatario, error) {
	return f.destinatarios, f.destinatariosErr
}

func (f *fakeRepo) ObtenerDestinatario(ctx context.Context, usuarioID string) (*domain.Destinatario, error) {
	for i := range f.destinatarios {
		if f.destinatarios[i].UsuarioID == usuarioID {
			d := f.destinatarios[i]
			return &d, nil
		}
	}
	return nil, domain.ErrDestinatarioNoEncontrado
}

func (f *fakeRepo) GuardarPreferencias(ctx context.Context, p domain.Preferencias) (*domain.Destinatario, error) {
	f.preferencias = &p
	for i := range f.destinatarios {
		if f.destinatarios[i].UsuarioID == p.UsuarioID {
			f.destinatarios[i].TelegramChatID = p.TelegramChatID
			f.destinatarios[i].NotificarEmail = p.NotificarEmail
			f.destinatarios[i].NotificarTelegram = p.NotificarTelegram
			d := f.destinatarios[i]
			return &d, nil
		}
	}
	return &domain.Destinatario{
		UsuarioID:         p.UsuarioID,
		TelegramChatID:    p.TelegramChatID,
		NotificarEmail:    p.NotificarEmail,
		NotificarTelegram: p.NotificarTelegram,
	}, nil
}

func (f *fakeRepo) Encolar(ctx context.Context, ns []domain.Notificacion) error {
	f.encoladas = append(f.encoladas, ns...)
	return f.encolarErr
}

func (f *fakeRepo) Pendientes(ctx context.Context, limite int) ([]domain.Notificacion, error) {
	return f.pendientes, f.pendientesErr
}

func (f *fakeRepo) MarcarEnviada(ctx context.Context, id string) error {
	f.enviadas = append(f.enviadas, id)
	return nil
}

func (f *fakeRepo) MarcarFallida(ctx context.Context, id, ultimoError string, proximo time.Time, agotada bool) error {
	f.fallidas = append(f.fallidas, fallidaCall{id: id, err: ultimoError, proximo: proximo, agotada: agotada})
	return nil
}

type fakeEmail struct {
	calls   int
	destino string
	asunto  string
	cuerpo  string
	html    string
	err     error
}

func (f *fakeEmail) Enviar(ctx context.Context, destino, asunto, cuerpo, htmlBody string) error {
	f.calls++
	f.destino, f.asunto, f.cuerpo, f.html = destino, asunto, cuerpo, htmlBody
	return f.err
}

type fakeTelegram struct {
	calls  int
	chatID string
	texto  string
	err    error
}

func (f *fakeTelegram) Enviar(ctx context.Context, chatID, texto string) error {
	f.calls++
	f.chatID, f.texto = chatID, texto
	return f.err
}

func chatPtr(s string) *string { return &s }

func alertaPrueba(severidad string) *alerta.Alerta {
	sector := "linea-a"
	lote := "11111111-1111-1111-1111-111111111111"
	return &alerta.Alerta{
		ID:        "22222222-2222-2222-2222-222222222222",
		SectorID:  &sector,
		LoteID:    &lote,
		HornoID:   "linea-a",
		Tipo:      alerta.TipoPrecalentamientoTimeout,
		Severidad: severidad,
		Estado:    alerta.EstadoActiva,
		Mensaje:   "Precalentamiento agotado tras 1800s",
		CreadaEn:  time.Date(2026, 11, 6, 17, 3, 0, 0, time.UTC),
	}
}

func TestEncolarParaAlerta_SeveridadYPreferencias(t *testing.T) {
	repo := &fakeRepo{destinatarios: []domain.Destinatario{
		{UsuarioID: "u1", Email: "a@x.com", Nombre: "Administrador", Rol: "Administrador", Activo: true,
			TelegramChatID: chatPtr("123456"), NotificarEmail: true, NotificarTelegram: true},
		{UsuarioID: "u2", Email: "b@x.com", Activo: false, NotificarEmail: true, NotificarTelegram: true},
		{UsuarioID: "u3", Email: "c@x.com", Activo: true, NotificarEmail: false, NotificarTelegram: true},
		{UsuarioID: "u4", Email: "d@x.com", Activo: true, TelegramChatID: nil, NotificarEmail: true, NotificarTelegram: true},
	}}
	svc := NewService(repo, &fakeEmail{}, &fakeTelegram{}, "https://panel.test/", "")

	// INFO no genera notificaciones.
	require.NoError(t, svc.EncolarParaAlerta(context.Background(), alertaPrueba(alerta.SeveridadInfo)))
	require.Empty(t, repo.encoladas)

	require.NoError(t, svc.EncolarParaAlerta(context.Background(), alertaPrueba(alerta.SeveridadWarning)))
	require.Len(t, repo.encoladas, 3, "sólo usuarios activos y con canal configurado")

	var email, telegram *domain.Notificacion
	for i := range repo.encoladas {
		n := &repo.encoladas[i]
		switch {
		case n.UsuarioID == "u1" && n.Canal == domain.CanalEmail:
			email = n
		case n.UsuarioID == "u1" && n.Canal == domain.CanalTelegram:
			telegram = n
		}
	}
	require.NotNil(t, email)
	require.NotNil(t, telegram)
	require.Equal(t, "[SmartCheck] ⚠️ WARNING · Precalentamiento agotado", email.Asunto)
	require.Contains(t, email.Cuerpo, "⚠️ Severidad: WARNING")
	require.Contains(t, email.Cuerpo, "Sector: linea-a")
	require.Contains(t, email.Cuerpo, "https://panel.test/alertas")
	require.Contains(t, email.Cuerpo, "06/11/2026 14:03 (ART)")
	require.Equal(t, "123456", telegram.Destino)
	require.Equal(t, email.Cuerpo, telegram.Cuerpo)
	require.Contains(t, email.CuerpoHTML, "<!DOCTYPE html>")
	require.Contains(t, email.CuerpoHTML, "Ver en el panel")
	require.Empty(t, telegram.CuerpoHTML)
}

func TestEncolarParaAlerta_OmiteCanalesNoConfigurados(t *testing.T) {
	repo := &fakeRepo{destinatarios: []domain.Destinatario{{
		UsuarioID: "u1", Email: "a@x.com", Activo: true,
		TelegramChatID: chatPtr("123456"), NotificarEmail: true, NotificarTelegram: true,
	}}}
	svc := NewService(repo, nil, nil, "", "")

	require.NoError(t, svc.EncolarParaAlerta(context.Background(), alertaPrueba(alerta.SeveridadWarning)))
	require.Empty(t, repo.encoladas)
}

func TestEncolarParaAlerta_TelegramTruncado(t *testing.T) {
	repo := &fakeRepo{destinatarios: []domain.Destinatario{{
		UsuarioID: "u1", Email: "a@x.com", Activo: true,
		TelegramChatID: chatPtr("123456"), NotificarEmail: false, NotificarTelegram: true,
	}}}
	svc := NewService(repo, nil, &fakeTelegram{}, "", "")
	a := alertaPrueba(alerta.SeveridadCritical)
	a.Mensaje = strings.Repeat("x", 5000)

	require.NoError(t, svc.EncolarParaAlerta(context.Background(), a))
	require.Len(t, repo.encoladas, 1)
	require.LessOrEqual(t, len([]rune(repo.encoladas[0].Cuerpo)), telegramMaxCaracteres)
}

func TestProcesarPendientes_ExitoYBackoff(t *testing.T) {
	repo := &fakeRepo{pendientes: []domain.Notificacion{
		{ID: "n1", Canal: domain.CanalEmail, Destino: "a@x.com", Asunto: "asunto", Cuerpo: "cuerpo"},
		{ID: "n2", Canal: domain.CanalTelegram, Destino: "123456", Cuerpo: "texto", Intentos: 0},
	}}
	email := &fakeEmail{}
	telegram := &fakeTelegram{err: errors.New("boom")}
	svc := NewService(repo, email, telegram, "", "")

	require.NoError(t, svc.ProcesarPendientes(context.Background(), 5))
	require.Equal(t, []string{"n1"}, repo.enviadas)
	require.Len(t, repo.fallidas, 1)
	require.Equal(t, "n2", repo.fallidas[0].id)
	require.False(t, repo.fallidas[0].agotada)
	require.InDelta(t, (2 * time.Minute).Seconds(), time.Until(repo.fallidas[0].proximo).Seconds(), 5)
}

func TestProcesarPendientes_AgotaIntentos(t *testing.T) {
	repo := &fakeRepo{pendientes: []domain.Notificacion{
		{ID: "n1", Canal: domain.CanalEmail, Destino: "a@x.com", Intentos: 4},
	}}
	svc := NewService(repo, &fakeEmail{err: errors.New("boom")}, nil, "", "")

	require.NoError(t, svc.ProcesarPendientes(context.Background(), 5))
	require.Len(t, repo.fallidas, 1)
	require.True(t, repo.fallidas[0].agotada)
	require.InDelta(t, (32 * time.Minute).Seconds(), time.Until(repo.fallidas[0].proximo).Seconds(), 5)
}

func TestProcesarPendientes_CanalNoConfigurado(t *testing.T) {
	repo := &fakeRepo{pendientes: []domain.Notificacion{
		{ID: "n1", Canal: domain.CanalTelegram, Destino: "123456"},
	}}
	svc := NewService(repo, nil, nil, "", "")

	require.NoError(t, svc.ProcesarPendientes(context.Background(), 5))
	require.Len(t, repo.fallidas, 1)
	require.True(t, repo.fallidas[0].agotada)
	require.Contains(t, repo.fallidas[0].err, "no configurado")
}

func TestActualizarPreferencias_MergeYValidacion(t *testing.T) {
	repo := &fakeRepo{destinatarios: []domain.Destinatario{{
		UsuarioID: "u1", Email: "a@x.com", Activo: true,
		NotificarEmail: true, NotificarTelegram: true,
	}}}
	svc := NewService(repo, nil, nil, "", "")

	_, err := svc.ActualizarPreferencias(context.Background(), "u1", domain.PreferenciasPatch{
		TelegramChatID: chatPtr("abc"),
	})
	require.ErrorIs(t, err, domain.ErrChatIDInvalido)

	d, err := svc.ActualizarPreferencias(context.Background(), "u1", domain.PreferenciasPatch{
		TelegramChatID:    chatPtr(" -1001234567890 "),
		NotificarEmail:    boolPtr(false),
		NotificarTelegram: boolPtr(false),
	})
	require.NoError(t, err)
	require.Equal(t, "-1001234567890", *d.TelegramChatID)
	require.False(t, d.NotificarEmail)
	require.False(t, d.NotificarTelegram)

	d, err = svc.ActualizarPreferencias(context.Background(), "u1", domain.PreferenciasPatch{
		TelegramChatID: chatPtr(""),
	})
	require.NoError(t, err)
	require.Nil(t, d.TelegramChatID)
	require.False(t, d.NotificarEmail, "los campos no enviados conservan su valor")

	_, err = svc.ActualizarPreferencias(context.Background(), "desconocido", domain.PreferenciasPatch{})
	require.ErrorIs(t, err, domain.ErrDestinatarioNoEncontrado)
}

func boolPtr(b bool) *bool { return &b }

func TestProbar(t *testing.T) {
	repo := &fakeRepo{destinatarios: []domain.Destinatario{
		{UsuarioID: "u1", Email: "a@x.com", Activo: true, TelegramChatID: chatPtr("123456")},
		{UsuarioID: "u2", Email: "b@x.com", Activo: true},
		{UsuarioID: "u3", Email: "", Activo: true, TelegramChatID: chatPtr("no-valido")},
	}}
	email := &fakeEmail{}
	telegram := &fakeTelegram{}
	svc := NewService(repo, email, telegram, "", "")

	require.NoError(t, svc.Probar(context.Background(), "u1", domain.CanalEmail))
	require.Equal(t, 1, email.calls)
	require.Equal(t, "a@x.com", email.destino)
	require.Contains(t, email.cuerpo, "mensaje de prueba")
	require.Contains(t, email.html, "#15803d")
	require.Contains(t, email.html, "✅ Notificación de prueba")

	require.NoError(t, svc.Probar(context.Background(), "u1", domain.CanalTelegram))
	require.Equal(t, 1, telegram.calls)

	require.ErrorIs(t, svc.Probar(context.Background(), "u2", domain.CanalTelegram), domain.ErrSinDestino)
	require.ErrorIs(t, svc.Probar(context.Background(), "u3", domain.CanalTelegram), domain.ErrChatIDInvalido)
	require.ErrorIs(t, svc.Probar(context.Background(), "desconocido", domain.CanalEmail), domain.ErrDestinatarioNoEncontrado)
	require.ErrorIs(t, svc.Probar(context.Background(), "u1", domain.Canal("OTRO")), domain.ErrCanalNoConfigurado)

	sinCanales := NewService(repo, nil, nil, "", "")
	require.ErrorIs(t, sinCanales.Probar(context.Background(), "u1", domain.CanalEmail), domain.ErrCanalNoConfigurado)
	require.ErrorIs(t, sinCanales.Probar(context.Background(), "u1", domain.CanalTelegram), domain.ErrCanalNoConfigurado)
}

func TestConfiguracion(t *testing.T) {
	svc := NewService(&fakeRepo{}, &fakeEmail{}, nil, "", "alertas@fermar.com.ar")
	cfg := svc.Configuracion()
	require.True(t, cfg.EmailConfigurado)
	require.False(t, cfg.TelegramConfigurado)
	require.Equal(t, "alertas@fermar.com.ar", cfg.Remitente)
}

func TestBackoff(t *testing.T) {
	require.Equal(t, 2*time.Minute, backoff(1))
	require.Equal(t, 32*time.Minute, backoff(5))
	require.Equal(t, 60*time.Minute, backoff(6))
	require.Equal(t, 60*time.Minute, backoff(20))
}
