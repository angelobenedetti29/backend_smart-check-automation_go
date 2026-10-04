package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/control"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/optimizador"
	"github.com/angelobenedetti29/smart-check-automation/internal/sse"
)

// fakeLoteLookup implementa LoteLookupService para los tests del handler.
type fakeLoteLookup struct {
	lote *lotesector.Lote
	err  error
}

func (f *fakeLoteLookup) GetByID(_ context.Context, _ string) (*lotesector.Lote, error) {
	return f.lote, f.err
}

// fakeProductoRepo implementa ProductoRepo para los tests del handler.
type fakeProductoRepo struct {
	prod *producto.Producto
	err  error
}

func (f *fakeProductoRepo) GetByID(_ context.Context, _ string) (*producto.Producto, error) {
	return f.prod, f.err
}

// fakeDecisionesRepo implementa decisiones_control.Repository con resultados
// controlables por el test.
type fakeDecisionesRepo struct {
	pendiente *decisiones_control.DecisionControl
	pendErr   error
}

func (f *fakeDecisionesRepo) Save(_ context.Context, _ *decisiones_control.DecisionControl) error {
	return nil
}

func (f *fakeDecisionesRepo) ListByLote(_ context.Context, _ string, _ int) ([]decisiones_control.DecisionControl, error) {
	return nil, nil
}

func (f *fakeDecisionesRepo) GetUltimaPendiente(_ context.Context, _ string) (*decisiones_control.DecisionControl, error) {
	return f.pendiente, f.pendErr
}

func (f *fakeDecisionesRepo) MarcarAplicada(_ context.Context, _ string, _, _ float64, _ string) error {
	return nil
}

// fakeConsignaDispatch implementa control.ConsignaDispatchService.
type fakeConsignaDispatch struct{}

func (f *fakeConsignaDispatch) Dispatch(_ context.Context, _ consigna.ConsignaManualRequest, _ consigna.Origen, _ *string) (*consigna.Consigna, error) {
	return nil, nil
}

// fakeMetricasRepo implementa MetricasRepo.
type fakeMetricasRepo struct{}

func (f *fakeMetricasRepo) GetUltimasParaLote(_ context.Context, _ string, _ int) ([]metricashorno.MetricaHorno, error) {
	return nil, nil
}

// fakeEventosRepoHandler implementa EventosRepo.
type fakeEventosRepoHandler struct{}

func (f *fakeEventosRepoHandler) CountEventosEnVentana(_ context.Context, _ string, _, _ time.Time) (int, int, int, error) {
	return 0, 0, 0, nil
}

// validControlDeps arma un set completo (todas no-nil) de deps del handler.
func validControlDeps() ControlDeps {
	return ControlDeps{
		LoteLookup:     &fakeLoteLookup{},
		ProductoRepo:   &fakeProductoRepo{},
		Optimizer:      optimizador.NewOptimizer(nil),
		Consigna:       &fakeConsignaDispatch{},
		LoopCancels:    NewLoopRegistry(),
		MetricasRepo:   &fakeMetricasRepo{},
		EventosRepo:    &fakeEventosRepoHandler{},
		DecisionesRepo: &fakeDecisionesRepo{},
		Broker:         sse.NewBroker(),
	}
}

// TestNewControlHandlerPanicConDepNil verifica que cada dep requerida nil
// dispara un panic con el nombre del campo.
func TestNewControlHandlerPanicConDepNil(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*ControlDeps)
	}{
		{"LoteLookup", func(d *ControlDeps) { d.LoteLookup = nil }},
		{"ProductoRepo", func(d *ControlDeps) { d.ProductoRepo = nil }},
		{"Optimizer", func(d *ControlDeps) { d.Optimizer = nil }},
		{"Consigna", func(d *ControlDeps) { d.Consigna = nil }},
		{"LoopCancels", func(d *ControlDeps) { d.LoopCancels = nil }},
		{"MetricasRepo", func(d *ControlDeps) { d.MetricasRepo = nil }},
		{"EventosRepo", func(d *ControlDeps) { d.EventosRepo = nil }},
		{"DecisionesRepo", func(d *ControlDeps) { d.DecisionesRepo = nil }},
		{"Broker", func(d *ControlDeps) { d.Broker = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := validControlDeps()
			tc.clear(&deps)
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("se esperaba panic por dep %s nil", tc.name)
				}
				if !strings.Contains(fmt.Sprint(r), tc.name) {
					t.Fatalf("el panic %v no menciona %q", r, tc.name)
				}
			}()
			NewControlHandler(deps)
		})
	}
}

// fakeLoop implementa la vista mínima propuestaProvider del loop.
type fakeLoop struct {
	snap    *control.PropuestaSnapshot
	lastTTL int
}

func (f *fakeLoop) PropuestaPendiente(ttl int) *control.PropuestaSnapshot {
	f.lastTTL = ttl
	if f.snap == nil {
		return nil
	}
	cp := *f.snap
	cp.TTLSegundos = ttl
	return &cp
}

// consignaEnvelope modela el envelope estándar de pkg/response.
type consignaEnvelope struct {
	Success bool                      `json:"success"`
	Data    consignaPropuestaResponse `json:"data"`
}

// TestHandleConsignaPropuestaSinLoopSinDecisionDevuelve204 verifica el caso
// vacío: sin loop y sin decisión pendiente en DB.
func TestHandleConsignaPropuestaSinLoopSinDecisionDevuelve204(t *testing.T) {
	h := &ControlHandler{
		registry:       NewLoopRegistry(),
		decisionesRepo: &fakeDecisionesRepo{pendiente: nil},
		loteLookup:     &fakeLoteLookup{lote: &lotesector.Lote{VentanaActualizacionSegundos: 30}},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes/lote-1/consigna-propuesta", nil)
	h.HandleConsignaPropuesta(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleConsignaPropuestaConLoopConPropuestaDevuelve200 verifica que una
// propuesta en memoria se sirve con 200 y los campos esperados.
func TestHandleConsignaPropuestaConLoopConPropuestaDevuelve200(t *testing.T) {
	t2 := 170.0
	snap := &control.PropuestaSnapshot{
		DecisionID:    "dec-9",
		Temp1:         180,
		Temp2:         &t2,
		Velocidad:     0.45,
		VentanaInicio: time.Unix(100, 0).UTC(),
		VentanaFin:    time.Unix(200, 0).UTC(),
		CreatedAt:     time.Unix(200, 0).UTC(),
	}
	fl := &fakeLoop{snap: snap}
	reg := NewLoopRegistry()
	reg.SetLoop("lote-1", fl)
	h := &ControlHandler{
		registry:       reg,
		decisionesRepo: &fakeDecisionesRepo{},
		loteLookup:     &fakeLoteLookup{lote: &lotesector.Lote{VentanaActualizacionSegundos: 30}},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes/lote-1/consigna-propuesta", nil)
	h.HandleConsignaPropuesta(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var env consignaEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !env.Success {
		t.Fatalf("se esperaba success=true")
	}
	if env.Data.Temp1Objetivo != snap.Temp1 {
		t.Fatalf("temp1_objetivo = %v, want %v", env.Data.Temp1Objetivo, snap.Temp1)
	}
	if env.Data.VelObjetivo != snap.Velocidad {
		t.Fatalf("vel_objetivo = %v, want %v", env.Data.VelObjetivo, snap.Velocidad)
	}
	if env.Data.DecisionID != snap.DecisionID {
		t.Fatalf("decision_id = %q, want %q", env.Data.DecisionID, snap.DecisionID)
	}
	if env.Data.TTLSegundos != 30 {
		t.Fatalf("ttl_segundos = %d, want 30", env.Data.TTLSegundos)
	}
	if !env.Data.Temp2Disponible || env.Data.Temp2Objetivo != 170 {
		t.Fatalf("temp2 mal propagado: disponible=%v valor=%v", env.Data.Temp2Disponible, env.Data.Temp2Objetivo)
	}
	if fl.lastTTL != 30 {
		t.Fatalf("el loop no recibió el TTL del lote; got=%d", fl.lastTTL)
	}
}

// TestHandleConsignaPropuestaConLoopVacioFallbackDB verifica que un loop sin
// propuesta cae al fallback de la última decisión pendiente persistida.
func TestHandleConsignaPropuestaConLoopVacioFallbackDB(t *testing.T) {
	prediccion, _ := json.Marshal(map[string]any{"temp2_pred": 160.0})
	reg := NewLoopRegistry()
	reg.SetLoop("lote-1", &fakeLoop{snap: nil})
	repo := &fakeDecisionesRepo{pendiente: &decisiones_control.DecisionControl{
		ID:            "dec-db",
		TempPropuesto: 175,
		VelPropuesto:  0.5,
		Prediccion:    prediccion,
		VentanaInicio: time.Unix(300, 0).UTC(),
		VentanaFin:    time.Unix(400, 0).UTC(),
		CreatedAt:     time.Now().UTC(),
	}}
	h := &ControlHandler{
		registry:       reg,
		decisionesRepo: repo,
		loteLookup: &fakeLoteLookup{lote: &lotesector.Lote{
			Estado:                       lotesector.EstadoAbierto,
			Modo:                         lotesector.ModoSupervision,
			VentanaActualizacionSegundos: 45,
		}},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes/lote-1/consigna-propuesta", nil)
	h.HandleConsignaPropuesta(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var env consignaEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Data.DecisionID != "dec-db" {
		t.Fatalf("decision_id = %q, want dec-db", env.Data.DecisionID)
	}
	if env.Data.Temp1Objetivo != 175 || env.Data.VelObjetivo != 0.5 {
		t.Fatalf("datos de DB mal propagados: %+v", env.Data)
	}
	if !env.Data.Temp2Disponible || env.Data.Temp2Objetivo != 160 {
		t.Fatalf("temp2 de DB mal propagado: disponible=%v valor=%v", env.Data.Temp2Disponible, env.Data.Temp2Objetivo)
	}
	if env.Data.TTLSegundos != 45 {
		t.Fatalf("ttl_segundos = %d, want 45", env.Data.TTLSegundos)
	}
}

// TestHandleConsignaPropuestaFallbackRechazaLoteCerrado verifica que el
// fallback no sirve una decisión NADIE cuando el lote ya está cerrado.
func TestHandleConsignaPropuestaFallbackRechazaLoteCerrado(t *testing.T) {
	repo := &fakeDecisionesRepo{pendiente: &decisiones_control.DecisionControl{
		ID:            "dec-cerrado",
		TempPropuesto: 180,
		VelPropuesto:  0.4,
		CreatedAt:     time.Now().UTC(),
	}}
	h := &ControlHandler{
		registry:       NewLoopRegistry(),
		decisionesRepo: repo,
		loteLookup: &fakeLoteLookup{lote: &lotesector.Lote{
			Estado:                       lotesector.EstadoCerrado,
			Modo:                         lotesector.ModoSupervision,
			VentanaActualizacionSegundos: 60,
		}},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes/lote-1/consigna-propuesta", nil)
	h.HandleConsignaPropuesta(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleConsignaPropuestaFallbackRechazaModoManual verifica que el
// fallback no sirve una decisión NADIE cuando el lote pasó a MANUAL.
func TestHandleConsignaPropuestaFallbackRechazaModoManual(t *testing.T) {
	repo := &fakeDecisionesRepo{pendiente: &decisiones_control.DecisionControl{
		ID:            "dec-manual",
		TempPropuesto: 180,
		VelPropuesto:  0.4,
		CreatedAt:     time.Now().UTC(),
	}}
	h := &ControlHandler{
		registry:       NewLoopRegistry(),
		decisionesRepo: repo,
		loteLookup: &fakeLoteLookup{lote: &lotesector.Lote{
			Estado:                       lotesector.EstadoAbierto,
			Modo:                         lotesector.ModoManual,
			VentanaActualizacionSegundos: 60,
		}},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes/lote-1/consigna-propuesta", nil)
	h.HandleConsignaPropuesta(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleConsignaPropuestaFallbackRespetaTTL verifica que una decisión
// pendiente más vieja que el TTL del lote no se sirve por el fallback.
func TestHandleConsignaPropuestaFallbackRespetaTTL(t *testing.T) {
	repo := &fakeDecisionesRepo{pendiente: &decisiones_control.DecisionControl{
		ID:            "dec-vieja",
		TempPropuesto: 180,
		VelPropuesto:  0.4,
		CreatedAt:     time.Now().UTC().Add(-10 * time.Minute),
	}}
	h := &ControlHandler{
		registry:       NewLoopRegistry(),
		decisionesRepo: repo,
		loteLookup: &fakeLoteLookup{lote: &lotesector.Lote{
			Estado:                       lotesector.EstadoAbierto,
			Modo:                         lotesector.ModoSupervision,
			VentanaActualizacionSegundos: 60,
		}},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes/lote-1/consigna-propuesta", nil)
	h.HandleConsignaPropuesta(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// TestLoopRegistryStopAllCancelaTodos verifica que StopAll cancela todos los
// contextos registrados y limpia la registry.
func TestLoopRegistryStopAllCancelaTodos(t *testing.T) {
	reg := NewLoopRegistry()
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	reg.Register("lote-1", ctx1, cancel1)
	reg.Register("lote-2", ctx2, cancel2)

	reg.StopAll()

	for i, ctx := range []context.Context{ctx1, ctx2} {
		select {
		case <-ctx.Done():
		default:
			t.Fatalf("ctx %d no fue cancelado por StopAll", i)
		}
	}
	if _, ok := reg.GetLoop("lote-1"); ok {
		t.Fatalf("StopAll no limpió el mapa de loops")
	}
	if _, ok := reg.Get("lote-2"); ok {
		t.Fatalf("StopAll no limpió el mapa de cancels")
	}
}

// TestHandleControlStartLoopSobreviveCierreRequest es la regresión de F-B2: el
// loop no debe morir cuando el server cancela r.Context() al cerrar la request.
func TestHandleControlStartLoopSobreviveCierreRequest(t *testing.T) {
	secret := []byte("regression-f-b2-secret-key-1234567890")
	loteID := "lote-fb2"
	reg := NewLoopRegistry()
	// El handler ahora exige todas las deps no-nil; partimos del set válido y
	// sólo sobreescribimos las que este test necesita.
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: &lotesector.Lote{
		ID:                           loteID,
		Estado:                       lotesector.EstadoAbierto,
		Modo:                         lotesector.ModoSupervision,
		ProductoID:                   "prod-1",
		VentanaEvaluacionSegundos:    3600,
		VentanaActualizacionSegundos: 3600,
	}}
	deps.ProductoRepo = &fakeProductoRepo{prod: &producto.Producto{ID: "prod-1"}}
	deps.LoopCancels = reg
	h := NewControlHandler(deps)

	// Un server real cancela el contexto de la request cuando ServeHTTP retorna;
	// así reproducimos el escenario de producción.
	srv := httptest.NewServer(authController.JWTMiddleware(secret, h.HandleControlStart(deps)))
	defer srv.Close()
	defer reg.StopAll()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/lotes/"+loteID+"/control/start", nil)
	if err != nil {
		t.Fatalf("crear request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "session_token", Value: signedTestToken(t, secret, user.RoleSupervisor)})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST control/start: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if _, ok := reg.GetLoop(loteID); !ok {
		t.Fatalf("el loop debería estar registrado tras el 200")
	}

	// Esperar a que el server cancele r.Context() y el watcher reaccione. Con el
	// fix el loop usa un contexto raíz propio y sigue vivo.
	time.Sleep(1 * time.Second)
	if _, ok := reg.GetLoop(loteID); !ok {
		t.Fatalf("REGRESIÓN F-B2: el loop murió al cerrarse la request HTTP")
	}
}

// signedTestToken firma un JWT HS256 válido para el middleware de auth.
func signedTestToken(t *testing.T, secret []byte, role string) string {
	t.Helper()
	claims := authService.Claims{
		Email: "sup@fermar.com.ar",
		Name:  "Supervisor Test",
		Role:  role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(secret)
	if err != nil {
		t.Fatalf("firmar token: %v", err)
	}
	return signed
}
