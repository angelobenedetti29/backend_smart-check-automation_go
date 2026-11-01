package oven_controller

import (
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// OvenController es la firma de despacho que consumen los callers
// (p. ej. service.OvenController en internal/service/consigna). Se declara en
// este paquete para que el factory pueda devolver el tipo interfaz sin importar
// el paquete de servicio (importar service/consigna crearía un ciclo, ya que
// este provider es dependencia suya). Es estructuralmente idéntica a
// service.OvenController, por lo que un valor de este tipo es asignable a un
// parámetro de esa interfaz.
type OvenController interface {
	SendSetpoint(hornoID string, temperatura, velocidad float64) DispatchResult
}

// NewOvenControllerFromEnv selecciona el driver de controlador físico según
// OVEN_DRIVER y devuelve el OvenController listo para usar. Default: "simulated".
//
// Variables de entorno:
//
//	OVEN_DRIVER                  simulated|modbus (default "simulated")
//	OVEN_CONTROLLER_ENDPOINT     endpoint informativo del driver simulado
//	OVEN_MODBUS_ADDRESS          "host:502" (default "localhost:502")
//	OVEN_MODBUS_TIMEOUT          Go duration (default 2s)
//	OVEN_MODBUS_SLAVE_ID         byte (default 1)
//	OVEN_MODBUS_REG_TEMP1        uint16 (default 100)
//	OVEN_MODBUS_REG_VEL          uint16 (default 104)
//	OVEN_MODBUS_READ_BACK_TOL    float64 (default 0.1)
//
// Si OVEN_DRIVER=="modbus" y falla el Connect inicial, retorna error (no hace
// fallback a simulated: fallar ruidosamente es mejor que servir setpoints
// fantasma). El segundo valor es un io.Closer para liberar recursos del driver
// real; es nil para el simulado.
func NewOvenControllerFromEnv() (OvenController, io.Closer, error) {
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("OVEN_DRIVER")))
	if driver == "" {
		driver = "simulated"
	}
	switch driver {
	case "simulated":
		endpoint := getEnvOrDefault("OVEN_CONTROLLER_ENDPOINT", "http://localhost:8600/plc/horno")
		return NewSimulatedOvenController(endpoint), nil, nil
	case "modbus":
		cfg := ModbusConfig{
			Address:           getEnvOrDefault("OVEN_MODBUS_ADDRESS", "localhost:502"),
			SlaveID:           byte(getEnvUint16OrDefault("OVEN_MODBUS_SLAVE_ID", 1)),
			Timeout:           getEnvDurationOrDefault("OVEN_MODBUS_TIMEOUT", 2*time.Second),
			RegTemp1:          getEnvUint16OrDefault("OVEN_MODBUS_REG_TEMP1", 100),
			RegVelocidad:      getEnvUint16OrDefault("OVEN_MODBUS_REG_VEL", 104),
			ReadBackTolerance: getEnvFloatOrDefault("OVEN_MODBUS_READ_BACK_TOL", 0.1),
		}
		c, err := NewModbusOvenController(cfg)
		if err != nil {
			return nil, nil, err
		}
		return c, c, nil
	default:
		return nil, nil, fmt.Errorf("OVEN_DRIVER=%q no soportado", driver)
	}
}

// getEnvOrDefault devuelve la variable de entorno o el default si está vacía.
func getEnvOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// getEnvUint16OrDefault parsea una variable de entorno como uint16 base 10.
func getEnvUint16OrDefault(key string, def uint16) uint16 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		log.Printf("[ovenController] %s=%q inválido, usando default %d: %v", key, raw, def, err)
		return def
	}
	return uint16(v)
}

// getEnvDurationOrDefault parsea una variable de entorno como time.Duration.
func getEnvDurationOrDefault(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("[ovenController] %s=%q inválido, usando default %s: %v", key, raw, def, err)
		return def
	}
	return d
}

// getEnvFloatOrDefault parsea una variable de entorno como float64.
func getEnvFloatOrDefault(key string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		log.Printf("[ovenController] %s=%q inválido, usando default %g: %v", key, raw, def, err)
		return def
	}
	return v
}
