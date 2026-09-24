package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/domain/sector"
	"github.com/angelobenedetti29/smart-check-automation/pkg/response"
)

type fakeSectorService struct {
	listResp    []sector.Sector
	listErr     error
	createResp  *sector.Sector
	createErr   error
	createReq   sector.CreateSectorRequest
	createCalls int
	updateResp  *sector.Sector
	updateErr   error
	updateID    string
	updateReq   sector.UpdateSectorRequest
	updateCalls int
	deleteErr   error
	deleteID    string
	deleteCalls int
}

func (f *fakeSectorService) List(context.Context) ([]sector.Sector, error) {
	return f.listResp, f.listErr
}

func (f *fakeSectorService) Create(_ context.Context, req sector.CreateSectorRequest) (*sector.Sector, error) {
	f.createCalls++
	f.createReq = req
	return f.createResp, f.createErr
}

func (f *fakeSectorService) Update(_ context.Context, id string, req sector.UpdateSectorRequest) (*sector.Sector, error) {
	f.updateCalls++
	f.updateID = id
	f.updateReq = req
	return f.updateResp, f.updateErr
}

func (f *fakeSectorService) Delete(_ context.Context, id string) error {
	f.deleteCalls++
	f.deleteID = id
	return f.deleteErr
}

func TestHandleSectores_List(t *testing.T) {
	svc := &fakeSectorService{listResp: []sector.Sector{{ID: "horno-1", Nombre: "Horno 1"}}}
	h := NewSectorHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sectores", nil)
	rec := httptest.NewRecorder()
	h.HandleSectores(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp response.Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("error decoding response: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
}

func TestHandleSectores_WrongMethod(t *testing.T) {
	h := NewSectorHandler(&fakeSectorService{})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/sectores", nil)
	rec := httptest.NewRecorder()
	h.HandleSectores(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleSectores_Create(t *testing.T) {
	svc := &fakeSectorService{createResp: &sector.Sector{ID: "horno-1", Nombre: "Horno 1"}}
	h := NewSectorHandler(svc)
	body := bytes.NewBufferString(`{"nombre":"Horno 1"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sectores", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleSectores(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	if svc.createReq.Nombre != "Horno 1" {
		t.Fatalf("expected nombre decoded, got %q", svc.createReq.Nombre)
	}
	var resp response.Response
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	data, _ := resp.Data.(map[string]interface{})
	if data["id"] != "horno-1" || data["nombre"] != "Horno 1" {
		t.Fatalf("unexpected data: %+v", data)
	}
}

func TestHandleSectores_Create_EmptyNombre(t *testing.T) {
	svc := &fakeSectorService{}
	h := NewSectorHandler(svc)
	body := bytes.NewBufferString(`{"nombre":"   "}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sectores", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleSectores(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	if svc.createCalls != 0 {
		t.Fatalf("invalid request must not reach service, got %d calls", svc.createCalls)
	}
}

func TestHandleSectores_Create_InvalidContentType(t *testing.T) {
	h := NewSectorHandler(&fakeSectorService{})
	body := bytes.NewBufferString(`{"nombre":"Horno 1"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sectores", body)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.HandleSectores(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", rec.Code)
	}
}

func TestHandleSectores_Create_InvalidJSON(t *testing.T) {
	h := NewSectorHandler(&fakeSectorService{})
	body := bytes.NewBufferString(`{"nombre":`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sectores", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleSectores(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandleSectores_Create_InternalError(t *testing.T) {
	h := NewSectorHandler(&fakeSectorService{createErr: errors.New("db caída")})
	body := bytes.NewBufferString(`{"nombre":"Horno 1"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sectores", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleSectores(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestHandleSectorByID_Update(t *testing.T) {
	svc := &fakeSectorService{updateResp: &sector.Sector{ID: "horno-1", Nombre: "Horno Uno"}}
	h := NewSectorHandler(svc)
	body := bytes.NewBufferString(`{"nombre":"Horno Uno"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/sectores/horno-1", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleSectorByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if svc.updateID != "horno-1" {
		t.Fatalf("expected id parsed from path, got %q", svc.updateID)
	}
}

func TestHandleSectorByID_Update_NotFound(t *testing.T) {
	h := NewSectorHandler(&fakeSectorService{updateErr: sector.ErrSectorNotFound})
	body := bytes.NewBufferString(`{"nombre":"Horno Uno"}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/sectores/missing", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleSectorByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleSectorByID_Update_ValidationError(t *testing.T) {
	h := NewSectorHandler(&fakeSectorService{})
	body := bytes.NewBufferString(`{"nombre":"  "}`)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/sectores/horno-1", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleSectorByID(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestHandleSectorByID_Delete(t *testing.T) {
	svc := &fakeSectorService{}
	h := NewSectorHandler(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sectores/horno-1", nil)
	rec := httptest.NewRecorder()
	h.HandleSectorByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if svc.deleteID != "horno-1" {
		t.Fatalf("expected id parsed from path, got %q", svc.deleteID)
	}
}

func TestHandleSectorByID_Delete_ConLotes(t *testing.T) {
	h := NewSectorHandler(&fakeSectorService{deleteErr: sector.ErrSectorConLotes})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sectores/horno-1", nil)
	rec := httptest.NewRecorder()
	h.HandleSectorByID(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestHandleSectorByID_Delete_NotFound(t *testing.T) {
	h := NewSectorHandler(&fakeSectorService{deleteErr: sector.ErrSectorNotFound})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sectores/missing", nil)
	rec := httptest.NewRecorder()
	h.HandleSectorByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleSectorByID_MissingID(t *testing.T) {
	h := NewSectorHandler(&fakeSectorService{})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sectores/", nil)
	rec := httptest.NewRecorder()
	h.HandleSectorByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleSectorByID_WrongMethod(t *testing.T) {
	h := NewSectorHandler(&fakeSectorService{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sectores/horno-1", nil)
	rec := httptest.NewRecorder()
	h.HandleSectorByID(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestExtractSectorID(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/api/v1/sectores/horno-1", "horno-1"},
		{"/api/v1/sectores/horno-1/", "horno-1"},
		{"/api/v1/sectores/", ""},
		{"/api/v1/sectores", ""},
		{"/api/v1/sectores/a/b", ""},
	}
	for _, tt := range tests {
		if got := extractSectorID(tt.path); got != tt.want {
			t.Errorf("extractSectorID(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
