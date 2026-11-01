package oven_controller

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/grid-x/modbus"
)

// ModbusOvenController envía setpoints al PLC físico del horno vía Modbus TCP.
// Usa github.com/grid-x/modbus (fork mantenido del deprecated goburrow/modbus).
//
// Convención de encoding: float32 se serializa como 2 holding registers de 16
// bits, big-endian ABCD (high-word-first), que es la convención industrial más
// común en PLCs Schneider/Modicon. Verificar contra el manual del PLC físico
// antes de deployar en planta.
//
// El driver escribe T1 y V (la interfaz SendSetpoint no transporta más
// variables).
//
// Verificación: después de escribir cada setpoint, lee de vuelta el registro y
// compara con tolerancia (default 0.1 °C / m/s). Si difiere, retorna
// DispatchResult{Aplicada: false, Motivo: "read-back mismatch ..."}.
type ModbusOvenController struct {
	client       modbus.Client
	handler      *modbus.TCPClientHandler
	slaveID      byte
	regTemp1     uint16 // holding register address (0-based)
	regVelocidad uint16
	readBackTol  float64
	timeout      time.Duration
}

// ModbusConfig es la configuración del driver Modbus.
type ModbusConfig struct {
	Address           string        // "host:502"
	SlaveID           byte          // default 1
	Timeout           time.Duration // default 2s
	RegTemp1          uint16        // 100 típico
	RegVelocidad      uint16        // 104 típico
	ReadBackTolerance float64       // default 0.1
}

// NewModbusOvenController construye el cliente contra la dirección TCP, slave ID
// y mapa de registros dados, y abre la conexión. Usa el cliente grid-x/modbus
// estándar sobre un handler TCP.
func NewModbusOvenController(cfg ModbusConfig) (*ModbusOvenController, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	handler := modbus.NewTCPClientHandler(cfg.Address)
	handler.SlaveID = cfg.SlaveID
	handler.Timeout = timeout
	if err := handler.Connect(context.Background()); err != nil {
		return nil, fmt.Errorf("modbus connect %s: %w", cfg.Address, err)
	}
	c := NewModbusOvenControllerFromClient(modbus.NewClient(handler), cfg)
	c.handler = handler
	return c, nil
}

// NewModbusOvenControllerFromClient construye el controlador sobre un
// modbus.Client ya provisto. Permite inyectar un transporte de prueba; en ese
// caso Close() es un no-op (el caller mantiene el ciclo de vida del cliente).
func NewModbusOvenControllerFromClient(client modbus.Client, cfg ModbusConfig) *ModbusOvenController {
	tol := cfg.ReadBackTolerance
	if tol <= 0 {
		tol = 0.1
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &ModbusOvenController{
		client:       client,
		slaveID:      cfg.SlaveID,
		regTemp1:     cfg.RegTemp1,
		regVelocidad: cfg.RegVelocidad,
		readBackTol:  tol,
		timeout:      timeout,
	}
}

// SendSetpoint escribe T1 y V en sus holding registers vía FC16 (Write Multiple
// Registers) y verifica read-back de cada uno. Devuelve Aplicada=false con
// Motivo ante cualquier error de escritura o mismatch de lectura.
func (c *ModbusOvenController) SendSetpoint(hornoID string, temperatura, velocidad float64) DispatchResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	// T1: float32 → 2 regs big-endian (ABCD).
	if motivo := c.writeFloat(ctx, c.regTemp1, temperatura, "T1"); motivo != "" {
		return DispatchResult{Aplicada: false, Motivo: motivo, LatencyMs: time.Since(start).Milliseconds()}
	}
	// V.
	if motivo := c.writeFloat(ctx, c.regVelocidad, velocidad, "V"); motivo != "" {
		return DispatchResult{Aplicada: false, Motivo: motivo, LatencyMs: time.Since(start).Milliseconds()}
	}

	return DispatchResult{
		Aplicada:        true,
		TemperaturaReal: temperatura,
		VelocidadReal:   velocidad,
		LatencyMs:       time.Since(start).Milliseconds(),
	}
}

// writeFloat serializa el valor en 2 holding registers big-endian, lo escribe y
// verifica el read-back. Devuelve "" si la escritura y la verificación fueron
// exitosas, o el motivo del fallo.
func (c *ModbusOvenController) writeFloat(ctx context.Context, reg uint16, valor float64, nombre string) string {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, math.Float32bits(float32(valor)))
	if _, err := c.client.WriteMultipleRegisters(ctx, reg, 2, payload); err != nil {
		return fmt.Sprintf("write %s: %v", nombre, err)
	}
	if !c.verifyReadBack(ctx, reg, float64(float32(valor))) {
		return fmt.Sprintf("read-back mismatch %s", nombre)
	}
	return ""
}

// verifyReadBack lee 2 holding registers y compara el float resultante contra el
// esperado con tolerancia readBackTol.
func (c *ModbusOvenController) verifyReadBack(ctx context.Context, reg uint16, esperado float64) bool {
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	buf, err := c.client.ReadHoldingRegisters(readCtx, reg, 2)
	if err != nil || len(buf) < 4 {
		return false
	}
	got := float64(math.Float32frombits(binary.BigEndian.Uint32(buf)))
	return math.Abs(got-esperado) < c.readBackTol
}

// Close libera la conexión TCP subyacente. Es un no-op cuando el cliente fue
// inyectado (FromClient) y el caller es dueño de su ciclo de vida.
func (c *ModbusOvenController) Close() error {
	if c.handler != nil {
		return c.handler.Close()
	}
	return nil
}
