package oven_controller

import (
	"log"
	"math/rand"
	"time"
)

// OvenControllerClient simulates a client interface for the oven's physical
// controller (PLC/microcontroller). In a full implementation, this would send
// the setpoint to the real hardware via Modbus/MQTT/HTTP.
type OvenControllerClient struct {
	endpoint string
}

// DispatchResult holds the simulated outcome of applying a setpoint on the
// physical oven controller.
type DispatchResult struct {
	Aplicada        bool    `json:"aplicada"`
	TemperaturaReal float64 `json:"temperatura_real"`
	VelocidadReal   float64 `json:"velocidad_real"`
	LatencyMs       int64   `json:"latency_ms"`
	Motivo          string  `json:"motivo,omitempty"`
}

// NewOvenControllerClient initializes a new OvenControllerClient.
func NewOvenControllerClient(endpoint string) *OvenControllerClient {
	return &OvenControllerClient{
		endpoint: endpoint,
	}
}

// SendSetpoint simulates dispatching a temperature/conveyor-speed setpoint to
// the physical oven controller.
func (c *OvenControllerClient) SendSetpoint(hornoID string, temperatura, velocidadCinta float64) DispatchResult {
	log.Printf("[OvenController] Despachando consigna a horno=%s temp=%.2f velocidad=%.2f (endpoint=%s)", hornoID, temperatura, velocidadCinta, c.endpoint)

	// Simulate physical controller round-trip latency (30-60ms)
	time.Sleep(40 * time.Millisecond)

	source := rand.NewSource(time.Now().UnixNano())
	r := rand.New(source)

	if r.Float64() > 0.95 { // 5% probability of simulated dispatch failure
		return DispatchResult{
			Aplicada: false,
			Motivo:   "timeout del controlador físico (simulado)",
		}
	}

	return DispatchResult{
		Aplicada:        true,
		TemperaturaReal: temperatura,
		VelocidadReal:   velocidadCinta,
		LatencyMs:       40,
	}
}
