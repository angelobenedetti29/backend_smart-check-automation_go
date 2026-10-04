package oven_controller

import (
	"log"
	"math/rand"
	"time"
)

// DispatchResult holds the outcome of applying a setpoint on the physical oven
// controller (or its simulated stand-in).
type DispatchResult struct {
	Aplicada        bool    `json:"aplicada"`
	TemperaturaReal float64 `json:"temperatura_real"`
	VelocidadReal   float64 `json:"velocidad_real"`
	LatencyMs       int64   `json:"latency_ms"`
	Motivo          string  `json:"motivo,omitempty"`
}

// SimulatedOvenController implementa la firma de despacho de setpoints contra
// un controlador simulado (sleep + 5% de fallo aleatorio). Es la implementación
// para dev/CI/tests; el driver real es `ModbusOvenController`. El endpoint se
// recibe por parámetro (no lee variables de entorno).
type SimulatedOvenController struct {
	endpoint string
}

// NewSimulatedOvenController inicializa un SimulatedOvenController.
func NewSimulatedOvenController(endpoint string) *SimulatedOvenController {
	return &SimulatedOvenController{
		endpoint: endpoint,
	}
}

// SendSetpoint simula el despacho de una consigna (temperatura/velocidad) al
// controlador físico del horno.
func (c *SimulatedOvenController) SendSetpoint(hornoID string, temperatura, velocidadCinta float64) DispatchResult {
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
