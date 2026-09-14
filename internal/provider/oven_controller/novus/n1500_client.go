// Package novus implementa el driver de despacho de consignas para hornos
// equipados con un indicador/controlador de temperatura NOVUS N1500 y un
// variador de frecuencia (VFD) para la velocidad de la cinta, ambos
// alcanzables vía Modbus TCP.
//
// Es una capa intercambiable: implementa la misma firma que
// oven_controller.OvenControllerClient (el simulador) y que tendría el
// driver de cualquier otro modelo de horno. El único lugar del sistema que
// conoce el mapa de registros del N1500 es este paquete — ConsignaService
// (internal/service/consigna) solo conoce la interfaz genérica
// consigna.OvenController. Cambiar de modelo de horno es escribir un paquete
// análogo a este e inyectarlo en su lugar en cmd/server/main.go.
package novus

import (
	"fmt"
	"log"
	"math"
	"time"

	"github.com/angelobenedetti29/smart-check-automation/internal/provider/oven_controller"
	"github.com/angelobenedetti29/smart-check-automation/internal/provider/oven_controller/modbus"
)

// registerWriter abstrae el transporte Modbus usado para hablar con cada
// dispositivo. Hoy lo satisface *modbus.Client (Modbus TCP); el día que haga
// falta hablar Modbus RTU por puerto serie directo (sin gateway), basta con
// una implementación distinta con esta misma firma — este driver no cambia.
// Como interfaz mínima, también permite inyectar un doble determinista en tests.
type registerWriter interface {
	WriteSingleRegister(address, value uint16) error
	ReadHoldingRegisters(address, quantity uint16) ([]uint16, error)
}

// DeviceConfig describe cómo alcanzar y direccionar, vía Modbus TCP, UN
// dispositivo físico puntual (el N1500 de temperatura, o el variador de
// velocidad de cinta).
//
// IMPORTANTE: SetpointRegister, PVRegister y Decimals son específicos de la
// configuración de cada equipo en planta. Deben confirmarse contra la
// "Tabela de Registradores para Comunicação Serial" del firmware instalado
// (varía entre versiones de firmware y según el equipo sea el N1500 o el
// variador) antes de apuntar esto a hardware real — no asumir estos valores.
type DeviceConfig struct {
	Host             string
	Port             int
	UnitID           byte   // Unit/Slave ID Modbus configurado en el equipo (no el del gateway)
	SetpointRegister uint16 // registro holding donde se escribe el setpoint
	PVRegister       uint16 // registro holding donde se lee el valor de proceso real medido (no usado hoy, reservado para telemetría fina)
	Decimals         uint   // decimales configurados en el equipo (parámetro "Pt" del N1500): escala el valor crudo del registro (ej. 1 decimal → valor_real = crudo / 10)
}

// N1500Config agrupa, para UN horno, los dos dispositivos Modbus que en
// conjunto conforman "el controlador físico" a nivel de negocio: el N1500
// que regula temperatura y el variador que regula la velocidad de cinta. En
// una planta sin variador propio, Velocidad puede apuntar al mismo
// dispositivo/registro que Temperatura si así está cableado.
type N1500Config struct {
	Temperatura DeviceConfig
	Velocidad   DeviceConfig
}

// dialFunc crea el registerWriter para un DeviceConfig dado. Es un campo del
// struct (no una llamada directa a modbus.NewClient) para poder inyectar un
// doble de prueba determinista en los tests sin abrir sockets reales.
type dialFunc func(cfg DeviceConfig, timeout time.Duration) registerWriter

// N1500Client despacha consignas de temperatura y velocidad de cinta a
// hornos equipados con NOVUS N1500 + variador, vía Modbus TCP — típicamente
// a través de un gateway RS-485↔Ethernet, ya que el N1500 nativamente solo
// habla Modbus RTU por RS-485.
type N1500Client struct {
	devices map[string]N1500Config // hornoID -> configuración de sus dos dispositivos Modbus
	timeout time.Duration
	dial    dialFunc
}

// NewN1500Client instancia el driver para el catálogo de hornos indicado
// (hornoID -> configuración Modbus de sus dispositivos).
func NewN1500Client(devices map[string]N1500Config, timeout time.Duration) *N1500Client {
	return &N1500Client{
		devices: devices,
		timeout: timeout,
		dial: func(cfg DeviceConfig, timeout time.Duration) registerWriter {
			return modbus.NewClient(cfg.Host, cfg.Port, cfg.UnitID, timeout)
		},
	}
}

// SendSetpoint despacha temperatura y velocidad al N1500/variador del horno
// hornoID vía Modbus TCP. Misma firma que oven_controller.OvenControllerClient.SendSetpoint,
// por lo que es un reemplazo directo del simulador (o de cualquier otro
// driver) donde se inyecta consigna.OvenController en cmd/server/main.go.
//
// Escribe ambos setpoints y relee cada registro para confirmar que el equipo
// los aceptó tal cual: el N1500 (como la mayoría de los controladores de
// proceso) clampea silenciosamente cualquier valor fuera de sus límites
// internos (SPLL/SPHL) en vez de rechazar la escritura, así que la única
// forma de detectarlo es releer — devolver "Aplicada: true" con un valor
// distinto al pedido sería mentirle a la auditoría (ver consigna.Consigna).
func (c *N1500Client) SendSetpoint(hornoID string, temperatura, velocidad float64) oven_controller.DispatchResult {
	start := time.Now()

	cfg, ok := c.devices[hornoID]
	if !ok {
		return oven_controller.DispatchResult{
			Aplicada: false,
			Motivo:   fmt.Sprintf("horno %q no está configurado en el driver Novus N1500", hornoID),
		}
	}

	tempReal, err := c.applySetpoint(cfg.Temperatura, temperatura)
	if err != nil {
		log.Printf("[NovusN1500] Fallo al despachar temperatura a horno=%s: %v", hornoID, err)
		return oven_controller.DispatchResult{
			Aplicada:  false,
			Motivo:    fmt.Sprintf("temperatura: %v", err),
			LatencyMs: time.Since(start).Milliseconds(),
		}
	}

	velReal, err := c.applySetpoint(cfg.Velocidad, velocidad)
	if err != nil {
		log.Printf("[NovusN1500] Fallo al despachar velocidad de cinta a horno=%s: %v", hornoID, err)
		return oven_controller.DispatchResult{
			Aplicada:  false,
			Motivo:    fmt.Sprintf("velocidad de cinta: %v", err),
			LatencyMs: time.Since(start).Milliseconds(),
		}
	}

	return oven_controller.DispatchResult{
		Aplicada:        true,
		TemperaturaReal: tempReal,
		VelocidadReal:   velReal,
		LatencyMs:       time.Since(start).Milliseconds(),
	}
}

// applySetpoint escribe `valor` (escalado según cfg.Decimals) en
// cfg.SetpointRegister y relee el mismo registro para devolver el valor real
// que el equipo terminó aceptando.
func (c *N1500Client) applySetpoint(cfg DeviceConfig, valor float64) (float64, error) {
	scale := math.Pow10(int(cfg.Decimals))
	rawFloat := math.Round(valor * scale)
	if rawFloat < 0 || rawFloat > math.MaxUint16 {
		return 0, fmt.Errorf("valor %.2f fuera del rango representable por un registro holding (equipo %s:%d unit %d)", valor, cfg.Host, cfg.Port, cfg.UnitID)
	}

	client := c.dial(cfg, c.timeout)

	if err := client.WriteSingleRegister(cfg.SetpointRegister, uint16(rawFloat)); err != nil {
		return 0, fmt.Errorf("no se pudo escribir el setpoint en %s:%d (unit %d, registro %d): %w", cfg.Host, cfg.Port, cfg.UnitID, cfg.SetpointRegister, err)
	}

	regs, err := client.ReadHoldingRegisters(cfg.SetpointRegister, 1)
	if err != nil {
		return 0, fmt.Errorf("no se pudo confirmar el setpoint en %s:%d (unit %d, registro %d): %w", cfg.Host, cfg.Port, cfg.UnitID, cfg.SetpointRegister, err)
	}

	return float64(regs[0]) / scale, nil
}
