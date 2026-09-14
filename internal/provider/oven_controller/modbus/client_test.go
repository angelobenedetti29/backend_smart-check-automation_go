package modbus

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSlave levanta un listener TCP que habla el framing MBAP mínimo
// necesario para validar el cliente contra un "dispositivo" determinista,
// sin depender de hardware real. handle recibe la function code y los datos
// del pedido, y devuelve los datos de la respuesta (o un exceptionCode > 0).
func fakeSlave(t *testing.T, handle func(functionCode byte, data []byte) (respData []byte, exceptionCode byte)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		header := make([]byte, 7)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		reqLen := binary.BigEndian.Uint16(header[4:6])
		body := make([]byte, reqLen-1)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		functionCode := body[0]
		respData, exceptionCode := handle(functionCode, body[1:])

		var respFC byte
		var payload []byte
		if exceptionCode != 0 {
			respFC = functionCode | exceptionBit
			payload = []byte{exceptionCode}
		} else {
			respFC = functionCode
			payload = respData
		}

		respPDU := append([]byte{respFC}, payload...)
		respHeader := make([]byte, 7)
		copy(respHeader[0:4], header[0:4]) // eco de transaction ID + protocol ID
		binary.BigEndian.PutUint16(respHeader[4:6], uint16(len(respPDU)+1))
		respHeader[6] = header[6] // eco del unit ID

		conn.Write(append(respHeader, respPDU...))
	}()

	return ln.Addr().String()
}

func TestClient_ReadHoldingRegisters(t *testing.T) {
	addr := fakeSlave(t, func(functionCode byte, data []byte) ([]byte, byte) {
		assert.Equal(t, byte(fcReadHoldingRegisters), functionCode)
		assert.Equal(t, uint16(10), binary.BigEndian.Uint16(data[0:2])) // address
		assert.Equal(t, uint16(1), binary.BigEndian.Uint16(data[2:4])) // quantity

		respData := make([]byte, 3)
		respData[0] = 2 // byte count
		binary.BigEndian.PutUint16(respData[1:3], 253)
		return respData, 0
	})

	host, port := splitAddr(t, addr)
	c := NewClient(host, port, 1, time.Second)

	regs, err := c.ReadHoldingRegisters(10, 1)
	require.NoError(t, err)
	assert.Equal(t, []uint16{253}, regs)
}

func TestClient_WriteSingleRegister_Success(t *testing.T) {
	addr := fakeSlave(t, func(functionCode byte, data []byte) ([]byte, byte) {
		assert.Equal(t, byte(fcWriteSingleRegister), functionCode)
		// Eco fiel del pedido, como especifica Modbus para FC06.
		return data, 0
	})

	host, port := splitAddr(t, addr)
	c := NewClient(host, port, 1, time.Second)

	err := c.WriteSingleRegister(20, 1800)
	require.NoError(t, err)
}

func TestClient_WriteSingleRegister_MismatchedEcho(t *testing.T) {
	addr := fakeSlave(t, func(functionCode byte, data []byte) ([]byte, byte) {
		// Simula un dispositivo que clampeó el valor antes de ecoarlo.
		mismatched := make([]byte, 4)
		binary.BigEndian.PutUint16(mismatched[0:2], binary.BigEndian.Uint16(data[0:2]))
		binary.BigEndian.PutUint16(mismatched[2:4], 999)
		return mismatched, 0
	})

	host, port := splitAddr(t, addr)
	c := NewClient(host, port, 1, time.Second)

	err := c.WriteSingleRegister(20, 1800)
	assert.Error(t, err)
}

func TestClient_ExceptionResponse(t *testing.T) {
	addr := fakeSlave(t, func(functionCode byte, data []byte) ([]byte, byte) {
		return nil, 0x02 // Illegal Data Address
	})

	host, port := splitAddr(t, addr)
	c := NewClient(host, port, 1, time.Second)

	_, err := c.ReadHoldingRegisters(9999, 1)
	assert.ErrorContains(t, err, "excepción")
}

func TestClient_ConnectionRefused(t *testing.T) {
	c := NewClient("127.0.0.1", 1, 1, 50*time.Millisecond) // puerto 1: nadie escucha
	_, err := c.ReadHoldingRegisters(0, 1)
	assert.Error(t, err)
}

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscan(portStr, &port)
	require.NoError(t, err)
	return host, port
}
