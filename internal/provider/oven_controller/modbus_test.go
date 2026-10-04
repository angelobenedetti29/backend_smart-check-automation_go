package oven_controller

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/grid-x/modbus"
)

// mockModbusClient implementa modbus.Client embebiendo la interfaz (así sólo
// hay que overridear los métodos usados) y permite inyectar comportamiento en
// el write y el read-back.
type mockModbusClient struct {
	modbus.Client
	writeHook   func(ctx context.Context, address, quantity uint16, value []byte) error
	readHook    func(ctx context.Context, address, quantity uint16) ([]byte, error)
	writeErr    error
	lastWritten []byte
	writeCalls  int
}

func (m *mockModbusClient) WriteMultipleRegisters(ctx context.Context, address, quantity uint16, value []byte) ([]byte, error) {
	m.writeCalls++
	if m.writeHook != nil {
		if err := m.writeHook(ctx, address, quantity, value); err != nil {
			return nil, err
		}
	}
	if m.writeErr != nil {
		return nil, m.writeErr
	}
	m.lastWritten = append([]byte(nil), value...)
	return []byte{byte(quantity >> 8), byte(quantity)}, nil
}

func (m *mockModbusClient) ReadHoldingRegisters(ctx context.Context, address, quantity uint16) ([]byte, error) {
	if m.readHook != nil {
		return m.readHook(ctx, address, quantity)
	}
	// Echo del último valor escrito → read-back consistente.
	return append([]byte(nil), m.lastWritten...), nil
}

// floatBytes serializa un float32 big-endian (2 registros ABCD).
func floatBytes(v float64) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, math.Float32bits(float32(v)))
	return buf
}

func TestModbusOvenControllerExito(t *testing.T) {
	mock := &mockModbusClient{}
	c := NewModbusOvenControllerFromClient(mock, ModbusConfig{
		RegTemp1: 100, RegVelocidad: 102, ReadBackTolerance: 0.1, Timeout: time.Second,
	})

	res := c.SendSetpoint("horno-1", 180.5, 0.25)
	if !res.Aplicada {
		t.Fatalf("se esperaba Aplicada=true; got=%+v", res)
	}
	if res.TemperaturaReal != 180.5 || res.VelocidadReal != 0.25 {
		t.Fatalf("setpoints mal reflejados: %+v", res)
	}
	if mock.writeCalls != 2 {
		t.Fatalf("writeCalls = %d, want 2 (T1 y V)", mock.writeCalls)
	}
}

func TestModbusOvenControllerWriteFalla(t *testing.T) {
	mock := &mockModbusClient{writeErr: errors.New("boom")}
	c := NewModbusOvenControllerFromClient(mock, ModbusConfig{
		RegTemp1: 100, RegVelocidad: 102, ReadBackTolerance: 0.1, Timeout: time.Second,
	})

	res := c.SendSetpoint("horno-1", 180, 0.2)
	if res.Aplicada {
		t.Fatalf("se esperaba Aplicada=false; got=%+v", res)
	}
	if !strings.Contains(res.Motivo, "write T1") {
		t.Fatalf("Motivo = %q, want contiene 'write T1'", res.Motivo)
	}
}

func TestModbusOvenControllerReadBackMismatch(t *testing.T) {
	mock := &mockModbusClient{}
	mock.readHook = func(context.Context, uint16, uint16) ([]byte, error) {
		return floatBytes(999), nil
	}
	c := NewModbusOvenControllerFromClient(mock, ModbusConfig{
		RegTemp1: 100, RegVelocidad: 102, ReadBackTolerance: 0.1, Timeout: time.Second,
	})

	res := c.SendSetpoint("horno-1", 180, 0.2)
	if res.Aplicada {
		t.Fatalf("se esperaba Aplicada=false por mismatch; got=%+v", res)
	}
	if !strings.Contains(res.Motivo, "read-back mismatch") {
		t.Fatalf("Motivo = %q, want contiene 'read-back mismatch'", res.Motivo)
	}
}

func TestModbusOvenControllerTimeout(t *testing.T) {
	mock := &mockModbusClient{writeHook: func(ctx context.Context, _, _ uint16, _ []byte) error {
		// Cuelga hasta que el ctx del controlador expire (Timeout corto).
		<-ctx.Done()
		return ctx.Err()
	}}
	c := NewModbusOvenControllerFromClient(mock, ModbusConfig{
		RegTemp1: 100, RegVelocidad: 102, ReadBackTolerance: 0.1, Timeout: 50 * time.Millisecond,
	})

	start := time.Now()
	res := c.SendSetpoint("horno-1", 180, 0.2)
	if res.Aplicada {
		t.Fatalf("se esperaba Aplicada=false por timeout; got=%+v", res)
	}
	if !strings.Contains(res.Motivo, "write T1") {
		t.Fatalf("Motivo = %q, want contiene 'write T1'", res.Motivo)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("el timeout no acotó el bloqueo: %s", elapsed)
	}
}

// TestModbusOvenControllerEncodeFloat documenta/valida el encoding ABCD que se
// escribe en los registros: 20.5 → 0x41A40000.
func TestModbusOvenControllerEncodeFloat(t *testing.T) {
	got := floatBytes(20.5)
	want := []byte{0x41, 0xA4, 0x00, 0x00}
	if string(got) != string(want) {
		t.Fatalf("encoding = % x, want % x", got, want)
	}
}
