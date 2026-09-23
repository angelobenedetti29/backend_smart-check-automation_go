package devicetoken

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/angelobenedetti29/smart-check-automation/internal/deviceauth"
	"github.com/angelobenedetti29/smart-check-automation/internal/guard"
)

// stubSecretStore resuelve un device id fijo para cualquier hash.
type stubSecretStore struct {
	id  string
	err error
}

func (s stubSecretStore) LookupActiveBySecretHash(context.Context, string) (string, error) {
	return s.id, s.err
}

// recordingHandler registra si fue invocado y el principal recibido.
type recordingHandler struct {
	called    bool
	principal deviceauth.Principal
	hasPrin   bool
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	h.principal, h.hasPrin = deviceauth.PrincipalFromContext(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func newVerifier(id string) *deviceauth.Verifier {
	return &deviceauth.Verifier{Store: stubSecretStore{id: id}}
}

func TestBeforeMuxPassesThroughNonProtectedRequests(t *testing.T) {
	cases := map[string]*http.Request{
		"get-protected-path": httptest.NewRequest(http.MethodGet, "/api/v1/lotes", nil),
		"post-unknown-path":  httptest.NewRequest(http.MethodPost, "/api/v1/otra", nil),
		"put-protected-path": httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos/ping", nil),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			next := &recordingHandler{}
			// Un store vacío probaría que no se autentica: si se intentara, daría 401.
			BeforeMux(&deviceauth.Verifier{Store: stubSecretStore{id: ""}}, nil, next).ServeHTTP(httptest.NewRecorder(), r)
			assert.True(t, next.called)
		})
	}
}

func TestBeforeMuxRejectsMissingOrMalformedAuthorization(t *testing.T) {
	cases := map[string]string{
		"missing":      "",
		"wrong-scheme": "Basic abc",
		"opaque":       "abc123",
		"no-token":     "Bearer",
		"extra-part":   "Bearer abc def",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			next := &recordingHandler{}
			r := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/ping", nil)
			if header != "" {
				r.Header.Set("Authorization", header)
			}
			rec := httptest.NewRecorder()
			BeforeMux(newVerifier("dev-1"), nil, next).ServeHTTP(rec, r)

			assert.False(t, next.called)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
			assert.Equal(t, invalidTokenBody, rec.Body.String())
		})
	}
}

func TestBeforeMuxInjectsPrincipalOnValidBearer(t *testing.T) {
	next := &recordingHandler{}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/lotes/inicio", nil)
	r.Header.Set("Authorization", "Bearer s3cr3t")
	rec := httptest.NewRecorder()
	BeforeMux(newVerifier("dev-42"), nil, next).ServeHTTP(rec, r)

	require.True(t, next.called)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	require.True(t, next.hasPrin)
	assert.Equal(t, "dev-42", next.principal.DeviceID)
}

func TestBeforeMuxRateLimitsAuthenticatedDevice(t *testing.T) {
	next := &recordingHandler{}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/lotes/inicio", nil)
	r.Header.Set("Authorization", "Bearer s3cr3t")
	rec := httptest.NewRecorder()
	BeforeMux(newVerifier("dev-42"), guard.NewLimiter(0, 0), next).ServeHTTP(rec, r)

	assert.False(t, next.called)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}

// TestBeforeMuxProtectsRenameEndpoint cubre que PUT /api/v1/dispositivos/nombre
// exige un token de dispositivo válido y que los POST previamente protegidos
// siguen exigiéndolo.
func TestBeforeMuxProtectsRenameEndpoint(t *testing.T) {
	t.Run("rename-sin-token-401", func(t *testing.T) {
		next := &recordingHandler{}
		r := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos/nombre", nil)
		rec := httptest.NewRecorder()
		BeforeMux(newVerifier("dev-1"), nil, next).ServeHTTP(rec, r)

		assert.False(t, next.called)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, invalidTokenBody, rec.Body.String())
	})

	t.Run("rename-con-token-valido", func(t *testing.T) {
		next := &recordingHandler{}
		r := httptest.NewRequest(http.MethodPut, "/api/v1/dispositivos/nombre", nil)
		r.Header.Set("Authorization", "Bearer s3cr3t")
		rec := httptest.NewRecorder()
		BeforeMux(newVerifier("dev-42"), nil, next).ServeHTTP(rec, r)

		require.True(t, next.called)
		assert.Equal(t, http.StatusNoContent, rec.Code)
		require.True(t, next.hasPrin)
		assert.Equal(t, "dev-42", next.principal.DeviceID)
	})

	t.Run("post-protegido-sin-token-401", func(t *testing.T) {
		for _, path := range []string{"/api/v1/dispositivos/ping", "/api/v1/lotes/inicio"} {
			next := &recordingHandler{}
			r := httptest.NewRequest(http.MethodPost, path, nil)
			rec := httptest.NewRecorder()
			BeforeMux(newVerifier("dev-1"), nil, next).ServeHTTP(rec, r)

			assert.False(t, next.called, "path %s", path)
			assert.Equal(t, http.StatusUnauthorized, rec.Code, "path %s", path)
		}
	})
}

// TestBeforeMuxProtectsSectorAndLoteEventEndpoints cubre los objetivos
// device-only incorporados: GET /dispositivos/sector y los POST
// /lotes/{id}/eventos y /lotes/{id}/cierre.
func TestBeforeMuxProtectsSectorAndLoteEventEndpoints(t *testing.T) {
	const loteID = "6f1e6a2c-3b4d-4f5a-9c8e-1d2e3f4a5b6c"
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/dispositivos/sector"},
		{http.MethodPost, "/api/v1/lotes/" + loteID + "/eventos"},
		{http.MethodPost, "/api/v1/lotes/" + loteID + "/cierre"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			next := &recordingHandler{}
			r := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			BeforeMux(newVerifier("dev-1"), nil, next).ServeHTTP(rec, r)

			assert.False(t, next.called)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.Equal(t, invalidTokenBody, rec.Body.String())
		})
	}
}

// TestBeforeMuxPassesThroughNonDeviceLoteEndpoints verifica que el matcher no
// intercepta rutas que no son device-only ni tienen la forma /lotes/{id}/(eventos|cierre).
func TestBeforeMuxPassesThroughNonDeviceLoteEndpoints(t *testing.T) {
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/lotes/abierto"},
		{http.MethodPost, "/api/v1/lotes/events"},
		{http.MethodGet, "/api/v1/lotes"},
		{http.MethodGet, "/api/v1/lotes/6f1e6a2c-3b4d-4f5a-9c8e-1d2e3f4a5b6c/eventos"},
		{http.MethodGet, "/api/v1/lotes/6f1e6a2c-3b4d-4f5a-9c8e-1d2e3f4a5b6c/cierre"},
		{http.MethodPost, "/api/v1/lotes/"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			next := &recordingHandler{}
			r := httptest.NewRequest(tc.method, tc.path, nil)
			// Store vacío: si el matcher interceptara, autenticar daría 401.
			BeforeMux(&deviceauth.Verifier{Store: stubSecretStore{id: ""}}, nil, next).ServeHTTP(httptest.NewRecorder(), r)
			assert.True(t, next.called)
		})
	}
}

// TestBeforeMuxIsProtectedTarget unit-tests the matcher boundaries directly.
func TestBeforeMuxIsProtectedTarget(t *testing.T) {
	protected := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v1/dispositivos/ping"},
		{"GET", "/api/v1/dispositivos/sector"},
		{"POST", "/api/v1/lotes/inicio"},
		{"POST", "/api/v1/lotes/6f1e6a2c-3b4d-4f5a-9c8e-1d2e3f4a5b6c/eventos"},
		{"POST", "/api/v1/lotes/6f1e6a2c-3b4d-4f5a-9c8e-1d2e3f4a5b6c/cierre"},
	}
	for _, tc := range protected {
		assert.True(t, isProtectedTarget(tc.method, tc.path, tc.path), "%s %s", tc.method, tc.path)
	}

	notProtected := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v1/lotes/abierto"},
		{"POST", "/api/v1/lotes/events"},
		{"POST", "/api/v1/lotes"}, // alta legada removida: ya no es device-only
		{"GET", "/api/v1/lotes"},
		{"GET", "/api/v1/lotes/inicio"},
		{"POST", "/api/v1/lotes//eventos"},
		{"POST", "/api/v1/lotes/a/b/eventos"},
		{"POST", "/api/v1/lotes/x/otro"},
	}
	for _, tc := range notProtected {
		assert.False(t, isProtectedTarget(tc.method, tc.path, tc.path), "%s %s", tc.method, tc.path)
	}

	// La ruta escapada del id con slash escapado SÍ debe matchear aunque la
	// decodificada no lo haga: es la defensa contra el bypass de autenticación.
	assert.True(t,
		isProtectedTarget("POST", "/api/v1/lotes/a/b/eventos", "/api/v1/lotes/a%2Fb/eventos"),
		"la forma escapada con slash debe ser interceptada")
}

// TestBeforeMuxInterceptaSlashEscapadoEnLoteID es la regresión del bypass de
// autenticación: Go enruta /lotes/a%2Fb/eventos al handler con id "a/b", pero el
// matcher sobre la ruta decodificada no lo veía y lo dejaba pasar sin token.
func TestBeforeMuxInterceptaSlashEscapadoEnLoteID(t *testing.T) {
	const loteID = "6f1e6a2c-3b4d-4f5a-9c8e-1d2e3f4a5b6c"

	t.Run("id-con-slash-escapado-sin-token-401", func(t *testing.T) {
		next := &recordingHandler{}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/lotes/a%2Fb/eventos", nil)
		rec := httptest.NewRecorder()
		BeforeMux(newVerifier("dev-1"), nil, next).ServeHTTP(rec, r)

		assert.False(t, next.called)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Equal(t, invalidTokenBody, rec.Body.String())
	})

	t.Run("uuid-sin-token-401", func(t *testing.T) {
		next := &recordingHandler{}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/lotes/"+loteID+"/eventos", nil)
		rec := httptest.NewRecorder()
		BeforeMux(newVerifier("dev-1"), nil, next).ServeHTTP(rec, r)

		assert.False(t, next.called)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("rutas-no-device-pasan", func(t *testing.T) {
		cases := []struct {
			method string
			path   string
		}{
			{http.MethodPost, "/api/v1/lotes/abierto"},
			{http.MethodPost, "/api/v1/lotes/events"},
			{http.MethodGet, "/api/v1/lotes"},
		}
		for _, tc := range cases {
			next := &recordingHandler{}
			r := httptest.NewRequest(tc.method, tc.path, nil)
			BeforeMux(&deviceauth.Verifier{Store: stubSecretStore{id: ""}}, nil, next).ServeHTTP(httptest.NewRecorder(), r)
			assert.True(t, next.called, "%s %s", tc.method, tc.path)
		}
	})
}
