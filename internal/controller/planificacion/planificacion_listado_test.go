package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/planificacion"
)

func TestListarPlanOK200(t *testing.T) {
	art := time.FixedZone("ART", -3*60*60)
	id := "11111111-1111-1111-1111-111111111111"
	svc := &fakePlanificacionService{planListado: planificacion.Listado[planificacion.PlanItem]{
		Total:    2,
		Truncada: true,
		Items: []planificacion.PlanItem{{
			ID: id, SectorID: "s1", SectorNombre: "Horno 1",
			ProductoID: "22222222-2222-2222-2222-222222222222", ProductoNombre: "Pan",
			Fecha: "2026-11-04", Turno: "mañana",
			InicioAt: time.Date(2026, 11, 4, 6, 0, 0, 0, art),
			FinAt:    time.Date(2026, 11, 4, 14, 0, 0, 0, art),
			KgPlan:   500,
		}},
	}}
	h := NewHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleSubtree(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/planificacion/plan?desde=2026-11-01&hasta=2026-11-04&sector_id=s1&limite=1", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Total    int                      `json:"total"`
			Truncada bool                     `json:"truncada"`
			Items    []planificacion.PlanItem `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.True(t, env.Success)
	assert.Equal(t, "Plan obtenido", env.Message)
	assert.Equal(t, 2, env.Data.Total)
	assert.True(t, env.Data.Truncada)
	require.Len(t, env.Data.Items, 1)
	assert.Equal(t, id, env.Data.Items[0].ID)
	assert.Equal(t, "-03:00", env.Data.Items[0].InicioAt.Format("-07:00"))
}

func TestListarParadasOK200(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{})

	for path, message := range map[string]string{
		"/api/v1/planificacion/paradas": "Paradas obtenidas",
	} {
		rec := httptest.NewRecorder()
		h.HandleSubtree(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, rec.Code, path)
		assert.Contains(t, rec.Body.String(), message)
	}
}

func TestListarValidacion400(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{listaErr: planificacion.ErrFechaInvalida})

	rec := httptest.NewRecorder()
	h.HandleSubtree(rec, httptest.NewRequest(http.MethodGet, "/api/v1/planificacion/plan?desde=mal", nil))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"success":false`)
}

func TestListarErrorInterno500(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{listaErr: errors.New("db caída")})

	rec := httptest.NewRecorder()
	h.HandleSubtree(rec, httptest.NewRequest(http.MethodGet, "/api/v1/planificacion/plan", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestEliminarOk200(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{})

	rec := httptest.NewRecorder()
	h.HandleSubtree(rec, httptest.NewRequest(http.MethodDelete,
		"/api/v1/planificacion/paradas/11111111-1111-1111-1111-111111111111", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"success":true,"message":"Registro eliminado"}`, rec.Body.String())
}

func TestEliminarIDInvalido400(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{delErr: planificacion.ErrIDInvalido})

	rec := httptest.NewRecorder()
	h.HandleSubtree(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/planificacion/paradas/no-uuid", nil))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestEliminarNoEncontrado404(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{delErr: planificacion.ErrNoEncontrado})

	rec := httptest.NewRecorder()
	h.HandleSubtree(rec, httptest.NewRequest(http.MethodDelete,
		"/api/v1/planificacion/plan/11111111-1111-1111-1111-111111111111", nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSubtreeMetodoNoPermitido405(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{})

	rec := httptest.NewRecorder()
	h.HandleSubtree(rec, httptest.NewRequest(http.MethodPut, "/api/v1/planificacion/plan", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestSubtreeRecursoInexistente404(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{})

	for _, path := range []string{
		"/api/v1/planificacion/otro",
		"/api/v1/planificacion/plan/11111111-1111-1111-1111-111111111111",
	} {
		rec := httptest.NewRecorder()
		h.HandleSubtree(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code, path)
	}
}
