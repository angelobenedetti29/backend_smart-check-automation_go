package oven_controller

import (
	"log"
	"time"
)

// DispatchResult holds the outcome of applying a setpoint on the physical oven
// controller (or its simulated stand-in).
type DispatchResult struct {
	Aplicada        bool    `json:"aplicada"`
	TemperaturaReal float64 `json:"temperatura_real"`
	VelocidadReal   float64 `json:"velocidad_real"`
	Motivo          string  `json:"motivo,omitempty"`
}

// SimulatedOvenController es el stub interno del backend: no escribe ningún PLC.
// Devuelve siempre Aplicada=true con los setpoints reflejados, para que el
// backend pueda auditar/actualizar estado. La Pi ENTRADA es la única que escribe
// el PLC real.
type SimulatedOvenController struct{}

// NewSimulatedOvenController inicializa un SimulatedOvenController.
func NewSimulatedOvenController() *SimulatedOvenController {
	return &SimulatedOvenController{}
}

// SendSetpoint simula el despacho de una consigna (temperatura/velocidad) al
// controlador físico del horno.
func (c *SimulatedOvenController) SendSetpoint(hornoID string, temperatura, velocidadCinta float64) DispatchResult {
	log.Printf("[OvenController] Despachando consigna a horno=%s temp=%.2f velocidad=%.2f", hornoID, temperatura, velocidadCinta)

	// Simula la latencia del round-trip al controlador físico (~40ms).
	time.Sleep(40 * time.Millisecond)

	return DispatchResult{
		Aplicada:        true,
		TemperaturaReal: temperatura,
		VelocidadReal:   velocidadCinta,
	}
}
