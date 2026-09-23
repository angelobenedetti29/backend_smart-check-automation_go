package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	lotesector "github.com/angelobenedetti29/smart-check-automation/internal/domain/lote_sector"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/producto"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
	loteSectorService "github.com/angelobenedetti29/smart-check-automation/internal/service/lote_sector"
)

// --- Fake service ----------------------------------------------------------

// testLoteUUID es un id de lote canónico para paths y cursores.
const testLoteUUID = "d1a2b3c4-d5e6-4789-8abc-def012345678"

type fakeService struct {
	sectorInfo *loteSectorService.SectorInfo
	sectorErr  error

	sectores    []sector.Sector
	sectoresErr error

	abrirLote   *lotesector.Lote
	abrirCreado bool
	abrirErr    error

	abiertoLote   *lotesector.Lote
	abiertoErr    error
	abiertoSector string

	regAceptados  int
	regDuplicados int
	regLote       *lotesector.Lote
	regErr        error

	cerrarLote    *lotesector.Lote
	cerrarErr     error
	cerrarMotivo  string
	cerrarConteos lotesector.Conteos

	histLotes  []lotesector.Lote
	histTotal  int
	histErr    error
	histSector string
	histLimite int
	histCursor string
}

func (f *fakeService) SectorDelDispositivo(context.Context, string) (*loteSectorService.SectorInfo, error) {
	return f.sectorInfo, f.sectorErr
}

func (f *fakeService) Sectores(context.Context) ([]sector.Sector, error) {
	return f.sectores, f.sectoresErr
}

func (f *fakeService) Abrir(context.Context, string, string, string) (*lotesector.Lote, bool, error) {
	return f.abrirLote, f.abrirCreado, f.abrirErr
}

func (f *fakeService) Abierto(_ context.Context, sectorID string) (*lotesector.Lote, error) {
	f.abiertoSector = sectorID
	return f.abiertoLote, f.abiertoErr
}

func (f *fakeService) RegistrarEventos(context.Context, string, string, []lotesector.Evento) (int, int, *lotesector.Lote, error) {
	return f.regAceptados, f.regDuplicados, f.regLote, f.regErr
}

func (f *fakeService) Cerrar(_ context.Context, _, _, motivo string, conteos lotesector.Conteos, _ string) (*lotesector.Lote, error) {
	f.cerrarMotivo = motivo
	f.cerrarConteos = conteos
	return f.cerrarLote, f.cerrarErr
}

func (f *fakeService) Historial(_ context.Context, sectorID, _ string, limite int, cursor string) ([]lotesector.Lote, int, error) {
	f.histSector = sectorID
	f.histLimite = limite
	f.histCursor = cursor
	return f.histLotes, f.histTotal, f.histErr
}

// --- Helpers ---------------------------------------------------------------

func deviceRequest(method, target, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	return r.WithContext(deviceauth.WithPrincipal(r.Context(), deviceauth.Principal{DeviceID: "dev-1"}))
}

func oauthRequest(t *testing.T, method, target, body string, secret []byte) *http.Request {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	r.AddCookie(&http.Cookie{Name: "session_token", Value: signSessionToken(t, secret)})
	return r
}

func signSessionToken(t *testing.T, secret []byte) string {
	t.Helper()
	claims := authService.Claims{
		Email: "supervisor@fermar.com.ar",
		Name:  "Supervisor",
		Role:  "Supervisor",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	require.NoError(t, err)
	return token
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Errors map[string]string `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	return env.Errors["code"]
}

// --- Inicio ----------------------------------------------------------------

func TestHandleInicioDeviceCrea201(t *testing.T) {
	svc := &fakeService{abrirLote: &lotesector.Lote{ID: testLoteUUID}, abrirCreado: true}
	h := NewLoteSectorHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleInicio(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/inicio", `{"idempotency_key":"k","producto_id":"prod-1"}`))

	require.Equal(t, http.StatusCreated, rec.Code)
	var env struct {
		Data struct {
			Creado bool             `json:"creado"`
			Lote   *lotesector.Lote `json:"lote"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.True(t, env.Data.Creado)
	require.NotNil(t, env.Data.Lote)
	assert.Equal(t, testLoteUUID, env.Data.Lote.ID)
}

func TestHandleInicioDeviceAdjunta200(t *testing.T) {
	svc := &fakeService{abrirLote: &lotesector.Lote{ID: testLoteUUID}, abrirCreado: false}
	h := NewLoteSectorHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleInicio(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/inicio", `{"idempotency_key":"k","producto_id":"prod-1"}`))

	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Data struct {
			Creado bool `json:"creado"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.False(t, env.Data.Creado)
}

func TestHandleInicioProductoDesconocido422(t *testing.T) {
	svc := &fakeService{abrirErr: producto.ErrDesconocido}
	h := NewLoteSectorHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleInicio(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/inicio", `{"idempotency_key":"k","producto_id":"nope"}`))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "producto_desconocido", errorCode(t, rec))
}

func TestHandleInicioJSONInvalido400(t *testing.T) {
	h := NewLoteSectorHandler(&fakeService{})
	rec := httptest.NewRecorder()
	h.HandleInicio(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/inicio", `{"producto_id":`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleInicioSinPrincipal401(t *testing.T) {
	h := NewLoteSectorHandler(&fakeService{})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/lotes/inicio", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.HandleInicio(rec, r)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// --- Abierto ---------------------------------------------------------------

func TestHandleAbiertoDeviceNullEs200(t *testing.T) {
	svc := &fakeService{sectorInfo: &loteSectorService.SectorInfo{SectorID: "sector-1"}, abiertoLote: nil}
	h := NewLoteSectorHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleAbierto(rec, deviceRequest(http.MethodGet, "/api/v1/lotes/abierto", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Data struct {
			Lote *lotesector.Lote `json:"lote"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Nil(t, env.Data.Lote)
	assert.Contains(t, rec.Body.String(), `"lote":null`)
}

func TestHandleAbiertoOAuthSinSectorID400(t *testing.T) {
	secret := []byte("test-secret")
	h := NewLoteSectorHandler(&fakeService{})
	wrapped := authController.JWTMiddleware(secret, h.HandleAbierto)

	rec := httptest.NewRecorder()
	wrapped(rec, oauthRequest(t, http.MethodGet, "/api/v1/lotes/abierto", "", secret))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "payload_invalido", errorCode(t, rec))
}

func TestHandleAbiertoOAuthConSectorID(t *testing.T) {
	secret := []byte("test-secret")
	svc := &fakeService{abiertoLote: &lotesector.Lote{ID: testLoteUUID, SectorID: "sector-9"}}
	h := NewLoteSectorHandler(svc)
	wrapped := authController.JWTMiddleware(secret, h.HandleAbierto)

	rec := httptest.NewRecorder()
	wrapped(rec, oauthRequest(t, http.MethodGet, "/api/v1/lotes/abierto?sector_id=sector-9", "", secret))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "sector-9", svc.abiertoSector)
}

// --- Eventos ---------------------------------------------------------------

func TestHandleEventosErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		status   int
		wantCode string
	}{
		{"demasiados", lotesector.ErrDemasiadosEventos, http.StatusRequestEntityTooLarge, "demasiados_eventos"},
		{"ajeno", lotesector.ErrAjeno, http.StatusForbidden, "lote_ajeno"},
		{"no-encontrado", lotesector.ErrNotFound, http.StatusNotFound, "lote_no_encontrado"},
		{"cerrado", lotesector.ErrCerrado, http.StatusConflict, "lote_cerrado"},
		{"producto-inconsistente", lotesector.ErrProductoInconsistente, http.StatusUnprocessableEntity, "producto_inconsistente"},
		{"payload", lotesector.ErrPayloadInvalido, http.StatusBadRequest, "payload_invalido"},
		{"sin-sector", sector.ErrSinSector, http.StatusNotFound, "sin_sector"},
	}
	body := `{"eventos":[{"evento_id":"c1a2b3c4-d5e6-4789-8abc-def012345678","producto_id":"prod-1"}]}`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewLoteSectorHandler(&fakeService{regErr: tc.err})
			rec := httptest.NewRecorder()
			h.HandleEventos(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/"+testLoteUUID+"/eventos", body))

			require.Equal(t, tc.status, rec.Code)
			assert.Equal(t, tc.wantCode, errorCode(t, rec))
		})
	}
}

func TestHandleEventosOK(t *testing.T) {
	svc := &fakeService{regAceptados: 2, regDuplicados: 1, regLote: &lotesector.Lote{ID: testLoteUUID}}
	h := NewLoteSectorHandler(svc)
	body := `{"eventos":[{"evento_id":"c1a2b3c4-d5e6-4789-8abc-def012345678","producto_id":"prod-1"}]}`

	rec := httptest.NewRecorder()
	h.HandleEventos(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/"+testLoteUUID+"/eventos", body))

	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Data struct {
			Aceptados  int `json:"aceptados"`
			Duplicados int `json:"duplicados"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, 2, env.Data.Aceptados)
	assert.Equal(t, 1, env.Data.Duplicados)
}

func TestHandleEventosIDVacio404(t *testing.T) {
	h := NewLoteSectorHandler(&fakeService{})
	rec := httptest.NewRecorder()
	h.HandleEventos(rec, deviceRequest(http.MethodPost, "/api/v1/lotes//eventos", `{"eventos":[]}`))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// M3: un lote_id no-UUID en el path es 404, nunca 500 por 22P02.
func TestHandleEventosNonUUIDLoteID404(t *testing.T) {
	h := NewLoteSectorHandler(&fakeService{})
	rec := httptest.NewRecorder()
	h.HandleEventos(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/not-a-uuid/eventos", `{"eventos":[]}`))
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "lote_no_encontrado", errorCode(t, rec))
}

// --- Cierre ----------------------------------------------------------------

func TestHandleCierreSectorActivo409(t *testing.T) {
	svc := &fakeService{cerrarErr: lotesector.ErrSectorActivo}
	h := NewLoteSectorHandler(svc)
	body := `{"idempotency_key":"k","motivo":"manual","conteos":{"ok":3,"quemado":1,"total":4}}`

	rec := httptest.NewRecorder()
	h.HandleCierre(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/"+testLoteUUID+"/cierre", body))

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "sector_activo", errorCode(t, rec))
}

func TestHandleCierreOK(t *testing.T) {
	svc := &fakeService{cerrarLote: &lotesector.Lote{ID: testLoteUUID, Estado: lotesector.EstadoCerrado}}
	h := NewLoteSectorHandler(svc)
	body := `{"idempotency_key":"k","motivo":"sin_detecciones","conteos":{"ok":3,"crudo":null,"quemado":1,"total":4}}`

	rec := httptest.NewRecorder()
	h.HandleCierre(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/"+testLoteUUID+"/cierre", body))

	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Data struct {
			Cerrado bool `json:"cerrado"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.True(t, env.Data.Cerrado)
	assert.Equal(t, "sin_detecciones", svc.cerrarMotivo)
	require.NotNil(t, svc.cerrarConteos.OK)
	assert.Equal(t, 3, *svc.cerrarConteos.OK)
	assert.Nil(t, svc.cerrarConteos.Crudo)
	assert.Equal(t, 4, svc.cerrarConteos.Total)
}

// M3: un lote_id no-UUID en el path de cierre es 404, nunca 500 por 22P02.
func TestHandleCierreNonUUIDLoteID404(t *testing.T) {
	h := NewLoteSectorHandler(&fakeService{})
	rec := httptest.NewRecorder()
	h.HandleCierre(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/not-a-uuid/cierre", `{}`))
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "lote_no_encontrado", errorCode(t, rec))
}

// M4: un cierre sin conteos no debe pisar los conteos vivos con NULL/0.
func TestHandleCierreSinConteos400(t *testing.T) {
	h := NewLoteSectorHandler(&fakeService{})
	rec := httptest.NewRecorder()
	h.HandleCierre(rec, deviceRequest(http.MethodPost, "/api/v1/lotes/"+testLoteUUID+"/cierre", `{"idempotency_key":"k","motivo":"manual"}`))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "payload_invalido", errorCode(t, rec))
}

// --- Historial -------------------------------------------------------------

func TestHandleHistorialDeviceIncluyeSiguienteCursor(t *testing.T) {
	inicio := time.Now().UTC().Truncate(time.Microsecond)
	svc := &fakeService{
		sectorInfo: &loteSectorService.SectorInfo{SectorID: "sector-1"},
		histLotes:  []lotesector.Lote{{ID: testLoteUUID, AbiertoEn: inicio}},
		histTotal:  5,
	}
	h := NewLoteSectorHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleHistorial(rec, deviceRequest(http.MethodGet, "/api/v1/lotes?limite=1", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "sector-1", svc.histSector)
	assert.Equal(t, 1, svc.histLimite)

	var env struct {
		Total           int    `json:"total"`
		Page            int    `json:"page"`
		PageSize        int    `json:"pageSize"`
		SiguienteCursor string `json:"siguiente_cursor"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, 5, env.Total)
	assert.Equal(t, 1, env.Page)
	require.NotEmpty(t, env.SiguienteCursor)

	gotTime, gotID, err := loteSectorService.DecodeCursor(env.SiguienteCursor)
	require.NoError(t, err)
	assert.True(t, gotTime.Equal(inicio))
	assert.Equal(t, testLoteUUID, gotID)
}

func TestHandleHistorialSinCursorCuandoPaginaIncompleta(t *testing.T) {
	svc := &fakeService{
		sectorInfo: &loteSectorService.SectorInfo{SectorID: "sector-1"},
		histLotes:  []lotesector.Lote{{ID: testLoteUUID}},
	}
	h := NewLoteSectorHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleHistorial(rec, deviceRequest(http.MethodGet, "/api/v1/lotes?limite=20", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "siguiente_cursor")
}

func TestHandleHistorialCursorMalformado400(t *testing.T) {
	svc := &fakeService{sectorInfo: &loteSectorService.SectorInfo{SectorID: "sector-1"}, histErr: lotesector.ErrPayloadInvalido}
	h := NewLoteSectorHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleHistorial(rec, deviceRequest(http.MethodGet, "/api/v1/lotes?antes_de=%%%", ""))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "payload_invalido", errorCode(t, rec))
}

// M3: un producto_id no-UUID en el historial es 400, nunca 500 por 22P02.
func TestHandleHistorialNonUUIDProducto400(t *testing.T) {
	svc := &fakeService{sectorInfo: &loteSectorService.SectorInfo{SectorID: "sector-1"}}
	h := NewLoteSectorHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleHistorial(rec, deviceRequest(http.MethodGet, "/api/v1/lotes?producto_id=no-uuid", ""))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "payload_invalido", errorCode(t, rec))
}

func TestHandleHistorialOAuthSinSectorID400(t *testing.T) {
	secret := []byte("test-secret")
	h := NewLoteSectorHandler(&fakeService{})
	wrapped := authController.JWTMiddleware(secret, h.HandleHistorial)

	rec := httptest.NewRecorder()
	wrapped(rec, oauthRequest(t, http.MethodGet, "/api/v1/lotes", "", secret))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- Sector ----------------------------------------------------------------

func TestHandleSectorDevice(t *testing.T) {
	svc := &fakeService{sectorInfo: &loteSectorService.SectorInfo{
		SectorID: "sector-1",
		Nombre:   "Horno 1",
		Tipo:     "ENTRADA_HORNO",
		Companeros: []sector.Companero{
			{DeviceID: "dev-2", Hostname: "pi-salida", Tipo: "SALIDA_HORNO"},
		},
	}}
	h := NewLoteSectorHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleSector(rec, deviceRequest(http.MethodGet, "/api/v1/dispositivos/sector", ""))

	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Data struct {
			SectorID string `json:"sector_id"`
			Nombre   string `json:"nombre"`
			Tipo     string `json:"tipo"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, "sector-1", env.Data.SectorID)
	assert.Equal(t, "Horno 1", env.Data.Nombre)
	assert.Equal(t, "ENTRADA_HORNO", env.Data.Tipo)
}

func TestHandleSectorSinSector404(t *testing.T) {
	svc := &fakeService{sectorErr: sector.ErrSinSector}
	h := NewLoteSectorHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleSector(rec, deviceRequest(http.MethodGet, "/api/v1/dispositivos/sector", ""))

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "sin_sector", errorCode(t, rec))
}

// --- Sectores --------------------------------------------------------------

func TestHandleSectoresOAuth(t *testing.T) {
	secret := []byte("test-secret")
	svc := &fakeService{sectores: []sector.Sector{
		{ID: "sector-1", Nombre: "Horno 1"},
		{ID: "sector-2", Nombre: "Horno 2"},
	}}
	h := NewLoteSectorHandler(svc)
	wrapped := authController.JWTMiddleware(secret, h.HandleSectores)

	rec := httptest.NewRecorder()
	wrapped(rec, oauthRequest(t, http.MethodGet, "/api/v1/sectores", "", secret))

	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Data []sector.Sector `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Len(t, env.Data, 2)
	assert.Equal(t, "sector-1", env.Data[0].ID)
	assert.Equal(t, "Horno 1", env.Data[0].Nombre)
}

func TestHandleSectoresMetodoNoPermitido405(t *testing.T) {
	h := NewLoteSectorHandler(&fakeService{})
	rec := httptest.NewRecorder()
	h.HandleSectores(rec, httptest.NewRequest(http.MethodPost, "/api/v1/sectores", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
