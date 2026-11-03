package oven_controller

import "testing"

// TestSimulatedOvenControllerAplicada verifica que el stub interno siempre
// aplica y refleja los setpoints recibidos.
func TestSimulatedOvenControllerAplicada(t *testing.T) {
	c := NewSimulatedOvenController()
	if c == nil {
		t.Fatal("NewSimulatedOvenController devolvió nil")
	}

	res := c.SendSetpoint("horno-1", 180.5, 0.25)
	if !res.Aplicada {
		t.Fatalf("se esperaba Aplicada=true; got=%+v", res)
	}
	if res.TemperaturaReal != 180.5 || res.VelocidadReal != 0.25 {
		t.Fatalf("setpoints mal reflejados: %+v", res)
	}
	if res.Motivo != "" {
		t.Fatalf("stub no debe reportar motivo: %+v", res)
	}
}
