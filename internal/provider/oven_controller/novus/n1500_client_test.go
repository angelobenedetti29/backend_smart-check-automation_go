package novus

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRegisterWriter es un doble de prueba determinista de registerWriter:
// simula un dispositivo Modbus en memoria, con soporte para forzar clamps o
// errores por registro, sin abrir sockets reales.
type fakeRegisterWriter struct {
	values     map[uint16]uint16
	clampTo    map[uint16]uint16 // si está seteado para un registro, ReadHoldingRegisters devuelve esto en vez de lo escrito
	writeErr   map[uint16]error
	readErr    map[uint16]error
	writeCalls int
}

func newFakeRegisterWriter() *fakeRegisterWriter {
	return &fakeRegisterWriter{
		values:   make(map[uint16]uint16),
		clampTo:  make(map[uint16]uint16),
		writeErr: make(map[uint16]error),
		readErr:  make(map[uint16]error),
	}
}

func (f *fakeRegisterWriter) WriteSingleRegister(address, value uint16) error {
	f.writeCalls++
	if err, ok := f.writeErr[address]; ok {
		return err
	}
	f.values[address] = value
	return nil
}

func (f *fakeRegisterWriter) ReadHoldingRegisters(address, quantity uint16) ([]uint16, error) {
	if err, ok := f.readErr[address]; ok {
		return nil, err
	}
	if clamped, ok := f.clampTo[address]; ok {
		return []uint16{clamped}, nil
	}
	return []uint16{f.values[address]}, nil
}

func testConfig(tempWriter, velWriter *fakeRegisterWriter) (N1500Config, map[string]*fakeRegisterWriter) {
	cfg := N1500Config{
		Temperatura: DeviceConfig{Host: "temp-gw", Port: 502, UnitID: 1, SetpointRegister: 10, Decimals: 1},
		Velocidad:   DeviceConfig{Host: "vel-gw", Port: 502, UnitID: 2, SetpointRegister: 20, Decimals: 2},
	}
	writers := map[string]*fakeRegisterWriter{
		cfg.Temperatura.Host: tempWriter,
		cfg.Velocidad.Host:   velWriter,
	}
	return cfg, writers
}

func newClientWithFakes(t *testing.T, hornoID string, cfg N1500Config, writers map[string]*fakeRegisterWriter) *N1500Client {
	t.Helper()
	c := NewN1500Client(map[string]N1500Config{hornoID: cfg}, time.Second)
	c.dial = func(devCfg DeviceConfig, _ time.Duration) registerWriter {
		w, ok := writers[devCfg.Host]
		require.True(t, ok, "no hay fake configurado para host %s", devCfg.Host)
		return w
	}
	return c
}

func TestSendSetpoint_Success(t *testing.T) {
	tempW, velW := newFakeRegisterWriter(), newFakeRegisterWriter()
	cfg, writers := testConfig(tempW, velW)
	c := newClientWithFakes(t, "horno-01", cfg, writers)

	result := c.SendSetpoint("horno-01", 220.5, 1.75)

	require.True(t, result.Aplicada)
	assert.Equal(t, 220.5, result.TemperaturaReal)
	assert.Equal(t, 1.75, result.VelocidadReal)
	assert.Equal(t, uint16(2205), tempW.values[10]) // 220.5 * 10^1
	assert.Equal(t, uint16(175), velW.values[20])   // 1.75 * 10^2
}

func TestSendSetpoint_HornoNoConfigurado(t *testing.T) {
	cfg, writers := testConfig(newFakeRegisterWriter(), newFakeRegisterWriter())
	c := newClientWithFakes(t, "horno-01", cfg, writers)

	result := c.SendSetpoint("horno-99", 220, 1.5)

	assert.False(t, result.Aplicada)
	assert.Contains(t, result.Motivo, "horno-99")
}

func TestSendSetpoint_FalloDeEscrituraEnTemperatura(t *testing.T) {
	tempW, velW := newFakeRegisterWriter(), newFakeRegisterWriter()
	tempW.writeErr[10] = errors.New("timeout modbus")
	cfg, writers := testConfig(tempW, velW)
	c := newClientWithFakes(t, "horno-01", cfg, writers)

	result := c.SendSetpoint("horno-01", 220, 1.5)

	assert.False(t, result.Aplicada)
	assert.Contains(t, result.Motivo, "temperatura")
	assert.Equal(t, 0, velW.writeCalls, "no debería intentar velocidad si temperatura ya falló")
}

func TestSendSetpoint_ClampSilenciosoDetectado(t *testing.T) {
	tempW, velW := newFakeRegisterWriter(), newFakeRegisterWriter()
	tempW.clampTo[10] = 2500 // pidió 220.5°C, el equipo clampeó a 250.0°C (SPHL)
	cfg, writers := testConfig(tempW, velW)
	c := newClientWithFakes(t, "horno-01", cfg, writers)

	result := c.SendSetpoint("horno-01", 220.5, 1.5)

	require.True(t, result.Aplicada) // el equipo aceptó *algo*, no rechazó la escritura
	assert.Equal(t, 250.0, result.TemperaturaReal, "debe reportar el valor real confirmado, no el pedido")
}
