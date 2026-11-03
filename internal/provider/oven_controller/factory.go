package oven_controller

// NewOvenController devuelve el stub interno del backend. El backend no escribe
// el PLC real: la Pi ENTRADA es la única escritora.
func NewOvenController() *SimulatedOvenController {
	return NewSimulatedOvenController()
}
