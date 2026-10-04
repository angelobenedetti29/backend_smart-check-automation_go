package oven_controller

import "testing"

// TestSimulatedOvenControllerAplicada verifica la forma del resultado del
// driver simulado. El simulado tiene 5% de fallo aleatorio por diseño, así que
// el test valida ambas ramas (éxito con setpoints reflejados, o fallo con
// motivo) sin ser flaky.
func TestSimulatedOvenControllerAplicada(t *testing.T) {
	c := NewSimulatedOvenController("http://localhost:8600/plc/horno")
	if c == nil {
		t.Fatal("NewSimulatedOvenController devolvió nil")
	}

	res := c.SendSetpoint("horno-1", 180.5, 0.25)
	if res.Aplicada {
		if res.TemperaturaReal != 180.5 || res.VelocidadReal != 0.25 {
			t.Fatalf("setpoints mal reflejados en éxito: %+v", res)
		}
		return
	}
	if res.Motivo == "" {
		t.Fatalf("fallo simulado sin motivo: %+v", res)
	}
}
