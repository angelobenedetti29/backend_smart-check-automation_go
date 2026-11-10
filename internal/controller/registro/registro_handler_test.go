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
	"github.com/stretchr/testify/require"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/domain/registro"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
)

// fakeRegistry implementa la interfaz privada registry para aislar el handler.
type fakeRegistry struct {
	issueResp  *registro.IssueResponse
	issueErr   error
	issueCalls int
	issuedReq  registro.CreateRequest

	listResp []registro.RegistrationRequest
	listErr  error

	approveResp  *registro.Approval
	approveErr   error
	approveCalls int
	approveActor string
	approveID    string

	rejectErr   error
	rejectCalls int

	pickupResp *registro.Pickup
	pickupErr  error
	pickupID   string
}

func (f *fakeRegistry) Issue(_ context.Context, req registro.CreateRequest) (*registro.IssueResponse, error) {
	f.issueCalls++
	f.issuedReq = req
	return f.issueResp, f.issueErr
}

func (f *fakeRegistry) List(context.Context) ([]registro.RegistrationRequest, error) {
	return f.listResp, f.listErr
}

func (f *fakeRegistry) Approve(_ context.Context, actor, requestID string) (*registro.Approval, error) {
	f.approveCalls++
	f.approveActor = actor
	f.approveID = requestID
	return f.approveResp, f.approveErr
}

func (f *fakeRegistry) Reject(_ context.Context, _, _ string) error {
	f.rejectCalls++
	return f.rejectErr
}

func (f *fakeRegistry) Pickup(_ context.Context, requestID string) (*registro.Pickup, error) {
	f.pickupID = requestID
	return f.pickupResp, f.pickupErr
}

const handlerTestJWTSecret = "test-secret-32-chars-exactly!!!"

// signedRegistrationCookie firma un JWT válido para las rutas de panel.
func signedRegistrationCookie(t *testing.T, email string) *http.Cookie {
	t.Helper()
	claims := &authService.Claims{
		Email: email,
		Name:  "Supervisor",
		Role:  "Supervisor",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "smart-check-automation",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(handlerTestJWTSecret))
	require.NoError(t, err)
	return &http.Cookie{Name: "session_token", Value: signed}
}

func decodeBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	return body
}

func TestHandleCreate_FlatCreated(t *testing.T) {
	fake := &fakeRegistry{issueResp: &registro.IssueResponse{RequestID: validHandlerID(), Status: registro.StatusPending}}
	h := NewHandler(fake)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/registration-requests", strings.NewReader(`{"hostname":"rpi-01","type":"ENTRADA_HORNO"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleCreate(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	require.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	body := decodeBody(t, rr)
	require.Equal(t, validHandlerID(), body["request_id"])
	require.Equal(t, registro.StatusPending, body["status"])
	require.NotContains(t, body, "success", "la respuesta del nodo es plana, sin envelope")
	require.Equal(t, 1, fake.issueCalls)
	require.Equal(t, "ENTRADA_HORNO", fake.issuedReq.Type)
}

func TestHandleCreate_MetodoNoPost_405(t *testing.T) {
	h := NewHandler(&fakeRegistry{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/registration-requests", nil)
	rr := httptest.NewRecorder()
	h.HandleCreate(rr, req)
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleCreate_ContentTypeInvalido_415(t *testing.T) {
	h := NewHandler(&fakeRegistry{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/registration-requests", strings.NewReader(`{"hostname":"rpi-01","type":"ENTRADA_HORNO"}`))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()
	h.HandleCreate(rr, req)
	require.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
}

func TestHandleCreate_HostnameInvalido_400(t *testing.T) {
	fake := &fakeRegistry{}
	h := NewHandler(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/registration-requests", strings.NewReader(`{"hostname":"  ","type":"ENTRADA_HORNO"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleCreate(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Zero(t, fake.issueCalls)
}

func TestHandleCreate_SinType_400(t *testing.T) {
	fake := &fakeRegistry{}
	h := NewHandler(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/registration-requests", strings.NewReader(`{"hostname":"rpi-01"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleCreate(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Zero(t, fake.issueCalls)
}

func TestHandleCreate_TypeInvalido_400(t *testing.T) {
	fake := &fakeRegistry{}
	h := NewHandler(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/registration-requests", strings.NewReader(`{"hostname":"rpi-01","type":"HORNO"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleCreate(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	body := decodeBody(t, rr)
	require.Contains(t, body["error"], "type: debe ser ENTRADA_HORNO o SALIDA_HORNO")
	require.Zero(t, fake.issueCalls)
}

func TestHandleList_EnvelopeConNoStore(t *testing.T) {
	fake := &fakeRegistry{listResp: []registro.RegistrationRequest{{RequestID: validHandlerID(), Hostname: "rpi-01", Status: registro.StatusPending}}}
	h := NewHandler(fake)
	rr := httptest.NewRecorder()
	h.HandleList(rr, httptest.NewRequest(http.MethodGet, "/api/v1/registration-requests?status=PENDING", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	body := decodeBody(t, rr)
	require.Equal(t, true, body["success"])
	require.NotEmpty(t, body["message"])
	data, ok := body["data"].([]interface{})
	require.True(t, ok, "data debe ser un arreglo")
	require.Len(t, data, 1)
}

func TestHandlePickup_FlatOK(t *testing.T) {
	device := "11111111-1111-1111-1111-111111111111"
	secret := "secreto"
	tipo := "ENTRADA_HORNO"
	fake := &fakeRegistry{pickupResp: &registro.Pickup{Status: registro.StatusApproved, DeviceID: &device, Secret: &secret, Type: &tipo}}
	h := NewHandler(fake)

	rr := httptest.NewRecorder()
	h.HandlePickup(rr, httptest.NewRequest(http.MethodGet, "/api/v1/registration-requests/"+validHandlerID(), nil))

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	body := decodeBody(t, rr)
	require.Equal(t, registro.StatusApproved, body["status"])
	require.Equal(t, device, body["device_id"])
	require.Equal(t, secret, body["secret"])
	require.Equal(t, tipo, body["type"])
	require.NotContains(t, body, "success")
	require.Equal(t, validHandlerID(), fake.pickupID)
}

func TestHandlePickup_NoEncontrado_Flat404(t *testing.T) {
	// Path inválido: no llega al service.
	invalid := &fakeRegistry{}
	rr := httptest.NewRecorder()
	NewHandler(invalid).HandlePickup(rr, httptest.NewRequest(http.MethodGet, "/api/v1/registration-requests/", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)
	require.Empty(t, invalid.pickupID)

	// El service devuelve ErrRequestNotFound.
	h := NewHandler(&fakeRegistry{pickupErr: registro.ErrRequestNotFound})
	rr2 := httptest.NewRecorder()
	h.HandlePickup(rr2, httptest.NewRequest(http.MethodGet, "/api/v1/registration-requests/"+validHandlerID(), nil))

	require.Equal(t, http.StatusNotFound, rr2.Code)
	body := decodeBody(t, rr2)
	require.Equal(t, "Solicitud de registro no encontrada", body["error"])
}

func TestHandleApprove_SinClaims_401(t *testing.T) {
	fake := &fakeRegistry{}
	h := NewHandler(fake)
	rr := httptest.NewRecorder()
	h.HandleApprove(rr, httptest.NewRequest(http.MethodPost, "/api/v1/registration-requests/"+validHandlerID()+"/approve", nil))

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	body := decodeBody(t, rr)
	require.Equal(t, false, body["success"])
	require.Zero(t, fake.approveCalls)
}

func TestHandleApprove_NoFiltraSecret(t *testing.T) {
	fake := &fakeRegistry{approveResp: &registro.Approval{RequestID: validHandlerID(), Status: registro.StatusApproved, DeviceID: "dev-1"}}
	h := NewHandler(fake)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/registration-requests/"+validHandlerID()+"/approve", nil)
	req.AddCookie(signedRegistrationCookie(t, "sup@fermar.com.ar"))
	rr := httptest.NewRecorder()
	authController.JWTMiddleware([]byte(handlerTestJWTSecret), h.HandleApprove)(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, fake.approveCalls)
	require.Equal(t, "sup@fermar.com.ar", fake.approveActor)
	body := decodeBody(t, rr)
	require.Equal(t, true, body["success"])
	data, ok := body["data"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "dev-1", data["device_id"])
	require.NotContains(t, data, "secret")
	require.NotContains(t, rr.Body.String(), "secret")
}

func TestHandleReject_Exito(t *testing.T) {
	fake := &fakeRegistry{}
	h := NewHandler(fake)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/registration-requests/"+validHandlerID()+"/reject", nil)
	req.AddCookie(signedRegistrationCookie(t, "sup@fermar.com.ar"))
	rr := httptest.NewRecorder()
	authController.JWTMiddleware([]byte(handlerTestJWTSecret), h.HandleReject)(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, 1, fake.rejectCalls)
	body := decodeBody(t, rr)
	require.Equal(t, true, body["success"])
	data, ok := body["data"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, validHandlerID(), data["requestId"])
	require.Equal(t, registro.StatusRejected, data["status"])
}

func TestRequestIDFromPath(t *testing.T) {
	id := validHandlerID()
	cases := []struct {
		name   string
		path   string
		suffix string
		want   string
	}{
		{"pickup", "/api/v1/registration-requests/" + id, "", id},
		{"approve", "/api/v1/registration-requests/" + id + "/approve", "/approve", id},
		{"reject", "/api/v1/registration-requests/" + id + "/reject", "/reject", id},
		{"prefijo ajeno", "/api/otra/" + id, "", ""},
		{"vacío", "/api/v1/registration-requests/", "", ""},
		{"barra extra", "/api/v1/registration-requests/a/b", "", ""},
		{"sufijo faltante", "/api/v1/registration-requests/" + id, "/approve", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, requestIDFromPath(tc.path, tc.suffix))
		})
	}
}

// validHandlerID devuelve un request_id con formato válido.
func validHandlerID() string { return "req_" + strings.Repeat("b", 43) }
