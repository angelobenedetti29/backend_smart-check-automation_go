package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/angelobenedetti29/smart-check-automation/internal/provider/database"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/yolo_client"
	"github.com/angelobenedetti29/smart-check-automation/internal/service/horno"
)

func TestHornoHandler_GetHornoStatus(t *testing.T) {
	// Initialize core database providers and service layers
	dbRepo := database.NewMySQLRepository()
	yolo := yolo_client.NewYOLOClient("http://localhost:8500")
	svc := service.NewHornoService(dbRepo, dbRepo, yolo)
	handler := NewHornoHandler(svc, dbRepo)

	// Create simulated GET request
	req, err := http.NewRequest("GET", "/api/v1/horno?id=horno-01", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}

	rr := httptest.NewRecorder()
	handler.GetHornoStatus(rr, req)

	// Verify HTTP 200 OK
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	// Verify response body JSON schema
	var body map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &body)
	if err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}

	if body["success"] != true {
		t.Errorf("expected success to be true, got %v", body["success"])
	}

	message := body["message"].(string)
	if message != "Estado de Horno verificado exitosamente" {
		t.Errorf("unexpected message: %s", message)
	}
}

func TestHornoHandler_UpdateTemperature(t *testing.T) {
	dbRepo := database.NewMySQLRepository()
	yolo := yolo_client.NewYOLOClient("http://localhost:8500")
	svc := service.NewHornoService(dbRepo, dbRepo, yolo)
	handler := NewHornoHandler(svc, dbRepo)

	// Valid input JSON body
	input := UpdateTemperatureInput{
		ID:          "horno-01",
		Temperatura: 205.5, // Trigger CRITICAL alert (> 200°C)
	}
	jsonBytes, _ := json.Marshal(input)

	req, err := http.NewRequest("POST", "/api/v1/horno/temperatura", bytes.NewBuffer(jsonBytes))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	handler.UpdateTemperature(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var body map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)

	if body["success"] != true {
		t.Errorf("expected success true, got %v", body["success"])
	}

	// Check that state updated to MANTENIMIENTO due to threshold trigger (> 200°C)
	data := body["data"].(map[string]interface{})
	hornoMap := data["horno"].(map[string]interface{})

	if hornoMap["estado"] != "MANTENIMIENTO" {
		t.Errorf("expected oven state 'MANTENIMIENTO' after 205.5°C trigger, got '%s'", hornoMap["estado"])
	}
}
