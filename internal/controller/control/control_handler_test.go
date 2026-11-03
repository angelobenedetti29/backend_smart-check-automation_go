package controller

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/consigna"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/decisiones_control"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/metricashorno"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/control"
	loteSectorService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
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
	mu        sync.Mutex
	pendiente *decisiones_control.DecisionControl
	pendErr   error
	saveErr   error
	saved     []*decisiones_control.DecisionControl

	porID    *decisiones_control.DecisionControl
	porIDErr error

	aprobarErr   error
	aprobarCalls []string // decision ids aprobados, en orden
	aprobarUser  []string

	marcarErr   error
	marcarCalls []string // decision ids marcados como aplicados
	marcarPor   []string // aplicado_por efectivo
}

// savedSnapshot copia la slice de decisiones guardadas para leerla desde el
// test sin carreras con la goroutine del Loop.
func (f *fakeDecisionesRepo) savedSnapshot() []*decisiones_control.DecisionControl {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*decisiones_control.DecisionControl(nil), f.saved...)
}

func (f *fakeDecisionesRepo) Save(_ context.Context, d *decisiones_control.DecisionControl) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = append(f.saved, d)
	return nil
}

func (f *fakeDecisionesRepo) ListByLote(_ context.Context, _ string, _ int) ([]decisiones_control.DecisionControl, error) {
	return nil, nil
}

func (f *fakeDecisionesRepo) GetUltimaPendiente(_ context.Context, _ string) (*decisiones_control.DecisionControl, error) {
	return f.pendiente, f.pendErr
}

func (f *fakeDecisionesRepo) GetByID(_ context.Context, _ string) (*decisiones_control.DecisionControl, error) {
	return f.porID, f.porIDErr
}

func (f *fakeDecisionesRepo) Aprobar(_ context.Context, decisionID, usuario string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.aprobarErr != nil {
		return f.aprobarErr
	}
	f.aprobarCalls = append(f.aprobarCalls, decisionID)
	f.aprobarUser = append(f.aprobarUser, usuario)
	return nil
}

func (f *fakeDecisionesRepo) MarcarAplicada(_ context.Context, decisionID string, _, _ float64, aplicadoPor string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.marcarErr != nil {
		return f.marcarErr
	}
	f.marcarCalls = append(f.marcarCalls, decisionID)
	f.marcarPor = append(f.marcarPor, aplicadoPor)
	return nil
}

// fakeConsignaDispatch implementa control.ConsignaDispatchService y cuenta los
// despachos para verificar que aprobar no despacha.
type fakeConsignaDispatch struct{ calls int }

func (f *fakeConsignaDispatch) Dispatch(_ context.Context, _ consigna.ConsignaManualRequest, _ consigna.Origen, _ *string) (*consigna.Consigna, error) {
	f.calls++
	return nil, nil
}

// fakeMetricasRepo implementa MetricasRepo. metricasFn permite generar métricas
// con timestamp relativo a cada llamada (útil para el loop con reloj real).
type fakeMetricasRepo struct {
	metricas   []metricashorno.MetricaHorno
	metricasFn func() []metricashorno.MetricaHorno
}

func (f *fakeMetricasRepo) GetUltimasParaLote(_ context.Context, _ string, _ int) ([]metricashorno.MetricaHorno, error) {
	if f.metricasFn != nil {
		return f.metricasFn(), nil
	}
	return f.metricas, nil
}

// fakePredictorHandler es un optimizador.Predictor determinista para el loop.
type fakePredictorHandler struct {
	out optimizador.PrediccionSalida
}

func (f fakePredictorHandler) Predict(_ context.Context, _ optimizador.EntradasPredictor) (optimizador.PrediccionSalida, error) {
	return f.out, nil
}

// ptrF64 devuelve un puntero a una copia del valor.
func ptrF64(v float64) *float64 { return &v }

// fakeEventosRepoHandler implementa EventosRepo.
type fakeEventosRepoHandler struct{}

func (f *fakeEventosRepoHandler) CountEventosEnVentana(_ context.Context, _ string, _, _ time.Time) (int, int, int, error) {
	return 0, 0, 0, nil
}

func (f *fakeEventosRepoHandler) ConfianzaPromedioEnVentana(_ context.Context, _ string, _, _ time.Time) (float64, int, error) {
	return 0, 0, nil
}

// fakeConfianzaRepo implementa ConfianzaRepo y registra los argumentos
// recibidos para verificar el passthrough del límite.
type fakeConfianzaRepo struct {
	clases     []lotesector.ConfianzaClase
	err        error
	lastLoteID string
	lastLimite int
}

func (f *fakeConfianzaRepo) ConfianzaPorClase(_ context.Context, loteID string, limite int) ([]lotesector.ConfianzaClase, error) {
	f.lastLoteID = loteID
	f.lastLimite = limite
	return f.clases, f.err
}

// fakeDeviceSector implementa DeviceSectorLookup para el PUT de la Pi.
type fakeDeviceSector struct {
	info *loteSectorService.SectorInfo
	err  error
}

func (f *fakeDeviceSector) SectorDelDispositivo(_ context.Context, _ string) (*loteSectorService.SectorInfo, error) {
	return f.info, f.err
}

// fakeLoteRepoHandler implementa lotesector.Repository para los tests del
// handler. Sólo UpdateModo (y los métodos de tuning/reanudación) son
// configurables; el resto son stubs no usados por los handlers de control.
type fakeLoteRepoHandler struct {
	updateErr   error
	updatedModo string
	updateCalls int

	abiertos  []lotesector.Lote
	listErr   error
	listCalls int

	paramErr      error
	paramCalls    int
	ultimosParams lotesector.ParametrosControl

	setpointsErr   error
	setpointsCalls int
	setpointsTemp  float64
	setpointsVel   float64
}

func (f *fakeLoteRepoHandler) GetAbiertoBySector(_ context.Context, _ string) (*lotesector.Lote, error) {
	return nil, nil
}

func (f *fakeLoteRepoHandler) GetByID(_ context.Context, _ string) (*lotesector.Lote, error) {
	return nil, lotesector.ErrNotFound
}

func (f *fakeLoteRepoHandler) Abrir(_ context.Context, _ lotesector.AbrirParams) (*lotesector.Lote, bool, error) {
	return nil, false, nil
}

func (f *fakeLoteRepoHandler) RegistrarEventos(_ context.Context, _, _, _ string, _ []lotesector.Evento) (int, int, *lotesector.Lote, error) {
	return 0, 0, nil, nil
}

func (f *fakeLoteRepoHandler) Cerrar(_ context.Context, _ lotesector.CerrarParams) (*lotesector.Lote, bool, error) {
	return nil, false, nil
}

func (f *fakeLoteRepoHandler) Historial(_ context.Context, _ lotesector.HistorialParams) ([]lotesector.Lote, int, error) {
	return nil, 0, nil
}

func (f *fakeLoteRepoHandler) CountEventosEnVentana(_ context.Context, _ string, _, _ time.Time) (int, int, int, error) {
	return 0, 0, 0, nil
}

func (f *fakeLoteRepoHandler) UpdateModo(_ context.Context, _ string, modo string) error {
	f.updateCalls++
	f.updatedModo = modo
	return f.updateErr
}

func (f *fakeLoteRepoHandler) ListAbiertos(_ context.Context) ([]lotesector.Lote, error) {
	f.listCalls++
	return f.abiertos, f.listErr
}

func (f *fakeLoteRepoHandler) UpdateParametrosControl(_ context.Context, _ string, p lotesector.ParametrosControl) error {
	f.paramCalls++
	f.ultimosParams = p
	return f.paramErr
}

func (f *fakeLoteRepoHandler) UpdateSetpointsAplicados(_ context.Context, _ string, temp, vel float64) error {
	f.setpointsCalls++
	f.setpointsTemp, f.setpointsVel = temp, vel
	return f.setpointsErr
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
		ConfianzaRepo:  &fakeConfianzaRepo{},
		DecisionesRepo: &fakeDecisionesRepo{},
		Broker:         sse.NewBroker(),
		LoteRepo:       &fakeLoteRepoHandler{},
		DeviceSector:   &fakeDeviceSector{info: &loteSectorService.SectorInfo{SectorID: "s-1"}},
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
		{"ConfianzaRepo", func(d *ControlDeps) { d.ConfianzaRepo = nil }},
		{"DecisionesRepo", func(d *ControlDeps) { d.DecisionesRepo = nil }},
		{"Broker", func(d *ControlDeps) { d.Broker = nil }},
		{"LoteRepo", func(d *ControlDeps) { d.LoteRepo = nil }},
		{"DeviceSector", func(d *ControlDeps) { d.DeviceSector = nil }},
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
	snap       *control.PropuestaSnapshot
	lastTTL    int
	clearedIDs []string
}

func (f *fakeLoop) ClearPropuesta(decisionID string) {
	f.clearedIDs = append(f.clearedIDs, decisionID)
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
	h := &ControlHandler{deps: ControlDeps{
		LoopCancels:    NewLoopRegistry(),
		DecisionesRepo: &fakeDecisionesRepo{pendiente: nil},
		LoteLookup:     &fakeLoteLookup{lote: &lotesector.Lote{VentanaActualizacionSegundos: 30}},
	}}
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
	snap := &control.PropuestaSnapshot{
		DecisionID:    "dec-9",
		Temp1:         180,
		Velocidad:     0.45,
		VentanaInicio: time.Unix(100, 0).UTC(),
		VentanaFin:    time.Unix(200, 0).UTC(),
		CreatedAt:     time.Unix(200, 0).UTC(),
		Autorizada:    true,
		Fase:          control.FaseAdaptando,
	}
	fl := &fakeLoop{snap: snap}
	reg := NewLoopRegistry()
	reg.SetLoop("lote-1", fl)
	h := &ControlHandler{deps: ControlDeps{
		LoopCancels:    reg,
		DecisionesRepo: &fakeDecisionesRepo{},
		LoteLookup:     &fakeLoteLookup{lote: &lotesector.Lote{VentanaActualizacionSegundos: 30}},
	}}

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
	if fl.lastTTL != 30 {
		t.Fatalf("el loop no recibió el TTL del lote; got=%d", fl.lastTTL)
	}
	if !env.Data.Autorizada {
		t.Fatalf("autorizada = false, want true (snapshot)")
	}
	if env.Data.Fase != control.FaseAdaptando {
		t.Fatalf("fase = %q, want %q", env.Data.Fase, control.FaseAdaptando)
	}
}

// TestHandleConsignaPropuestaConLoopVacioFallbackDB verifica que un loop sin
// propuesta cae al fallback de la última decisión pendiente persistida.
func TestHandleConsignaPropuestaConLoopVacioFallbackDB(t *testing.T) {
	reg := NewLoopRegistry()
	reg.SetLoop("lote-1", &fakeLoop{snap: nil})
	repo := &fakeDecisionesRepo{pendiente: &decisiones_control.DecisionControl{
		ID:            "dec-db",
		TempPropuesto: 175,
		VelPropuesto:  0.5,
		VentanaInicio: time.Unix(300, 0).UTC(),
		VentanaFin:    time.Unix(400, 0).UTC(),
		CreatedAt:     time.Now().UTC(),
	}}
	h := &ControlHandler{deps: ControlDeps{
		LoopCancels:    reg,
		DecisionesRepo: repo,
		LoteLookup: &fakeLoteLookup{lote: &lotesector.Lote{
			Estado:                       lotesector.EstadoAbierto,
			Modo:                         lotesector.ModoSupervision,
			VentanaActualizacionSegundos: 45,
		}}},
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
	h := &ControlHandler{deps: ControlDeps{
		LoopCancels:    NewLoopRegistry(),
		DecisionesRepo: repo,
		LoteLookup: &fakeLoteLookup{lote: &lotesector.Lote{
			Estado:                       lotesector.EstadoCerrado,
			Modo:                         lotesector.ModoSupervision,
			VentanaActualizacionSegundos: 60,
		}}},
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
	h := &ControlHandler{deps: ControlDeps{
		LoopCancels:    NewLoopRegistry(),
		DecisionesRepo: repo,
		LoteLookup: &fakeLoteLookup{lote: &lotesector.Lote{
			Estado:                       lotesector.EstadoAbierto,
			Modo:                         lotesector.ModoManual,
			VentanaActualizacionSegundos: 60,
		}}},
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
	h := &ControlHandler{deps: ControlDeps{
		LoopCancels:    NewLoopRegistry(),
		DecisionesRepo: repo,
		LoteLookup: &fakeLoteLookup{lote: &lotesector.Lote{
			Estado:                       lotesector.EstadoAbierto,
			Modo:                         lotesector.ModoSupervision,
			VentanaActualizacionSegundos: 60,
		}}},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes/lote-1/consigna-propuesta", nil)
	h.HandleConsignaPropuesta(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleConsignaPropuestaFallbackAutorizacion cubre la semántica del
// fallback persistido: se sirve en SUPERVISION y AUTOMATICO, y en MANUAL sólo
// si la fila ya está aprobada (override manual); autorizada=true en AUTOMATICO
// o cuando la fila ya tiene aprobada_por; manual_override=true sólo en el
// override manual aprobado (Muestras=0); 204 en MANUAL sin aprobar/cerrado.
func TestHandleConsignaPropuestaFallbackAutorizacion(t *testing.T) {
	aprobada := "operario@fermar.com.ar"
	cases := []struct {
		name               string
		estado             string
		modo               string
		aprobadaPor        *string
		muestras           int
		wantStatus         int
		wantAuth           bool
		wantManualOverride bool
	}{
		{"automatico", lotesector.EstadoAbierto, lotesector.ModoAutomatico, nil, 17, http.StatusOK, true, false},
		{"supervision-sin-aprobar", lotesector.EstadoAbierto, lotesector.ModoSupervision, nil, 17, http.StatusOK, false, false},
		{"supervision-aprobada", lotesector.EstadoAbierto, lotesector.ModoSupervision, &aprobada, 17, http.StatusOK, true, false},
		{"manual", lotesector.EstadoAbierto, lotesector.ModoManual, nil, 0, http.StatusNoContent, false, false},
		{"manual-aprobada", lotesector.EstadoAbierto, lotesector.ModoManual, &aprobada, 0, http.StatusOK, true, true},
		{"manual-aprobada-mpc", lotesector.EstadoAbierto, lotesector.ModoManual, &aprobada, 17, http.StatusNoContent, false, false},
		{"cerrado", lotesector.EstadoCerrado, lotesector.ModoAutomatico, nil, 17, http.StatusNoContent, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeDecisionesRepo{pendiente: &decisiones_control.DecisionControl{
				ID:            "dec-1",
				TempPropuesto: 180,
				VelPropuesto:  0.4,
				Muestras:      tc.muestras,
				CreatedAt:     time.Now().UTC(),
				AprobadaPor:   tc.aprobadaPor,
			}}
			h := &ControlHandler{deps: ControlDeps{
				LoopCancels:    NewLoopRegistry(),
				DecisionesRepo: repo,
				LoteLookup: &fakeLoteLookup{lote: &lotesector.Lote{
					Estado:                       tc.estado,
					Modo:                         tc.modo,
					VentanaActualizacionSegundos: 60,
				}}},
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes/lote-1/consigna-propuesta", nil)
			h.HandleConsignaPropuesta(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var env consignaEnvelope
			if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if env.Data.Autorizada != tc.wantAuth {
				t.Fatalf("autorizada = %v, want %v", env.Data.Autorizada, tc.wantAuth)
			}
			if env.Data.ManualOverride != tc.wantManualOverride {
				t.Fatalf("manual_override = %v, want %v", env.Data.ManualOverride, tc.wantManualOverride)
			}
			if tc.aprobadaPor != nil {
				if env.Data.AprobadaPor == nil || *env.Data.AprobadaPor != *tc.aprobadaPor {
					t.Fatalf("aprobada_por = %v, want %q", env.Data.AprobadaPor, *tc.aprobadaPor)
				}
			}
		})
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
	srv := httptest.NewServer(authController.JWTMiddleware(secret, h.HandleControlStart()))
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

// --- F-B5: PATCH /lotes/{id}/modo ---

// modoChangeEnvelope modela el envelope del endpoint de cambio de modo.
type modoChangeEnvelope struct {
	Success bool           `json:"success"`
	Data    modoCambioData `json:"data"`
}

// newModeChangeHandler arma el handler con deps controlables y un server con
// JWT middleware (para ejercitar el chequeo de rol real).
func newModeChangeHandler(t *testing.T, lote *lotesector.Lote, loteRepo *fakeLoteRepoHandler, decisiones *fakeDecisionesRepo) (*ControlHandler, *httptest.Server, []byte) {
	t.Helper()
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	deps.LoteRepo = loteRepo
	deps.DecisionesRepo = decisiones
	deps.ProductoRepo = &fakeProductoRepo{prod: &producto.Producto{
		ID:                     lote.ProductoID,
		TempSetpoint:           ptrF64(200),
		VelocidadCintaSetpoint: ptrF64(0.3),
	}}
	h := NewControlHandler(deps)

	secret := []byte("mode-change-test-secret-1234567890")
	srv := httptest.NewServer(authController.JWTMiddleware(secret, h.HandleModeChange))
	t.Cleanup(srv.Close)
	t.Cleanup(h.deps.LoopCancels.StopAll)
	return h, srv, secret
}

// patchModo dispara PATCH /api/v1/lotes/lote-1/modo y devuelve el status + data.
func patchModo(t *testing.T, srv *httptest.Server, secret []byte, role, body string) (int, modoChangeEnvelope) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/lotes/lote-1/modo", strings.NewReader(body))
	if err != nil {
		t.Fatalf("crear request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: signedTestToken(t, secret, role)})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH modo: %v", err)
	}
	defer resp.Body.Close()
	var env modoChangeEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp.StatusCode, env
}

// TestHandleModeChangeNoopDevuelve200 verifica que fijar el modo vigente no
// audita, no toca el loop ni persiste.
func TestHandleModeChangeNoopDevuelve200(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision,
		ProductoID: "prod-1", VentanaEvaluacionSegundos: 600,
	}
	repo := &fakeLoteRepoHandler{}
	dec := &fakeDecisionesRepo{}
	h, srv, secret := newModeChangeHandler(t, lote, repo, dec)

	status, env := patchModo(t, srv, secret, user.RoleSupervisor, `{"modo":"SUPERVISION"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !env.Success || env.Data.Cambiado {
		t.Fatalf("se esperaba cambiado=false; data=%+v", env.Data)
	}
	if env.Data.Modo != lotesector.ModoSupervision {
		t.Fatalf("modo = %q, want SUPERVISION", env.Data.Modo)
	}
	if repo.updateCalls != 0 {
		t.Fatalf("no debe llamar UpdateModo; calls=%d", repo.updateCalls)
	}
	if saved := dec.savedSnapshot(); len(saved) != 0 {
		t.Fatalf("no debe auditar un no-op; saved=%d", len(saved))
	}
	if _, ok := h.deps.LoopCancels.GetLoop("lote-1"); ok {
		t.Fatalf("el no-op no debe registrar loop")
	}
}

// TestHandleModeChangeAMANUALCancelaLoop verifica que pasar a MANUAL cancela el
// loop vigente, persiste el modo y deja la auditoría.
func TestHandleModeChangeAMANUALCancelaLoop(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision,
		ProductoID: "prod-1", VentanaEvaluacionSegundos: 600,
	}
	repo := &fakeLoteRepoHandler{}
	dec := &fakeDecisionesRepo{}
	h, srv, secret := newModeChangeHandler(t, lote, repo, dec)

	loopCtx, loopCancel := context.WithCancel(context.Background())
	h.deps.LoopCancels.Register("lote-1", loopCtx, loopCancel)
	h.deps.LoopCancels.SetLoop("lote-1", &fakeLoop{})

	status, env := patchModo(t, srv, secret, user.RoleSupervisor, `{"modo":"MANUAL"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !env.Data.Cambiado || env.Data.ModoAnterior != lotesector.ModoSupervision || env.Data.ModoNuevo != lotesector.ModoManual {
		t.Fatalf("data de cambio incorrecta: %+v", env.Data)
	}
	if env.Data.LoopActivo {
		t.Fatalf("MANUAL no debe dejar loop activo")
	}
	if _, ok := h.deps.LoopCancels.GetLoop("lote-1"); ok {
		t.Fatalf("el loop viejo debe quedar desregistrado")
	}
	select {
	case <-loopCtx.Done():
	default:
		t.Fatalf("el loop viejo debe quedar cancelado")
	}
	if repo.updateCalls != 1 || repo.updatedModo != lotesector.ModoManual {
		t.Fatalf("UpdateModo mal llamado: calls=%d modo=%q", repo.updateCalls, repo.updatedModo)
	}
	saved := dec.savedSnapshot()
	if len(saved) != 1 {
		t.Fatalf("debe auditar el cambio; saved=%d", len(saved))
	}
	got := saved[0]
	if got.AplicadoPor != decisiones_control.AplicadoPorNadie {
		t.Fatalf("aplicado_por = %q, want NADIE", got.AplicadoPor)
	}
	if got.RazonSkipped == nil || *got.RazonSkipped != "cambio_modo_SUPERVISION_MANUAL" {
		t.Fatalf("razon_skipped = %v, want cambio_modo_SUPERVISION_MANUAL", got.RazonSkipped)
	}
	if got.TempPropuesto != 0 || got.VelPropuesto != 0 {
		t.Fatalf("setpoints del audit deben ser 0: temp=%v vel=%v", got.TempPropuesto, got.VelPropuesto)
	}
}

// TestHandleModeChangeASupervisionArrancaLoop verifica que pasar a SUPERVISION
// registra un loop nuevo, persiste el modo y audita.
func TestHandleModeChangeASupervisionArrancaLoop(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoManual,
		ProductoID: "prod-1", VentanaEvaluacionSegundos: 600,
	}
	repo := &fakeLoteRepoHandler{}
	dec := &fakeDecisionesRepo{}
	h, srv, secret := newModeChangeHandler(t, lote, repo, dec)

	status, env := patchModo(t, srv, secret, user.RoleSupervisor, `{"modo":"SUPERVISION"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !env.Data.Cambiado || env.Data.ModoAnterior != lotesector.ModoManual || env.Data.ModoNuevo != lotesector.ModoSupervision {
		t.Fatalf("data de cambio incorrecta: %+v", env.Data)
	}
	if !env.Data.LoopActivo {
		t.Fatalf("SUPERVISION debe dejar loop activo")
	}
	if _, ok := h.deps.LoopCancels.GetLoop("lote-1"); !ok {
		t.Fatalf("debe quedar un loop registrado")
	}
	if repo.updateCalls != 1 || repo.updatedModo != lotesector.ModoSupervision {
		t.Fatalf("UpdateModo mal llamado: calls=%d modo=%q", repo.updateCalls, repo.updatedModo)
	}
	// El loop arranca y puede dejar además una decisión propia (el tick es
	// inmediato); buscamos el audit del cambio entre las filas persistidas.
	saved := dec.savedSnapshot()
	found := false
	for _, got := range saved {
		if got.RazonSkipped != nil && *got.RazonSkipped == "cambio_modo_MANUAL_SUPERVISION" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("debe auditar el cambio; saved=%d", len(saved))
	}
}

// TestHandleModeChangeLoteCerrado409 verifica que un lote no ABIERTO se rechaza
// sin tocar DB ni auditar.
func TestHandleModeChangeLoteCerrado409(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", Estado: lotesector.EstadoCerrado, Modo: lotesector.ModoSupervision,
		ProductoID: "prod-1",
	}
	repo := &fakeLoteRepoHandler{}
	dec := &fakeDecisionesRepo{}
	_, srv, secret := newModeChangeHandler(t, lote, repo, dec)

	status, _ := patchModo(t, srv, secret, user.RoleSupervisor, `{"modo":"AUTOMATICO"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if repo.updateCalls != 0 || len(dec.savedSnapshot()) != 0 {
		t.Fatalf("no debe tocar DB ni auditar con lote cerrado")
	}
}

// TestHandleModeChangeRolInsuficiente403 verifica que un Operario no puede
// cambiar el modo.
func TestHandleModeChangeRolInsuficiente403(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoManual,
		ProductoID: "prod-1",
	}
	repo := &fakeLoteRepoHandler{}
	dec := &fakeDecisionesRepo{}
	_, srv, secret := newModeChangeHandler(t, lote, repo, dec)

	status, _ := patchModo(t, srv, secret, user.RoleOperario, `{"modo":"AUTOMATICO"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if repo.updateCalls != 0 || len(dec.savedSnapshot()) != 0 {
		t.Fatalf("un rol insuficiente no debe tocar DB ni auditar")
	}
}

// TestHandleModeChangeModoInvalido400 verifica que un modo fuera del dominio
// se rechaza con 400.
func TestHandleModeChangeModoInvalido400(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoManual,
		ProductoID: "prod-1",
	}
	repo := &fakeLoteRepoHandler{}
	dec := &fakeDecisionesRepo{}
	_, srv, secret := newModeChangeHandler(t, lote, repo, dec)

	status, _ := patchModo(t, srv, secret, user.RoleSupervisor, `{"modo":"INVALIDO"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if repo.updateCalls != 0 || len(dec.savedSnapshot()) != 0 {
		t.Fatalf("modo inválido no debe tocar DB ni auditar")
	}
}

// TestHandleModeChangeDevicePutActualizaModo verifica que la Pi pueda empujar
// el modo con PUT + Bearer, validando que pertenezca al sector del lote.
func TestHandleModeChangeDevicePutActualizaModo(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoManual,
		ProductoID: "prod-1", VentanaEvaluacionSegundos: 600,
	}
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	repo := &fakeLoteRepoHandler{}
	deps.LoteRepo = repo
	deps.ProductoRepo = &fakeProductoRepo{prod: &producto.Producto{
		ID:                     lote.ProductoID,
		TempSetpoint:           ptrF64(200),
		VelocidadCintaSetpoint: ptrF64(0.3),
	}}
	deps.DeviceSector = &fakeDeviceSector{info: &loteSectorService.SectorInfo{SectorID: "s-1"}}
	h := NewControlHandler(deps)
	t.Cleanup(h.deps.LoopCancels.StopAll)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/lotes/lote-1/modo", strings.NewReader(`{"modo":"AUTOMATICO"}`))
	req = req.WithContext(deviceauth.WithPrincipal(req.Context(), deviceauth.Principal{DeviceID: "dev-1"}))
	rec := httptest.NewRecorder()
	h.HandleModeChange(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if repo.updateCalls != 1 || repo.updatedModo != lotesector.ModoAutomatico {
		t.Fatalf("UpdateModo mal llamado: calls=%d modo=%q", repo.updateCalls, repo.updatedModo)
	}
	if _, ok := h.deps.LoopCancels.GetLoop("lote-1"); !ok {
		t.Fatalf("AUTOMATICO debe dejar loop registrado")
	}
}

// TestHandleModeChangeDevicePutOtroSector verifica que la Pi no pueda cambiar
// el modo de un lote de otro sector.
func TestHandleModeChangeDevicePutOtroSector(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoManual,
		ProductoID: "prod-1",
	}
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	repo := &fakeLoteRepoHandler{}
	deps.LoteRepo = repo
	deps.DeviceSector = &fakeDeviceSector{info: &loteSectorService.SectorInfo{SectorID: "s-2"}}
	h := NewControlHandler(deps)
	t.Cleanup(h.deps.LoopCancels.StopAll)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/lotes/lote-1/modo", strings.NewReader(`{"modo":"AUTOMATICO"}`))
	req = req.WithContext(deviceauth.WithPrincipal(req.Context(), deviceauth.Principal{DeviceID: "dev-1"}))
	rec := httptest.NewRecorder()
	h.HandleModeChange(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if repo.updateCalls != 0 {
		t.Fatalf("no debe persistir un modo de otro sector; calls=%d", repo.updateCalls)
	}
}

// TestHandleModeChangeAuditSaveFallaNoBloquea verifica que un fallo del audit
// no impide el cambio de modo ni el arranque del loop.
func TestHandleModeChangeAuditSaveFallaNoBloquea(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoManual,
		ProductoID: "prod-1", VentanaEvaluacionSegundos: 600,
	}
	repo := &fakeLoteRepoHandler{}
	dec := &fakeDecisionesRepo{saveErr: errors.New("db down")}
	h, srv, secret := newModeChangeHandler(t, lote, repo, dec)

	status, env := patchModo(t, srv, secret, user.RoleSupervisor, `{"modo":"SUPERVISION"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 pese al fallo de audit", status)
	}
	if !env.Data.Cambiado || !env.Data.LoopActivo {
		t.Fatalf("el cambio debe completarse: %+v", env.Data)
	}
	if repo.updateCalls != 1 || repo.updatedModo != lotesector.ModoSupervision {
		t.Fatalf("UpdateModo mal llamado: calls=%d modo=%q", repo.updateCalls, repo.updatedModo)
	}
	if _, ok := h.deps.LoopCancels.GetLoop("lote-1"); !ok {
		t.Fatalf("el loop debe arrancar pese al fallo de audit")
	}
}

// --- F-B10: E2E del endpoint SSE con un Loop real ---

// TestControlSSEReceivePropuestaEnModoSupervision monta un Loop real en
// SUPERVISION sobre el broker real, arranca el endpoint SSE y verifica que el
// cliente recibe al menos un evento `control.propuesta` con payload JSON válido
// para el lote.
func TestControlSSEReceivePropuestaEnModoSupervision(t *testing.T) {
	const loteID = "lote-sse"

	broker := sse.NewBroker()
	reg := NewLoopRegistry()

	metricas := &fakeMetricasRepo{metricasFn: func() []metricashorno.MetricaHorno {
		ts := time.Now().UTC().Add(-10 * time.Millisecond)
		return []metricashorno.MetricaHorno{{
			ReceivedAt:     ts,
			TempHorno1:     ptrF64(120),
			VelocidadCinta: ptrF64(0.2),
			TempObjetivo:   ptrF64(120),
			VelObjetivo:    ptrF64(0.2),
		}}
	}}

	loop := control.NewLoop(
		loteID,
		&fakeLoteLookup{lote: &lotesector.Lote{
			ID: loteID, Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision,
		}},
		metricas,
		&fakeEventosRepoHandler{},
		&fakeProductoRepo{prod: &producto.Producto{ID: "prod-1"}},
		optimizador.NewOptimizer(fakePredictorHandler{out: optimizador.PrediccionSalida{
			PctCrudos: 10, PctOk: 80, PctQuemados: 10, ModelVersion: "test",
		}}),
		&fakeConsignaDispatch{},
		&fakeDecisionesRepo{},
		broker,
	)
	loop.Configurar(
		"prod-1", lotesector.ModoSupervision, "horno-1", 1, 1,
		optimizador.Pesos{WC: 1, WQ: 1, WO: 1, DT: 0.05, DC: 0.5},
		optimizador.Limites{
			TempMin: 100, TempMax: 300,
			VelMin: 0.05, VelMax: 0.5,
			DeltaTemp1: 10, DeltaVel: 0.05,
			ToleranciaTempC: 2.0,
		},
	)

	ctx, cancel := context.WithCancel(context.Background())
	reg.Register(loteID, ctx, cancel)
	reg.SetLoop(loteID, loop)
	loop.Start(ctx, cancel)
	defer reg.StopAll()

	sseH := NewControlSSEHandler(broker)
	srv := httptest.NewServer(http.HandlerFunc(sseH.HandleControlEvents))
	defer srv.Close()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer reqCancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL+"/api/v1/lotes/"+loteID+"/control/events", nil)
	if err != nil {
		t.Fatalf("crear request SSE: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET control/events: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var gotEvent bool
	var dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "event: control.propuesta" {
			gotEvent = true
		}
		if strings.HasPrefix(line, "data:") {
			dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if gotEvent && dataLine != "" {
			break
		}
	}
	if !gotEvent {
		t.Fatalf("no se recibió 'event: control.propuesta' en el stream SSE")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
		t.Fatalf("payload SSE no es JSON válido: %v (data=%q)", err, dataLine)
	}
	if got, _ := payload["lote_id"].(string); got != loteID {
		t.Fatalf("lote_id del payload = %q, want %q", got, loteID)
	}
	if _, ok := payload["propuesta"]; !ok {
		t.Fatalf("payload SSE sin 'propuesta': %+v", payload)
	}
	propuesta, ok := payload["propuesta"].(map[string]any)
	if !ok {
		t.Fatalf("propuesta SSE no es un objeto: %+v", payload["propuesta"])
	}
	for _, key := range []string{"temp1", "velocidad", "z", "prediccion"} {
		if _, ok := propuesta[key]; !ok {
			t.Fatalf("propuesta SSE sin %q: %+v", key, propuesta)
		}
	}
	if _, ok := payload["autorizada"]; !ok {
		t.Fatalf("payload SSE sin 'autorizada': %+v", payload)
	}
}

// --- control/estado y confianza-clases ---

// doPanelRequest dispara una request al server de prueba con cookie JWT y
// devuelve el status y el body crudo (para poder assertar `null` explícito).
func doPanelRequest(t *testing.T, srv *httptest.Server, secret []byte, role, method, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("crear request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "session_token", Value: signedTestToken(t, secret, role)})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("leer body: %v", err)
	}
	return resp.StatusCode, body
}

// newControlEstadoServer monta HandleControlEstado con el JWT middleware real.
func newControlEstadoServer(t *testing.T, lookup LoteLookupService, reg *LoopRegistry) (*httptest.Server, []byte) {
	t.Helper()
	deps := validControlDeps()
	deps.LoteLookup = lookup
	deps.LoopCancels = reg
	h := NewControlHandler(deps)
	secret := []byte("control-estado-test-secret-1234567890")
	srv := httptest.NewServer(authController.JWTMiddleware(secret, h.HandleControlEstado))
	t.Cleanup(srv.Close)
	t.Cleanup(reg.StopAll)
	return srv, secret
}

// controlEstadoEnvelope modela el envelope de GET /lotes/{id}/control/estado.
type controlEstadoEnvelope struct {
	Success bool              `json:"success"`
	Data    controlEstadoData `json:"data"`
}

// TestHandleControlEstadoSinLoop verifica que sin loop activo el modo venga del
// lote y started_at sea null explícito.
func TestHandleControlEstadoSinLoop(t *testing.T) {
	reg := NewLoopRegistry()
	srv, secret := newControlEstadoServer(t,
		&fakeLoteLookup{lote: &lotesector.Lote{ID: "lote-1", Modo: lotesector.ModoSupervision}}, reg)

	status, body := doPanelRequest(t, srv, secret, user.RoleOperario, http.MethodGet, "/api/v1/lotes/lote-1/control/estado")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var env controlEstadoEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !env.Success || env.Data.LoteID != "lote-1" || env.Data.Modo != lotesector.ModoSupervision {
		t.Fatalf("data inesperada: %+v", env.Data)
	}
	if env.Data.LoopActivo || env.Data.StartedAt != nil {
		t.Fatalf("sin loop: loop_activo=%v started_at=%v", env.Data.LoopActivo, env.Data.StartedAt)
	}
	if !strings.Contains(string(body), `"started_at":null`) {
		t.Fatalf("started_at debe serializarse null; body=%s", body)
	}
}

// TestHandleControlEstadoConLoopActivo verifica que un loop registrado reporte
// loop_activo=true y un started_at no nulo.
func TestHandleControlEstadoConLoopActivo(t *testing.T) {
	reg := NewLoopRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	reg.Register("lote-1", ctx, cancel)
	desde := time.Now().UTC().Add(-time.Second)

	srv, secret := newControlEstadoServer(t,
		&fakeLoteLookup{lote: &lotesector.Lote{ID: "lote-1", Modo: lotesector.ModoAutomatico}}, reg)

	status, body := doPanelRequest(t, srv, secret, user.RoleSupervisor, http.MethodGet, "/api/v1/lotes/lote-1/control/estado")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var env controlEstadoEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !env.Data.LoopActivo {
		t.Fatalf("se esperaba loop_activo=true; body=%s", body)
	}
	if env.Data.StartedAt == nil {
		t.Fatalf("se esperaba started_at no nulo; body=%s", body)
	}
	if env.Data.StartedAt.Before(desde) || env.Data.StartedAt.After(time.Now().UTC()) {
		t.Fatalf("started_at fuera de rango: %v", env.Data.StartedAt)
	}
	if env.Data.Modo != lotesector.ModoAutomatico {
		t.Fatalf("modo = %q, want AUTOMATICO", env.Data.Modo)
	}
}

// TestHandleControlEstadoLoteDesconocido verifica el 404.
func TestHandleControlEstadoLoteDesconocido(t *testing.T) {
	srv, secret := newControlEstadoServer(t, &fakeLoteLookup{err: lotesector.ErrNotFound}, NewLoopRegistry())
	if status, body := doPanelRequest(t, srv, secret, user.RoleAdmin, http.MethodGet, "/api/v1/lotes/lote-x/control/estado"); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", status, body)
	}
}

// TestHandleControlEstadoMetodoNoPermitido verifica el 405.
func TestHandleControlEstadoMetodoNoPermitido(t *testing.T) {
	srv, secret := newControlEstadoServer(t, &fakeLoteLookup{lote: &lotesector.Lote{ID: "lote-1"}}, NewLoopRegistry())
	if status, body := doPanelRequest(t, srv, secret, user.RoleAdmin, http.MethodPost, "/api/v1/lotes/lote-1/control/estado"); status != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405; body=%s", status, body)
	}
}

// confianzaEnvelope modela el envelope de GET /lotes/{id}/confianza-clases.
type confianzaEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		LoteID string                      `json:"lote_id"`
		Clases []lotesector.ConfianzaClase `json:"clases"`
		Total  int                         `json:"total"`
	} `json:"data"`
}

// newConfianzaClasesServer monta HandleConfianzaClases con el JWT middleware.
func newConfianzaClasesServer(t *testing.T, lookup LoteLookupService, repo *fakeConfianzaRepo) (*httptest.Server, []byte) {
	t.Helper()
	deps := validControlDeps()
	deps.LoteLookup = lookup
	deps.ConfianzaRepo = repo
	h := NewControlHandler(deps)
	secret := []byte("confianza-clases-test-secret-1234567890")
	srv := httptest.NewServer(authController.JWTMiddleware(secret, h.HandleConfianzaClases))
	t.Cleanup(srv.Close)
	return srv, secret
}

// TestHandleConfianzaClasesPassthrough verifica que las clases se devuelvan en
// el orden del repo, con el null de stddev preservado y el límite propagado.
func TestHandleConfianzaClasesPassthrough(t *testing.T) {
	stddev := 0.07
	repo := &fakeConfianzaRepo{clases: []lotesector.ConfianzaClase{
		{Clase: lotesector.EstadoCrudo, ConfianzaMedia: 0.71, ConfianzaStddev: &stddev, Muestras: 3},
		{Clase: lotesector.EstadoOK, ConfianzaMedia: 0.93, Muestras: 1},
	}}
	srv, secret := newConfianzaClasesServer(t, &fakeLoteLookup{lote: &lotesector.Lote{ID: "lote-1"}}, repo)

	status, body := doPanelRequest(t, srv, secret, user.RoleSupervisor, http.MethodGet, "/api/v1/lotes/lote-1/confianza-clases?limite=50")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	var env confianzaEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Data.LoteID != "lote-1" || env.Data.Total != 2 || len(env.Data.Clases) != 2 {
		t.Fatalf("data inesperada: %+v", env.Data)
	}
	if env.Data.Clases[0].Clase != lotesector.EstadoCrudo || env.Data.Clases[1].Clase != lotesector.EstadoOK {
		t.Fatalf("orden de clases incorrecto: %+v", env.Data.Clases)
	}
	if env.Data.Clases[0].ConfianzaStddev == nil || *env.Data.Clases[0].ConfianzaStddev != stddev {
		t.Fatalf("stddev mal propagado: %+v", env.Data.Clases[0])
	}
	if env.Data.Clases[1].ConfianzaStddev != nil {
		t.Fatalf("stddev de una muestra debe ser null: %+v", env.Data.Clases[1])
	}
	if repo.lastLoteID != "lote-1" || repo.lastLimite != 50 {
		t.Fatalf("repo recibió lote=%q limite=%d", repo.lastLoteID, repo.lastLimite)
	}
}

// TestHandleConfianzaClasesLimite verifica el default, el clamp y que sin
// muestras el JSON sea [] y no null.
func TestHandleConfianzaClasesLimite(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  int
	}{
		{"sin query usa default", "", 200},
		{"no numérico usa default", "?limite=abc", 200},
		{"cero se clampa a 1", "?limite=0", 1},
		{"negativo se clampa a 1", "?limite=-5", 1},
		{"arriba del máximo se clampa", "?limite=5000", 2000},
		{"dentro de rango se respeta", "?limite=7", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeConfianzaRepo{}
			srv, secret := newConfianzaClasesServer(t, &fakeLoteLookup{lote: &lotesector.Lote{ID: "lote-1"}}, repo)
			status, body := doPanelRequest(t, srv, secret, user.RoleAdmin, http.MethodGet, "/api/v1/lotes/lote-1/confianza-clases"+tc.query)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", status, body)
			}
			if repo.lastLimite != tc.want {
				t.Fatalf("limite = %d, want %d", repo.lastLimite, tc.want)
			}
			if !strings.Contains(string(body), `"clases":[]`) {
				t.Fatalf("clases vacías deben serializarse []; body=%s", body)
			}
		})
	}
}

// TestHandleConfianzaClasesLoteDesconocido verifica el 404 sin consultar el repo.
func TestHandleConfianzaClasesLoteDesconocido(t *testing.T) {
	repo := &fakeConfianzaRepo{}
	srv, secret := newConfianzaClasesServer(t, &fakeLoteLookup{err: lotesector.ErrNotFound}, repo)
	if status, body := doPanelRequest(t, srv, secret, user.RoleOperario, http.MethodGet, "/api/v1/lotes/lote-x/confianza-clases"); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", status, body)
	}
	if repo.lastLimite != 0 {
		t.Fatalf("no debe consultar el repo con lote desconocido")
	}
}

// --- EnsureLoopForLote ---

// TestEnsureLoopForLoteIdempotente verifica que no reemplaza un loop ya
// registrado para el lote.
func TestEnsureLoopForLoteIdempotente(t *testing.T) {
	deps := validControlDeps()
	deps.ProductoRepo = &fakeProductoRepo{prod: &producto.Producto{ID: "prod-1"}}
	h := NewControlHandler(deps)
	t.Cleanup(deps.LoopCancels.StopAll)

	ctx, cancel := context.WithCancel(context.Background())
	deps.LoopCancels.Register("lote-1", ctx, cancel)
	existente := &fakeLoop{}
	deps.LoopCancels.SetLoop("lote-1", existente)

	lote := &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico, ProductoID: "prod-1"}
	if err := h.EnsureLoopForLote(context.Background(), lote); err != nil {
		t.Fatalf("EnsureLoopForLote: %v", err)
	}
	got, ok := h.deps.LoopCancels.GetLoop("lote-1")
	if !ok || got != any(existente) {
		t.Fatalf("el loop existente no debe reemplazarse; got=%T", got)
	}
}

// TestEnsureLoopForLoteSkip verifica que no arranca loop para lote nil, cerrado
// o en modo MANUAL.
func TestEnsureLoopForLoteSkip(t *testing.T) {
	cases := []struct {
		name string
		lote *lotesector.Lote
	}{
		{"nil", nil},
		{"cerrado", &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoCerrado, Modo: lotesector.ModoAutomatico}},
		{"manual", &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoManual}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := validControlDeps()
			deps.ProductoRepo = &fakeProductoRepo{prod: &producto.Producto{ID: "prod-1"}}
			h := NewControlHandler(deps)
			t.Cleanup(deps.LoopCancels.StopAll)

			if err := h.EnsureLoopForLote(context.Background(), tc.lote); err != nil {
				t.Fatalf("EnsureLoopForLote: %v", err)
			}
			if _, ok := h.deps.LoopCancels.GetLoop("lote-1"); ok {
				t.Fatalf("no debe arrancar loop para %s", tc.name)
			}
		})
	}
}

// TestEnsureLoopForLoteArranca verifica que arranca el loop para los modos no
// manuales sobre un lote ABIERTO.
func TestEnsureLoopForLoteArranca(t *testing.T) {
	for _, modo := range []string{lotesector.ModoAutomatico, lotesector.ModoSupervision} {
		t.Run(modo, func(t *testing.T) {
			deps := validControlDeps()
			deps.ProductoRepo = &fakeProductoRepo{prod: &producto.Producto{ID: "prod-1"}}
			h := NewControlHandler(deps)
			t.Cleanup(deps.LoopCancels.StopAll)

			lote := &lotesector.Lote{
				ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: modo, ProductoID: "prod-1",
				VentanaEvaluacionSegundos: 3600, VentanaActualizacionSegundos: 3600,
			}
			if err := h.EnsureLoopForLote(context.Background(), lote); err != nil {
				t.Fatalf("EnsureLoopForLote: %v", err)
			}
			if _, ok := h.deps.LoopCancels.GetLoop("lote-1"); !ok {
				t.Fatalf("debe arrancar loop para modo %s", modo)
			}
		})
	}
}

// --- aplicar/aprobar consigna y ack del nodo ---

// aplicarEnvelope modela el envelope de POST /lotes/{id}/aplicar-consigna.
type aplicarEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Aprobada      bool    `json:"aprobada"`
		Aplicada      bool    `json:"aplicada"`
		DecisionID    string  `json:"decision_id"`
		Temp1Objetivo float64 `json:"temp1_objetivo"`
		VelObjetivo   float64 `json:"vel_objetivo"`
	} `json:"data"`
}

// newAplicarServer monta HandleAplicarConsigna con el JWT middleware real.
func newAplicarServer(t *testing.T, deps ControlDeps) (*ControlHandler, *httptest.Server, []byte) {
	t.Helper()
	h := NewControlHandler(deps)
	secret := []byte("aplicar-consigna-test-secret-1234567890")
	srv := httptest.NewServer(authController.JWTMiddleware(secret, h.HandleAplicarConsigna()))
	t.Cleanup(srv.Close)
	t.Cleanup(h.deps.LoopCancels.StopAll)
	return h, srv, secret
}

func postAplicar(t *testing.T, srv *httptest.Server, secret []byte, path, body string) (int, aplicarEnvelope) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("crear request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: signedTestToken(t, secret, user.RoleSupervisor)})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST aplicar-consigna: %v", err)
	}
	defer resp.Body.Close()
	var env aplicarEnvelope
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp.StatusCode, env
}

// TestHandleAplicarConsignaSupervisionApruebaSinDespachar verifica que con
// actuador "pi" el endpoint sólo persiste la aprobación (200 aplicada=false) y
// no despacha al horno.
func TestHandleAplicarConsignaSupervisionApruebaSinDespachar(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision,
	}
	repo := &fakeDecisionesRepo{pendiente: &decisiones_control.DecisionControl{
		ID: "dec-1", LoteID: "lote-1", TempPropuesto: 175, VelPropuesto: 0.5,
	}}
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	deps.DecisionesRepo = repo
	dispatch := &fakeConsignaDispatch{}
	deps.Consigna = dispatch
	_, srv, secret := newAplicarServer(t, deps)

	status, env := postAplicar(t, srv, secret, "/api/v1/lotes/lote-1/aplicar-consigna", `{}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !env.Data.Aprobada || env.Data.Aplicada {
		t.Fatalf("se esperaba aprobada=true aplicada=false; data=%+v", env.Data)
	}
	if env.Data.DecisionID != "dec-1" || env.Data.Temp1Objetivo != 175 || env.Data.VelObjetivo != 0.5 {
		t.Fatalf("data inesperada: %+v", env.Data)
	}
	if len(repo.aprobarCalls) != 1 || repo.aprobarCalls[0] != "dec-1" {
		t.Fatalf("Aprobar no llamado con dec-1: %v", repo.aprobarCalls)
	}
	if repo.aprobarUser[0] != "sup@fermar.com.ar" {
		t.Fatalf("usuario de aprobación = %q", repo.aprobarUser[0])
	}
	if dispatch.calls != 0 {
		t.Fatalf("no debe despachar en modo pi; dispatch=%d", dispatch.calls)
	}
}

// TestHandleAplicarConsignaDecisionVieja409 evita aprobar una propuesta
// distinta de la que el operario está viendo.
func TestHandleAplicarConsignaDecisionVieja409(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision,
	}
	repo := &fakeDecisionesRepo{pendiente: &decisiones_control.DecisionControl{
		ID: "dec-nueva", LoteID: "lote-1", TempPropuesto: 175, VelPropuesto: 0.5,
	}}
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	deps.DecisionesRepo = repo
	_, srv, secret := newAplicarServer(t, deps)

	status, _ := postAplicar(t, srv, secret, "/api/v1/lotes/lote-1/aplicar-consigna", `{"decision_id":"dec-vieja"}`)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if len(repo.aprobarCalls) != 0 {
		t.Fatalf("no debe aprobar una decisión vieja; calls=%v", repo.aprobarCalls)
	}
}

// TestHandleAplicarConsignaAutomatico409 verifica que AUTOMATICO no se aprueba
// a mano: la Pi aplica sola.
func TestHandleAplicarConsignaAutomatico409(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico,
	}
	repo := &fakeDecisionesRepo{}
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	deps.DecisionesRepo = repo
	_, srv, secret := newAplicarServer(t, deps)

	if status, _ := postAplicar(t, srv, secret, "/api/v1/lotes/lote-1/aplicar-consigna", `{}`); status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
	if len(repo.aprobarCalls) != 0 {
		t.Fatalf("no debe aprobar en AUTOMATICO; calls=%v", repo.aprobarCalls)
	}
}

// TestHandleAplicarConsignaSinPendiente409 verifica el 409 cuando no hay
// propuesta en SUPERVISION.
func TestHandleAplicarConsignaSinPendiente409(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision,
	}
	repo := &fakeDecisionesRepo{pendiente: nil}
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	deps.DecisionesRepo = repo
	_, srv, secret := newAplicarServer(t, deps)

	if status, _ := postAplicar(t, srv, secret, "/api/v1/lotes/lote-1/aplicar-consigna", `{}`); status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", status)
	}
}

// TestHandleAplicarConsignaDeviceValidaSector verifica que la Pi sólo puede
// aprobar lotes de su sector, y que la auditoría registra "dispositivo:<id>".
func TestHandleAplicarConsignaDeviceValidaSector(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision,
	}
	repo := &fakeDecisionesRepo{pendiente: &decisiones_control.DecisionControl{
		ID: "dec-1", LoteID: "lote-1", TempPropuesto: 175, VelPropuesto: 0.5,
	}}
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	deps.DecisionesRepo = repo
	deps.DeviceSector = &fakeDeviceSector{info: &loteSectorService.SectorInfo{SectorID: "s-2"}}
	h := NewControlHandler(deps)
	t.Cleanup(h.deps.LoopCancels.StopAll)

	doDevice := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/lotes/lote-1/aplicar-consigna", strings.NewReader(`{}`))
		req = req.WithContext(deviceauth.WithPrincipal(req.Context(), deviceauth.Principal{DeviceID: "dev-1"}))
		rec := httptest.NewRecorder()
		h.HandleAplicarConsigna()(rec, req)
		return rec
	}

	if rec := doDevice(); rec.Code != http.StatusForbidden {
		t.Fatalf("otro sector: status = %d, want 403", rec.Code)
	}
	if len(repo.aprobarCalls) != 0 {
		t.Fatalf("no debe aprobar con otro sector")
	}

	h.deps.DeviceSector = &fakeDeviceSector{info: &loteSectorService.SectorInfo{SectorID: "s-1"}}
	if rec := doDevice(); rec.Code != http.StatusOK {
		t.Fatalf("mismo sector: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.aprobarUser) != 1 || repo.aprobarUser[0] != "dispositivo:dev-1" {
		t.Fatalf("usuario de aprobación = %v, want dispositivo:dev-1", repo.aprobarUser)
	}
}

// ackEnvelope modela el envelope de POST /lotes/{id}/consigna/ack.
type ackEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		DecisionID  string `json:"decision_id"`
		Aplicado    bool   `json:"aplicado"`
		AplicadoPor string `json:"aplicado_por"`
	} `json:"data"`
}

const ackBody = `{"decision_id":"dec-1","temp_aplicado":180,"vel_aplicado":0.4}`

func newAckTestHandler(t *testing.T, lote *lotesector.Lote, repo *fakeDecisionesRepo, sector string) *ControlHandler {
	t.Helper()
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	deps.DecisionesRepo = repo
	deps.DeviceSector = &fakeDeviceSector{info: &loteSectorService.SectorInfo{SectorID: sector}}
	h := NewControlHandler(deps)
	t.Cleanup(h.deps.LoopCancels.StopAll)
	return h
}

func doAck(t *testing.T, h *ControlHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lotes/lote-1/consigna/ack", strings.NewReader(body))
	req = req.WithContext(deviceauth.WithPrincipal(req.Context(), deviceauth.Principal{DeviceID: "dev-1"}))
	rec := httptest.NewRecorder()
	h.HandleConsignaAck()(rec, req)
	return rec
}

func decodeAck(t *testing.T, rec *httptest.ResponseRecorder) ackEnvelope {
	t.Helper()
	var env ackEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	return env
}

// TestHandleConsignaAckRequiereDispositivo verifica el 401 sin principal.
func TestHandleConsignaAckRequiereDispositivo(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico}
	h := newAckTestHandler(t, lote, &fakeDecisionesRepo{}, "s-1")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/lotes/lote-1/consigna/ack", strings.NewReader(ackBody))
	rec := httptest.NewRecorder()
	h.HandleConsignaAck()(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestHandleConsignaAckValidaSector verifica el 403 cuando la Pi no es del
// sector del lote.
func TestHandleConsignaAckValidaSector(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico}
	repo := &fakeDecisionesRepo{porID: &decisiones_control.DecisionControl{ID: "dec-1", LoteID: "lote-1"}}
	h := newAckTestHandler(t, lote, repo, "s-2")
	if rec := doAck(t, h, ackBody); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(repo.marcarCalls) != 0 {
		t.Fatalf("no debe marcar con otro sector")
	}
}

// TestHandleConsignaAckSupervisionSinAprobar409 verifica que la Pi no puede
// aplicar una propuesta de SUPERVISION sin aprobación.
func TestHandleConsignaAckSupervisionSinAprobar409(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision}
	repo := &fakeDecisionesRepo{porID: &decisiones_control.DecisionControl{ID: "dec-1", LoteID: "lote-1"}}
	h := newAckTestHandler(t, lote, repo, "s-1")
	if rec := doAck(t, h, ackBody); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.marcarCalls) != 0 {
		t.Fatalf("no debe marcar una propuesta sin aprobar")
	}
}

// TestHandleConsignaAckAutomaticoMarcaMPC verifica el ack en AUTOMATICO:
// aplicado_por=MPC y se descarta la propuesta del loop.
func TestHandleConsignaAckAutomaticoMarcaMPC(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico}
	repo := &fakeDecisionesRepo{porID: &decisiones_control.DecisionControl{ID: "dec-1", LoteID: "lote-1"}}
	h := newAckTestHandler(t, lote, repo, "s-1")
	fl := &fakeLoop{}
	h.deps.LoopCancels.SetLoop("lote-1", fl)

	rec := doAck(t, h, ackBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	env := decodeAck(t, rec)
	if !env.Data.Aplicado || env.Data.DecisionID != "dec-1" || env.Data.AplicadoPor != decisiones_control.AplicadoPorMPC {
		t.Fatalf("data inesperada: %+v", env.Data)
	}
	if len(repo.marcarPor) != 1 || repo.marcarPor[0] != decisiones_control.AplicadoPorMPC {
		t.Fatalf("aplicado_por persistido = %v, want MPC", repo.marcarPor)
	}
	if len(fl.clearedIDs) != 1 || fl.clearedIDs[0] != "dec-1" {
		t.Fatalf("no se descartó la propuesta: %v", fl.clearedIDs)
	}
}

// TestHandleConsignaAckSupervisionMarcaOperario verifica el ack de una
// propuesta aprobada: aplicado_por=OPERARIO.
func TestHandleConsignaAckSupervisionMarcaOperario(t *testing.T) {
	aprobada := "sup@fermar.com.ar"
	lote := &lotesector.Lote{ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision}
	repo := &fakeDecisionesRepo{porID: &decisiones_control.DecisionControl{ID: "dec-1", LoteID: "lote-1", AprobadaPor: &aprobada}}
	h := newAckTestHandler(t, lote, repo, "s-1")

	rec := doAck(t, h, ackBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	env := decodeAck(t, rec)
	if env.Data.AplicadoPor != decisiones_control.AplicadoPorOperario {
		t.Fatalf("aplicado_por = %q, want OPERARIO", env.Data.AplicadoPor)
	}
	if len(repo.marcarPor) != 1 || repo.marcarPor[0] != decisiones_control.AplicadoPorOperario {
		t.Fatalf("aplicado_por persistido = %v, want OPERARIO", repo.marcarPor)
	}
}

// TestHandleConsignaAckIdempotente verifica que un ack repetido sigue dando 200.
func TestHandleConsignaAckIdempotente(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico}
	repo := &fakeDecisionesRepo{porID: &decisiones_control.DecisionControl{ID: "dec-1", LoteID: "lote-1"}}
	h := newAckTestHandler(t, lote, repo, "s-1")

	for i := 0; i < 2; i++ {
		if rec := doAck(t, h, ackBody); rec.Code != http.StatusOK {
			t.Fatalf("ack %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	if len(repo.marcarCalls) != 2 {
		t.Fatalf("MarcarAplicada calls = %d, want 2", len(repo.marcarCalls))
	}
}

// TestHandleConsignaAckBodyIncompleto400 verifica la validación del body.
func TestHandleConsignaAckBodyIncompleto400(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico}
	h := newAckTestHandler(t, lote, &fakeDecisionesRepo{}, "s-1")
	if rec := doAck(t, h, `{"decision_id":"dec-1","temp_aplicado":180}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// fakeModelos implementa ModeloStatus con resultado controlable.
type fakeModelos struct {
	entrenado bool
	err       error
	calls     int
}

func (f *fakeModelos) ModeloEntrenado(_ context.Context, _ string) (bool, error) {
	f.calls++
	return f.entrenado, f.err
}

// safeDecisionesRepo envuelve fakeDecisionesRepo con mutex: los loops reales
// reanudados escriben en paralelo mientras el test lee las filas.
type safeDecisionesRepo struct {
	mu sync.Mutex
	fakeDecisionesRepo
}

func (s *safeDecisionesRepo) Save(ctx context.Context, d *decisiones_control.DecisionControl) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeDecisionesRepo.Save(ctx, d)
}

func (s *safeDecisionesRepo) countRazon(razon string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, d := range s.fakeDecisionesRepo.saved {
		if d.RazonSkipped != nil && *d.RazonSkipped == razon {
			n++
		}
	}
	return n
}

// TestResumeLotesAbiertos verifica que la reanudación arranque en ADAPTANDO los
// lotes AUTOMATICO/SUPERVISION, saltee MANUAL y sea idempotente.
func TestResumeLotesAbiertos(t *testing.T) {
	repo := &fakeLoteRepoHandler{abiertos: []lotesector.Lote{
		{ID: "lote-auto", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico,
			ProductoID: "prod-1", VentanaEvaluacionSegundos: 3600, VentanaActualizacionSegundos: 3600},
		{ID: "lote-sup", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision,
			ProductoID: "prod-1", VentanaEvaluacionSegundos: 3600, VentanaActualizacionSegundos: 3600},
		{ID: "lote-manual", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoManual,
			ProductoID: "prod-1"},
	}}
	dec := &safeDecisionesRepo{}
	deps := validControlDeps()
	deps.LoteRepo = repo
	deps.DecisionesRepo = dec
	deps.ProductoRepo = &fakeProductoRepo{prod: &producto.Producto{ID: "prod-1"}}
	h := NewControlHandler(deps)
	t.Cleanup(h.deps.LoopCancels.StopAll)

	h.ResumeLotesAbiertos(context.Background())

	for _, id := range []string{"lote-auto", "lote-sup"} {
		v, ok := h.deps.LoopCancels.GetLoop(id)
		if !ok {
			t.Fatalf("el lote %s debe tener loop reanudado", id)
		}
		loop, ok := v.(*control.Loop)
		if !ok {
			t.Fatalf("loop de %s es %T", id, v)
		}
		if loop.FaseInicial() != control.FaseAdaptando {
			t.Fatalf("lote %s: fase inicial = %q, want ADAPTANDO", id, loop.FaseInicial())
		}
	}
	if _, ok := h.deps.LoopCancels.GetLoop("lote-manual"); ok {
		t.Fatalf("MANUAL no debe reanudar loop")
	}
	if got := dec.countRazon("reanudado_tras_reinicio"); got != 2 {
		t.Fatalf("auditorías = %d, want 2", got)
	}

	// Idempotente: una segunda pasada no re-arranca ni re-audita.
	h.ResumeLotesAbiertos(context.Background())
	if got := dec.countRazon("reanudado_tras_reinicio"); got != 2 {
		t.Fatalf("tras segunda pasada: auditorías = %d, want 2", got)
	}
	if repo.listCalls != 2 {
		t.Fatalf("ListAbiertos calls = %d, want 2", repo.listCalls)
	}
}

// TestHandleModeChangeAAutomaticoSinReceta422 verifica que AUTOMATICO exija la
// receta completa del producto.
func TestHandleModeChangeAAutomaticoSinReceta422(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoAbierto,
		Modo: lotesector.ModoManual, ProductoID: "prod-1"}
	repo := &fakeLoteRepoHandler{}
	h, srv, secret := newModeChangeHandler(t, lote, repo, &fakeDecisionesRepo{})
	h.deps.ProductoRepo = &fakeProductoRepo{prod: &producto.Producto{ID: "prod-1"}}

	status, _ := patchModo(t, srv, secret, user.RoleSupervisor, `{"modo":"AUTOMATICO"}`)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", status)
	}
	if repo.updateCalls != 0 {
		t.Fatalf("no debe persistir el modo sin receta")
	}
}

// TestHandleModeChangeAAutomaticoModelo cubre el gate del modelo entrenado:
// ausente → 422, predictor caído → 503, presente → 200, placeholder permitido.
func TestHandleModeChangeAAutomaticoModelo(t *testing.T) {
	cases := []struct {
		name        string
		modelos     *fakeModelos
		placeholder bool
		wantStatus  int
		wantCalls   int
	}{
		{"sin-modelo", &fakeModelos{entrenado: false}, false, http.StatusUnprocessableEntity, 1},
		{"predictor-caido", &fakeModelos{err: errors.New("down")}, false, http.StatusServiceUnavailable, 1},
		{"con-modelo", &fakeModelos{entrenado: true}, false, http.StatusOK, 1},
		{"placeholder-permitido", &fakeModelos{entrenado: false}, true, http.StatusOK, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.placeholder {
				t.Setenv("MPC_PERMITIR_PLACEHOLDER", "true")
			}
			lote := &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoAbierto,
				Modo: lotesector.ModoManual, ProductoID: "prod-1", VentanaEvaluacionSegundos: 600}
			repo := &fakeLoteRepoHandler{}
			h, srv, secret := newModeChangeHandler(t, lote, repo, &fakeDecisionesRepo{})
			h.deps.Modelos = tc.modelos

			status, _ := patchModo(t, srv, secret, user.RoleSupervisor, `{"modo":"AUTOMATICO"}`)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if tc.modelos.calls != tc.wantCalls {
				t.Fatalf("ModeloEntrenado calls = %d, want %d", tc.modelos.calls, tc.wantCalls)
			}
			wantUpdate := 0
			if tc.wantStatus == http.StatusOK {
				wantUpdate = 1
			}
			if repo.updateCalls != wantUpdate {
				t.Fatalf("UpdateModo calls = %d, want %d", repo.updateCalls, wantUpdate)
			}
		})
	}
}

// --- PATCH /lotes/{id}/control/parametros ---

// newTuningServer monta HandleTuning con el JWT middleware real.
func newTuningServer(t *testing.T, deps ControlDeps) (*ControlHandler, *httptest.Server, []byte) {
	t.Helper()
	h := NewControlHandler(deps)
	secret := []byte("tuning-test-secret-1234567890")
	srv := httptest.NewServer(authController.JWTMiddleware(secret, h.HandleTuning))
	t.Cleanup(srv.Close)
	t.Cleanup(h.deps.LoopCancels.StopAll)
	return h, srv, secret
}

func patchTuning(t *testing.T, srv *httptest.Server, secret []byte, role, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/lotes/lote-1/control/parametros", strings.NewReader(body))
	if err != nil {
		t.Fatalf("crear request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: signedTestToken(t, secret, role)})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH tuning: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestHandleTuningActualizaSinReiniciarLoop verifica el tuning parcial y que
// el loop vivo NO se reinicie: reiniciarlo en ADAPTANDO durante el
// precalentamiento podría proponer velocidad y arrancar la cinta antes de
// temperatura. El loop relee los límites por tick.
func TestHandleTuningActualizaSinReiniciarLoop(t *testing.T) {
	lote := &lotesector.Lote{
		ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico,
		ProductoID: "prod-1", VentanaEvaluacionSegundos: 3600, VentanaActualizacionSegundos: 3600,
	}
	repo := &fakeLoteRepoHandler{}
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	deps.LoteRepo = repo
	deps.ProductoRepo = &fakeProductoRepo{prod: &producto.Producto{ID: "prod-1"}}
	h, srv, secret := newTuningServer(t, deps)

	viejoCtx, viejoCancel := context.WithCancel(context.Background())
	defer viejoCancel()
	h.deps.LoopCancels.Register("lote-1", viejoCtx, viejoCancel)
	loopViejo := &fakeLoop{}
	h.deps.LoopCancels.SetLoop("lote-1", loopViejo)

	status := patchTuning(t, srv, secret, user.RoleSupervisor,
		`{"tolerancia_temp_c":1.5,"umbral_aceptacion_crudo":3,"umbral_aceptacion_quemado":4,"delta_temp1_max_c":4,"delta_vel_max":0.01}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if repo.paramCalls != 1 {
		t.Fatalf("UpdateParametrosControl calls = %d, want 1", repo.paramCalls)
	}
	p := repo.ultimosParams
	if p.ToleranciaTempC == nil || *p.ToleranciaTempC != 1.5 ||
		p.UmbralAceptacionCrudo == nil || *p.UmbralAceptacionCrudo != 3 ||
		p.UmbralAceptacionQuemado == nil || *p.UmbralAceptacionQuemado != 4 ||
		p.DeltaTemp1MaxC == nil || *p.DeltaTemp1MaxC != 4 ||
		p.DeltaVelMax == nil || *p.DeltaVelMax != 0.01 {
		t.Fatalf("parámetros mal propagados: %+v", p)
	}
	select {
	case <-viejoCtx.Done():
		t.Fatalf("el tuning no debe cancelar el loop vivo")
	default:
	}
	v, ok := h.deps.LoopCancels.GetLoop("lote-1")
	if !ok || v != loopViejo {
		t.Fatalf("el loop debe seguir siendo el mismo; got=%T", v)
	}
	if lote.ToleranciaTempC != 1.5 || lote.DeltaTemp1MaxC == nil || *lote.DeltaTemp1MaxC != 4 {
		t.Fatalf("el lote en memoria no refleja el tuning: %+v", lote)
	}
}

// TestHandleTuningValidacion verifica los 400 de rango/vacío y el 409 de lote
// cerrado, sin tocar el repo.
func TestHandleTuningValidacion(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		estado string
		want   int
	}{
		{"vacio", `{}`, lotesector.EstadoAbierto, http.StatusBadRequest},
		{"tolerancia-alta", `{"tolerancia_temp_c":25}`, lotesector.EstadoAbierto, http.StatusBadRequest},
		{"tolerancia-cero", `{"tolerancia_temp_c":0}`, lotesector.EstadoAbierto, http.StatusBadRequest},
		{"umbral-alto", `{"umbral_aceptacion_crudo":101}`, lotesector.EstadoAbierto, http.StatusBadRequest},
		{"umbral-negativo", `{"umbral_aceptacion_quemado":-1}`, lotesector.EstadoAbierto, http.StatusBadRequest},
		{"delta-temp", `{"delta_temp1_max_c":51}`, lotesector.EstadoAbierto, http.StatusBadRequest},
		{"delta-vel", `{"delta_vel_max":3}`, lotesector.EstadoAbierto, http.StatusBadRequest},
		{"lote-cerrado", `{"tolerancia_temp_c":1}`, lotesector.EstadoCerrado, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lote := &lotesector.Lote{ID: "lote-1", Estado: tc.estado, Modo: lotesector.ModoAutomatico, ProductoID: "prod-1"}
			repo := &fakeLoteRepoHandler{}
			deps := validControlDeps()
			deps.LoteLookup = &fakeLoteLookup{lote: lote}
			deps.LoteRepo = repo
			deps.ProductoRepo = &fakeProductoRepo{prod: &producto.Producto{ID: "prod-1"}}
			_, srv, secret := newTuningServer(t, deps)

			if got := patchTuning(t, srv, secret, user.RoleSupervisor, tc.body); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
			if repo.paramCalls != 0 {
				t.Fatalf("no debe persistir con body inválido/estado conflictivo")
			}
		})
	}
}

// TestHandleTuningRolInsuficiente403 verifica que un Operario no pueda hacer
// tuning.
func TestHandleTuningRolInsuficiente403(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico, ProductoID: "prod-1"}
	repo := &fakeLoteRepoHandler{}
	deps := validControlDeps()
	deps.LoteLookup = &fakeLoteLookup{lote: lote}
	deps.LoteRepo = repo
	_, srv, secret := newTuningServer(t, deps)

	if got := patchTuning(t, srv, secret, user.RoleOperario, `{"tolerancia_temp_c":1}`); got != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", got)
	}
	if repo.paramCalls != 0 {
		t.Fatalf("un rol insuficiente no debe persistir")
	}
}

// TestHandleConsignaPropuestaIncluyeLimites verifica que la propuesta exponga
// los límites efectivos (producto + override del lote) en ambos caminos.
func TestHandleConsignaPropuestaIncluyeLimites(t *testing.T) {
	dt, dv := 4.0, 0.01
	limitesLote := &lotesector.Lote{
		ID: "lote-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoSupervision,
		ProductoID: "prod-1", VentanaActualizacionSegundos: 30,
		ToleranciaTempC: 1.5, UmbralAceptacionCrudo: 3, UmbralAceptacionQuemado: 4,
		DeltaTemp1MaxC: &dt, DeltaVelMax: &dv,
	}
	prod := &producto.Producto{
		ID: "prod-1", TempMin: 150, TempMax: 250,
		VelocidadCintaMin: 0.1, VelocidadCintaMax: 0.4,
		DeltaTemp1MaxC: 10, DeltaVelMax: 0.05,
	}
	cases := []struct {
		name  string
		enMem bool
	}{
		{"snapshot", true},
		{"fallback", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := validControlDeps()
			deps.LoteLookup = &fakeLoteLookup{lote: limitesLote}
			deps.ProductoRepo = &fakeProductoRepo{prod: prod}
			dec := &fakeDecisionesRepo{pendiente: &decisiones_control.DecisionControl{
				ID: "dec-1", TempPropuesto: 180, VelPropuesto: 0.2,
				CreatedAt: time.Now().UTC(),
			}}
			deps.DecisionesRepo = dec
			h := NewControlHandler(deps)
			if tc.enMem {
				h.deps.LoopCancels.SetLoop("lote-1", &fakeLoop{snap: &control.PropuestaSnapshot{
					DecisionID: "dec-1", Temp1: 180, Velocidad: 0.2, Autorizada: true,
				}})
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/lotes/lote-1/consigna-propuesta", nil)
			h.HandleConsignaPropuesta(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			var env consignaEnvelope
			if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			lim := env.Data.Limites
			if lim == nil {
				t.Fatalf("la propuesta debe incluir limites; body=%s", rec.Body.String())
			}
			if lim.TempMin != 150 || lim.TempMax != 250 || lim.VelMin != 0.1 || lim.VelMax != 0.4 {
				t.Fatalf("rangos mal propagados: %+v", lim)
			}
			if lim.DeltaTemp1MaxC != 4 || lim.DeltaVelMax != 0.01 {
				t.Fatalf("overrides del lote mal propagados: %+v", lim)
			}
			if lim.ToleranciaTempC != 1.5 {
				t.Fatalf("tolerancia = %v, want 1.5", lim.ToleranciaTempC)
			}
		})
	}
}

// TestHandleConsignaAckActualizaSetpointsLote verifica el espejo best-effort de
// los setpoints confirmados en el ack.
func TestHandleConsignaAckActualizaSetpointsLote(t *testing.T) {
	lote := &lotesector.Lote{ID: "lote-1", SectorID: "s-1", Estado: lotesector.EstadoAbierto, Modo: lotesector.ModoAutomatico}
	repo := &fakeDecisionesRepo{porID: &decisiones_control.DecisionControl{ID: "dec-1", LoteID: "lote-1"}}
	h := newAckTestHandler(t, lote, repo, "s-1")
	loteRepo := h.deps.LoteRepo.(*fakeLoteRepoHandler)

	if rec := doAck(t, h, ackBody); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if loteRepo.setpointsCalls != 1 {
		t.Fatalf("UpdateSetpointsAplicados calls = %d, want 1", loteRepo.setpointsCalls)
	}
	if loteRepo.setpointsTemp != 180 || loteRepo.setpointsVel != 0.4 {
		t.Fatalf("setpoints persistidos = %v/%v, want 180/0.4", loteRepo.setpointsTemp, loteRepo.setpointsVel)
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
