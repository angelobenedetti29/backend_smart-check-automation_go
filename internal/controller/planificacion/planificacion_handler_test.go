package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/planificacion"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
	service "github.com/angelobenedetti29/smart-check-automation/internal/service/planificacion"
)

// fakePlanificacionService es un doble configurable de las importaciones.
type fakePlanificacionService struct {
	res planificacion.ImportResult
	err error

	planListado planificacion.Listado[planificacion.PlanItem]
	listaErr    error
	delErr      error
}

func (f *fakePlanificacionService) ImportarPlan(context.Context, io.Reader) (planificacion.ImportResult, error) {
	return f.res, f.err
}

func (f *fakePlanificacionService) ImportarParadas(context.Context, io.Reader) (planificacion.ImportResult, error) {
	return f.res, f.err
}

func (f *fakePlanificacionService) ListarPlan(context.Context, service.Query) (planificacion.Listado[planificacion.PlanItem], error) {
	return f.planListado, f.listaErr
}

func (f *fakePlanificacionService) ListarParadas(context.Context, service.Query) (planificacion.Listado[planificacion.ParadaItem], error) {
	return planificacion.Listado[planificacion.ParadaItem]{Items: []planificacion.ParadaItem{}}, f.listaErr
}

func (f *fakePlanificacionService) EliminarPlan(context.Context, string) error {
	return f.delErr
}

func (f *fakePlanificacionService) EliminarParada(context.Context, string) error {
	return f.delErr
}

// multipartCSV arma un POST multipart con el CSV en el campo `file`.
func multipartCSV(t *testing.T, field, contenido string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile(field, "import.csv")
	require.NoError(t, err)
	_, err = part.Write([]byte(contenido))
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/planificacion/plan/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestImportPlanOK200(t *testing.T) {
	svc := &fakePlanificacionService{res: planificacion.ImportResult{
		Procesadas: 2, Insertadas: 1, Actualizadas: 1,
		Errores: []planificacion.ErrorImportacion{{Fila: 3, Mensaje: "sector_id inexistente"}},
	}}
	h := NewHandler(svc)

	rec := httptest.NewRecorder()
	h.HandlePlanImport(rec, multipartCSV(t, "file", "sector_id\ns1\n"))

	require.Equal(t, http.StatusOK, rec.Code)
	var env struct {
		Success bool                       `json:"success"`
		Message string                     `json:"message"`
		Data    planificacion.ImportResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.True(t, env.Success)
	assert.Equal(t, "Plan importado", env.Message)
	assert.Equal(t, 2, env.Data.Procesadas)
	assert.Equal(t, 1, env.Data.Insertadas)
	assert.Equal(t, 1, env.Data.Actualizadas)
	require.Len(t, env.Data.Errores, 1)
	assert.Equal(t, 3, env.Data.Errores[0].Fila)
}

func TestImportPlanSinArchivo400(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("otro", "x"))
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/planificacion/plan/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	rec := httptest.NewRecorder()
	h.HandlePlanImport(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestImportPlanHeaderInvalido400(t *testing.T) {
	svc := &fakePlanificacionService{err: planificacion.ErrFormatoCSV}
	h := NewHandler(svc)

	rec := httptest.NewRecorder()
	h.HandlePlanImport(rec, multipartCSV(t, "file", "sector_id,fecha\n"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestImportPlanMetodoNoPermitido405(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{})

	rec := httptest.NewRecorder()
	h.HandlePlanImport(rec, httptest.NewRequest(http.MethodGet, "/api/v1/planificacion/plan/import", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestImportPlanArchivoMayorA5MB400(t *testing.T) {
	h := NewHandler(&fakePlanificacionService{})
	contenido := strings.Repeat("a", maxImportFileBytes+1)

	rec := httptest.NewRecorder()
	h.HandlePlanImport(rec, multipartCSV(t, "file", contenido))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestImportPlanRolOperario403(t *testing.T) {
	secret := []byte("test-secret-32-chars-exactly!!!")
	claims := &authService.Claims{
		Email: "operario@fermar.com.ar",
		Role:  "Operario",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
	require.NoError(t, err)

	h := NewHandler(&fakePlanificacionService{})
	wrapped := authController.JWTMiddleware(secret,
		authController.RequireRole([]string{"Supervisor", "Administrador"}, h.HandlePlanImport))

	req := multipartCSV(t, "file", "sector_id\ns1\n")
	req.AddCookie(&http.Cookie{Name: "session_token", Value: token})
	rec := httptest.NewRecorder()
	wrapped(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}
