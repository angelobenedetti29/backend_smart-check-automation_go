// Package modbus implementa un cliente Modbus TCP mínimo, sin dependencias
// externas: solo las dos operaciones (leer/escribir holding registers) que
// necesitan los drivers de modelos de horno en internal/provider/oven_controller/.
// Es agnóstico de marca/modelo — el mapa de registros de cada dispositivo
// (ej. el NOVUS N1500 en internal/provider/oven_controller/novus) vive en su
// propio driver, no acá.
package modbus

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Function codes Modbus implementados (los únicos que necesitan los drivers
// actuales: escribir un setpoint y releerlo para confirmar).
const (
	fcReadHoldingRegisters = 0x03
	fcWriteSingleRegister  = 0x06

	exceptionBit = 0x80 // OR-eado a la function code cuando el dispositivo responde una excepción
)

// ErrUnexpectedResponse indica que el frame de respuesta no tiene el formato
// esperado (largo o function code inconsistente).
var ErrUnexpectedResponse = errors.New("modbus: respuesta inesperada del dispositivo")

// Client es un cliente Modbus TCP: se conecta a un gateway o dispositivo que
// hable Modbus TCP directamente (ej. un conversor RS-485↔Ethernet) y opera
// sobre un único Unit ID (esclavo) detrás de ese gateway.
//
// Abre una conexión TCP nueva por cada operación en vez de mantener un socket
// persistente: para el volumen de despachos de consigna (no es telemetría de
// alta frecuencia) es más simple y más resiliente ante cortes de red — no hay
// riesgo de reusar una conexión que quedó en un estado zombie.
type Client struct {
	addr    string
	unitID  byte
	timeout time.Duration
}

// NewClient instancia un cliente Modbus TCP contra host:port, dirigido al
// Unit ID indicado (el ID de esclavo Modbus configurado en el dispositivo
// físico, no el del gateway).
func NewClient(host string, port int, unitID byte, timeout time.Duration) *Client {
	return &Client{
		addr:    fmt.Sprintf("%s:%d", host, port),
		unitID:  unitID,
		timeout: timeout,
	}
}

// ReadHoldingRegisters lee `quantity` holding registers (function code 0x03)
// a partir de `address`.
func (c *Client) ReadHoldingRegisters(address, quantity uint16) ([]uint16, error) {
	reqData := make([]byte, 4)
	binary.BigEndian.PutUint16(reqData[0:2], address)
	binary.BigEndian.PutUint16(reqData[2:4], quantity)

	resp, err := c.doRequest(fcReadHoldingRegisters, reqData)
	if err != nil {
		return nil, err
	}

	if len(resp) < 1 || int(resp[0]) != len(resp)-1 || int(resp[0]) != int(quantity)*2 {
		return nil, ErrUnexpectedResponse
	}

	regs := make([]uint16, quantity)
	for i := 0; i < int(quantity); i++ {
		regs[i] = binary.BigEndian.Uint16(resp[1+i*2 : 3+i*2])
	}
	return regs, nil
}

// WriteSingleRegister escribe `value` en `address` (function code 0x06). Por
// especificación Modbus, la respuesta exitosa a esta función es un eco del
// pedido (dirección y valor) — confirma que el dispositivo recibió el frame
// sin corrupción, pero NO garantiza que haya guardado ese valor tal cual: un
// controlador de proceso puede clampearlo silenciosamente a sus límites
// internos (ej. SPLL/SPHL del NOVUS N1500). Releer el registro después de
// escribir es responsabilidad del driver del modelo (ver internal/provider/oven_controller/novus),
// no de este cliente genérico.
func (c *Client) WriteSingleRegister(address, value uint16) error {
	reqData := make([]byte, 4)
	binary.BigEndian.PutUint16(reqData[0:2], address)
	binary.BigEndian.PutUint16(reqData[2:4], value)

	resp, err := c.doRequest(fcWriteSingleRegister, reqData)
	if err != nil {
		return err
	}
	if len(resp) != 4 {
		return ErrUnexpectedResponse
	}

	echoedAddr := binary.BigEndian.Uint16(resp[0:2])
	echoedVal := binary.BigEndian.Uint16(resp[2:4])
	if echoedAddr != address || echoedVal != value {
		return fmt.Errorf("modbus: el dispositivo no confirmó el frame enviado (registro %d, valor %d; eco recibido: registro %d, valor %d)", address, value, echoedAddr, echoedVal)
	}
	return nil
}

// doRequest arma un frame MBAP (cabecera Modbus TCP) con la function code y
// los datos indicados, lo envía por una conexión nueva y devuelve el payload
// de datos de la respuesta (sin cabecera MBAP ni function code).
func (c *Client) doRequest(functionCode byte, data []byte) ([]byte, error) {
	conn, err := net.DialTimeout("tcp", c.addr, c.timeout)
	if err != nil {
		return nil, fmt.Errorf("modbus: no se pudo conectar a %s: %w", c.addr, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, fmt.Errorf("modbus: no se pudo fijar el timeout de conexión a %s: %w", c.addr, err)
	}

	pdu := append([]byte{functionCode}, data...)

	header := make([]byte, 7)
	binary.BigEndian.PutUint16(header[0:2], transactionID()) // Transaction ID: solo para correlacionar en logs, no hay pipelining acá
	binary.BigEndian.PutUint16(header[2:4], 0)                // Protocol ID: siempre 0 para Modbus
	binary.BigEndian.PutUint16(header[4:6], uint16(len(pdu)+1)) // Length: Unit ID + PDU que siguen
	header[6] = c.unitID

	if _, err := conn.Write(append(header, pdu...)); err != nil {
		return nil, fmt.Errorf("modbus: error al escribir en %s: %w", c.addr, err)
	}

	respHeader := make([]byte, 7)
	if _, err := io.ReadFull(conn, respHeader); err != nil {
		return nil, fmt.Errorf("modbus: error al leer la cabecera de respuesta de %s: %w", c.addr, err)
	}
	respLen := binary.BigEndian.Uint16(respHeader[4:6])
	if respLen < 2 { // como mínimo: Unit ID (ya leído) + function code
		return nil, ErrUnexpectedResponse
	}

	respBody := make([]byte, respLen-1) // -1: el Unit ID ya vino en respHeader
	if _, err := io.ReadFull(conn, respBody); err != nil {
		return nil, fmt.Errorf("modbus: error al leer el cuerpo de respuesta de %s: %w", c.addr, err)
	}

	respFunctionCode := respBody[0]
	if respFunctionCode == functionCode|exceptionBit {
		exceptionCode := byte(0)
		if len(respBody) > 1 {
			exceptionCode = respBody[1]
		}
		return nil, fmt.Errorf("modbus: %s (%s) respondió excepción 0x%02X para function code 0x%02X", c.addr, unitLabel(c.unitID), exceptionCode, functionCode)
	}
	if respFunctionCode != functionCode {
		return nil, ErrUnexpectedResponse
	}

	return respBody[1:], nil
}

// transactionID genera un identificador de transacción Modbus TCP
// razonablemente único para logging/correlación (no hay pipelining de
// requests en este cliente, así que no necesita ser estrictamente secuencial).
func transactionID() uint16 {
	return uint16(time.Now().UnixNano())
}

func unitLabel(unitID byte) string {
	return fmt.Sprintf("unit %d", unitID)
}
