package oven_controller

import (
	"strings"
	"testing"
)

func TestNewOvenControllerFromEnvDefault(t *testing.T) {
	t.Setenv("OVEN_DRIVER", "")
	c, closer, err := NewOvenControllerFromEnv()
	if err != nil {
		t.Fatalf("error inesperado: %v", err)
	}
	if closer != nil {
		t.Fatalf("el driver simulado no debe requerir closer")
	}
	if _, ok := c.(*SimulatedOvenController); !ok {
		t.Fatalf("tipo = %T, want *SimulatedOvenController", c)
	}
}

func TestNewOvenControllerFromEnvModbusConnectFalla(t *testing.T) {
	t.Setenv("OVEN_DRIVER", "modbus")
	// Puerto muerto: Connect debe fallar y NO caer a simulated.
	t.Setenv("OVEN_MODBUS_ADDRESS", "127.0.0.1:1")
	t.Setenv("OVEN_MODBUS_TIMEOUT", "200ms")

	c, closer, err := NewOvenControllerFromEnv()
	if err == nil {
		t.Fatalf("se esperaba error de Connect; got c=%T closer=%v", c, closer)
	}
	if c != nil || closer != nil {
		t.Fatalf("ante error no debe devolver controller/closer: c=%v closer=%v", c, closer)
	}
	if !strings.Contains(err.Error(), "modbus connect") {
		t.Fatalf("error = %v, want contiene 'modbus connect'", err)
	}
}

func TestNewOvenControllerFromEnvDriverInvalido(t *testing.T) {
	t.Setenv("OVEN_DRIVER", "mqtt")
	_, _, err := NewOvenControllerFromEnv()
	if err == nil {
		t.Fatal("se esperaba error por driver no soportado")
	}
	if !strings.Contains(err.Error(), "mqtt") {
		t.Fatalf("error = %v, want menciona 'mqtt'", err)
	}
}
