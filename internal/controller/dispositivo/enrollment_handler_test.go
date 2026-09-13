package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	authController "github.com/angelobenedetti29/smart-check-automation/internal/controller/auth"
	"github.com/angelobenedetti29/smart-check-automation/internal/controller/deviceproof"
	dispositivo "github.com/angelobenedetti29/smart-check-automation/internal/domain/dispositivo"
	userDomain "github.com/angelobenedetti29/smart-check-automation/internal/domain/user"
	"github.com/angelobenedetti29/smart-check-automation/internal/guard"
	authService "github.com/angelobenedetti29/smart-check-automation/internal/service/auth"
)

const composedJWTSecret = "test-secret-32-chars-exactly!!!"

// fakeEnrollmentService records calls to the enrollmentActions surface.
type fakeEnrollmentService struct {
	issueCalls       int
	reprovisionCalls int
	reprovisionResp  *dispositivo.EnrollmentInvitation
	reprovisionErr   error
}

func (f *fakeEnrollmentService) Issue(context.Context, string, dispositivo.EnrollmentCreateRequest) (*dispositivo.EnrollmentInvitation, error) {
	f.issueCalls++
	return &dispositivo.EnrollmentInvitation{EnrollmentID: "env-1", Status: "pending"}, nil
}

func (f *fakeEnrollmentService) List(context.Context) ([]dispositivo.EnrollmentInvitation, error) {
	return nil, nil
}

func (f *fakeEnrollmentService) Cancel(context.Context, string, string) error { return nil }

func (f *fakeEnrollmentService) Redeem(context.Context, string, dispositivo.PublicJWK) (*dispositivo.DeviceIdentity, error) {
	return nil, nil
}

func (f *fakeEnrollmentService) Recover(context.Context, dispositivo.PublicJWK) (*dispositivo.DeviceIdentity, error) {
	return nil, nil
}

func (f *fakeEnrollmentService) Lifecycle(context.Context, string, string, string) (*dispositivo.DeviceRead, error) {
	return nil, nil
}

func (f *fakeEnrollmentService) Reprovision(context.Context, string, string) (*dispositivo.EnrollmentInvitation, error) {
	f.reprovisionCalls++
	if f.reprovisionErr != nil {
		return nil, f.reprovisionErr
	}
	if f.reprovisionResp != nil {
		return f.reprovisionResp, nil
	}
	return &dispositivo.EnrollmentInvitation{EnrollmentID: "env-1", Status: "pending"}, nil
}

func (f *fakeEnrollmentService) Reads(context.Context) ([]dispositivo.DeviceRead, error) {
	return nil, nil
}

// stubUserRepo is a minimal userDomain.Repository for composed route tests.
type stubUserRepo struct {
	user *userDomain.User
}

func (s stubUserRepo) FindByEmail(_ context.Context, _ string) (*userDomain.User, error) {
	if s.user == nil {
		return nil, userDomain.ErrUserNotFound
	}
	u := *s.user
	return &u, nil
}

func (s stubUserRepo) FindByID(context.Context, string) (*userDomain.User, error) { return nil, nil }
func (s stubUserRepo) FindAll(context.Context) ([]*userDomain.User, error)        { return nil, nil }
func (s stubUserRepo) Create(context.Context, *userDomain.User) error             { return nil }
func (s stubUserRepo) Update(context.Context, *userDomain.User) error             { return nil }

func signedSessionCookie(t *testing.T, email, role string) *http.Cookie {
	t.Helper()
	claims := &authService.Claims{
		Email: email,
		Name:  "Test User",
		Role:  role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "smart-check-automation",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(composedJWTSecret))
	if err != nil {
		t.Fatalf("signing session cookie: %v", err)
	}
	return &http.Cookie{Name: "session_token", Value: signed}
}

// buildReprovisionChain mirrors the production wiring for the human management
// route: JWTMiddleware -> DB role -> per-user mutation quota -> handler, all
// wrapped by the pre-mux device-proof guard.
func buildReprovisionChain(repo userDomain.Repository, svc *fakeEnrollmentService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/dispositivos/", authController.JWTMiddleware([]byte(composedJWTSecret),
		authController.RequireRoleFromDB(repo, []string{userDomain.RoleSupervisor, userDomain.RoleAdmin},
			authController.RateLimitByUser(repo, guard.NewLimiter(0.5, 10), func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/reprovision") {
					NewEnrollmentHandler(svc).HandleReprovision(w, r)
					return
				}
				http.NotFound(w, r)
			}))))
	return deviceproof.BeforeMux(nil, mux)
}

func supervisorRepo(role string) stubUserRepo {
	return stubUserRepo{user: &userDomain.User{ID: "u-1", Email: "op@fermar.com.ar", Rol: role, Activo: true}}
}

// TestReprovisionRouting_SupervisorAndAdminReachService is the composed
// regression: the pre-mux guard must let the human route through untouched.
func TestReprovisionRouting_SupervisorAndAdminReachService(t *testing.T) {
	for _, role := range []string{userDomain.RoleSupervisor, userDomain.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			svc := &fakeEnrollmentService{}
			handler := buildReprovisionChain(supervisorRepo(role), svc)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/dev-1/reprovision", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(signedSessionCookie(t, "op@fermar.com.ar", role))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusCreated {
				t.Fatalf("role=%s status=%d body=%s", role, rec.Code, rec.Body.String())
			}
			if svc.reprovisionCalls != 1 {
				t.Fatalf("role=%s expected service call, got %d", role, svc.reprovisionCalls)
			}
		})
	}
}

func TestReprovisionRouting_OperarioForbidden(t *testing.T) {
	svc := &fakeEnrollmentService{}
	handler := buildReprovisionChain(supervisorRepo(userDomain.RoleOperario), svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/dev-1/reprovision", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(signedSessionCookie(t, "op@fermar.com.ar", userDomain.RoleOperario))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.reprovisionCalls != 0 {
		t.Fatalf("Operario must not reach service, got %d calls", svc.reprovisionCalls)
	}
}

func TestReprovisionRouting_MissingSessionUnauthorized(t *testing.T) {
	svc := &fakeEnrollmentService{}
	handler := buildReprovisionChain(supervisorRepo(userDomain.RoleSupervisor), svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/dev-1/reprovision", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if svc.reprovisionCalls != 0 {
		t.Fatalf("missing session must not reach service, got %d calls", svc.reprovisionCalls)
	}
}

// TestReprovisionRouting_DeviceProofAloneCannotManage proves a DeviceProof
// Authorization value does not substitute for the human session: the request
// is not intercepted by the proof guard and is rejected by the JWT layer.
func TestReprovisionRouting_DeviceProofAloneCannotManage(t *testing.T) {
	svc := &fakeEnrollmentService{}
	handler := buildReprovisionChain(supervisorRepo(userDomain.RoleSupervisor), svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/dev-1/reprovision", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "DeviceProof not-a-human-session")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Sesión requerida") {
		t.Fatalf("expected the human JWT layer to reject, got body=%s", rec.Body.String())
	}
	if svc.reprovisionCalls != 0 {
		t.Fatalf("device proof alone must not reach service, got %d calls", svc.reprovisionCalls)
	}
}

// TestHandleCollection_Create_RejectsStrictJSONBeforeService asserts duplicate
// and case-alias members on the invitation DTO fail before Issue runs.
func TestHandleCollection_Create_RejectsStrictJSONBeforeService(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate-nombre":  `{"nombre":"Pi 1","nombre":"Pi 2"}`,
		"case-alias-nombre": `{"Nombre":"Pi 1"}`,
		"trailing-doc":      `{"nombre":"Pi 1"}{"x":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeEnrollmentService{}
			h := NewEnrollmentHandler(svc)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/enrollments", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(signedSessionCookie(t, "op@fermar.com.ar", userDomain.RoleSupervisor))
			rec := httptest.NewRecorder()

			authController.JWTMiddleware([]byte(composedJWTSecret), h.HandleCollection)(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
			}
			if svc.issueCalls != 0 {
				t.Fatalf("invitation create must not reach service, got %d calls", svc.issueCalls)
			}
		})
	}
}

// TestHandleCollection_Create_AcceptsCanonicalPayload preserves compatibility.
func TestHandleCollection_Create_AcceptsCanonicalPayload(t *testing.T) {
	svc := &fakeEnrollmentService{}
	h := NewEnrollmentHandler(svc)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dispositivos/enrollments", strings.NewReader(`{"nombre":"Pi 1","ubicacion":"Línea A"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(signedSessionCookie(t, "op@fermar.com.ar", userDomain.RoleSupervisor))
	rec := httptest.NewRecorder()

	authController.JWTMiddleware([]byte(composedJWTSecret), h.HandleCollection)(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.issueCalls != 1 {
		t.Fatalf("expected one service call, got %d", svc.issueCalls)
	}
}
