package dispositivo

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const testDispositivoUUID = "b1c2d3e4-5678-90ab-cdef-1234567890ab"

func TestPingRequestValidate_Valid(t *testing.T) {
	req := PingRequest{
		DispositivoID:      "b1c2d3e4-5678-90ab-cdef-1234567890ab",
		CpuPct:             42.5,
		MemRamDisponibleMb: 512.0,
		TempChip:           58.3,
		AiProcessorPct:     42.5,
	}

	if err := req.Validate(); err != nil {
		t.Fatalf("expected no validation error, got: %v", err)
	}
}

func TestPingRequestValidate_TelemetriaExtendida(t *testing.T) {
	memTotal := 1024.0
	almacenamientoDisponible := 20000.0
	almacenamientoTotal := 64000.0
	req := PingRequest{
		DispositivoID:              testDispositivoUUID,
		MemRamDisponibleMb:         512,
		MemRamTotalMb:              &memTotal,
		AlmacenamientoDisponibleMb: &almacenamientoDisponible,
		AlmacenamientoTotalMb:      &almacenamientoTotal,
	}

	if err := req.Validate(); err != nil {
		t.Fatalf("expected extended telemetry to be valid, got: %v", err)
	}
}

func TestPingRequestValidate_DispositivoIDDebeSerUUID(t *testing.T) {
	req := PingRequest{DispositivoID: "no-es-un-uuid"}

	err := req.Validate()
	if err == nil || !strings.Contains(err.Error(), "dispositivoId: debe ser un UUID válido") {
		t.Fatalf("expected UUID validation error, got: %v", err)
	}
}

func TestPingRequestValidate_TelemetriaExtendidaMantieneCompatibilidadLegacy(t *testing.T) {
	req := PingRequest{DispositivoID: testDispositivoUUID, MemRamDisponibleMb: 512}

	if err := req.Validate(); err != nil {
		t.Fatalf("expected legacy telemetry without totals to remain valid, got: %v", err)
	}
}

func TestPingRequestValidate_TelemetriaExtendidaRechazaRelacionesInvalidas(t *testing.T) {
	memTotal := 100.0
	almacenamientoDisponible := 200.0
	almacenamientoTotal := 100.0
	req := PingRequest{
		DispositivoID:              testDispositivoUUID,
		MemRamDisponibleMb:         101,
		MemRamTotalMb:              &memTotal,
		AlmacenamientoDisponibleMb: &almacenamientoDisponible,
		AlmacenamientoTotalMb:      &almacenamientoTotal,
	}

	err := req.Validate()
	if err == nil {
		t.Fatal("expected invalid free/total relationships to be rejected")
	}
	if !strings.Contains(err.Error(), "memRamDisponibleMb: no puede superar memRamTotalMb") ||
		!strings.Contains(err.Error(), "almacenamientoDisponibleMb: no puede superar almacenamientoTotalMb") {
		t.Fatalf("expected both relationship errors, got: %v", err)
	}
}

func TestPingRequestValidate_AlmacenamientoDebeInformarseComoPar(t *testing.T) {
	almacenamiento := 100.0
	tests := []PingRequest{
		{DispositivoID: testDispositivoUUID, AlmacenamientoDisponibleMb: &almacenamiento},
		{DispositivoID: testDispositivoUUID, AlmacenamientoTotalMb: &almacenamiento},
	}

	for i, req := range tests {
		if err := req.Validate(); err == nil || !strings.Contains(err.Error(), "almacenamientoDisponibleMb y almacenamientoTotalMb: deben informarse juntos") {
			t.Errorf("case %d: expected atomic storage pair validation error, got %v", i, err)
		}
	}
}

func TestPingRequestValidate_TelemetriaExtendidaRechazaTotalesNegativos(t *testing.T) {
	memTotal := -1.0
	almacenamientoDisponible := -1.0
	almacenamientoTotal := -1.0
	req := PingRequest{
		DispositivoID:              testDispositivoUUID,
		MemRamTotalMb:              &memTotal,
		AlmacenamientoDisponibleMb: &almacenamientoDisponible,
		AlmacenamientoTotalMb:      &almacenamientoTotal,
	}

	err := req.Validate()
	if err == nil {
		t.Fatal("expected negative optional telemetry values to be rejected")
	}
	for _, field := range []string{"memRamTotalMb: no puede ser negativo", "almacenamientoDisponibleMb: no puede ser negativo", "almacenamientoTotalMb: no puede ser negativo"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("expected error %q, got: %v", field, err)
		}
	}
}

func TestPingRequestValidate_Invalid(t *testing.T) {
	req := PingRequest{
		DispositivoID:      "   ",
		CpuPct:             120.0,
		MemRamDisponibleMb: -1.0,
		TempChip:           200.0,
		AiProcessorPct:     150.0,
	}

	err := req.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}

	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(ve.Fields) != 5 {
		t.Fatalf("expected 5 field errors, got %d: %v", len(ve.Fields), ve.Fields)
	}
}

func TestPingRequestValidate_BoundariesCpu(t *testing.T) {
	tests := []struct {
		name string
		cpu  float64
		ok   bool
	}{
		{"cpu min 0", 0, true},
		{"cpu max 100", 100, true},
		{"cpu negativo", -0.01, false},
		{"cpu sobre 100", 100.01, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := PingRequest{DispositivoID: testDispositivoUUID, CpuPct: tt.cpu}
			err := req.Validate()
			if tt.ok && err != nil {
				t.Fatalf("expected valid, got: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected invalid")
			}
		})
	}
}

func TestPingRequestValidate_BoundariesAiProcessor(t *testing.T) {
	tests := []struct {
		name string
		ai   float64
		ok   bool
	}{
		{"ai min 0", 0, true},
		{"ai max 100", 100, true},
		{"ai negativo", -0.01, false},
		{"ai sobre 100", 100.01, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := PingRequest{DispositivoID: testDispositivoUUID, AiProcessorPct: tt.ai}
			err := req.Validate()
			if tt.ok && err != nil {
				t.Fatalf("expected valid, got: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected invalid")
			}
		})
	}
}

func TestCreateDispositivoRequestValidate_Valid(t *testing.T) {
	req := CreateDispositivoRequest{
		Nombre:    "Raspberry Pi Horno 2",
		Ubicacion: "Línea B",
	}

	if err := req.Validate(); err != nil {
		t.Fatalf("expected no validation error, got: %v", err)
	}
}

func TestCreateDispositivoRequestValidate_ValidWithEmptyUbicacion(t *testing.T) {
	req := CreateDispositivoRequest{Nombre: "Raspberry Pi Horno 2"}

	if err := req.Validate(); err != nil {
		t.Fatalf("expected no validation error for optional ubicacion, got: %v", err)
	}
}

func TestCreateDispositivoRequestValidate_NombreRequired(t *testing.T) {
	req := CreateDispositivoRequest{Nombre: "   ", Ubicacion: "Línea B"}

	err := req.Validate()
	if err == nil {
		t.Fatal("expected validation error for empty nombre")
	}

	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(ve.Fields) != 1 || ve.Fields[0] != "nombre: es requerido" {
		t.Fatalf("expected nombre field error, got %v", ve.Fields)
	}
}

func TestCreateDispositivoRequestValidate_NombreTooLong(t *testing.T) {
	long := ""
	for i := 0; i < 101; i++ {
		long += "a"
	}
	req := CreateDispositivoRequest{Nombre: long}

	err := req.Validate()
	if err == nil {
		t.Fatal("expected validation error for nombre over 100 chars")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
}

func TestCreateDispositivoRequestValidate_UbicacionTooLong(t *testing.T) {
	long := ""
	for i := 0; i < 101; i++ {
		long += "b"
	}
	req := CreateDispositivoRequest{Nombre: "Pi 1", Ubicacion: long}

	err := req.Validate()
	if err == nil {
		t.Fatal("expected validation error for ubicacion over 100 chars")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
}

func TestCreateDispositivoRequestValidate_NombreAtMaxLengthIsValid(t *testing.T) {
	long := ""
	for i := 0; i < 100; i++ {
		long += "a"
	}
	req := CreateDispositivoRequest{Nombre: long, Ubicacion: long}

	if err := req.Validate(); err != nil {
		t.Fatalf("expected valid at exactly 100 chars, got: %v", err)
	}
}

func TestCreateDispositivoRequestValidate_WhepURLValida(t *testing.T) {
	req := CreateDispositivoRequest{
		Nombre:  "Raspberry Pi Horno 2",
		WhepURL: "https://camaras.example.com/whep/horno-2",
	}

	if err := req.Validate(); err != nil {
		t.Fatalf("expected valid whepUrl, got: %v", err)
	}
}

func TestCreateDispositivoRequestValidate_WhepURLEsOpcional(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		req := CreateDispositivoRequest{Nombre: "Raspberry Pi Horno 2", WhepURL: raw}
		if err := req.Validate(); err != nil {
			t.Fatalf("expected omitted/empty whepUrl to be valid, got: %v", err)
		}
	}
}

func TestCreateDispositivoRequestValidate_WhepURLInvalida(t *testing.T) {
	invalids := []string{
		"no-es-una-url",
		"/whep/horno-2",
		"ftp://camaras.example.com/whep/horno-2",
		"https://",
	}

	for _, raw := range invalids {
		req := CreateDispositivoRequest{Nombre: "Raspberry Pi Horno 2", WhepURL: raw}
		err := req.Validate()
		if err == nil || !strings.Contains(err.Error(), "whepUrl: debe ser una URL absoluta http o https") {
			t.Errorf("expected invalid whepUrl error for %q, got: %v", raw, err)
		}
	}
}

func TestCreateDispositivoRequestValidate_WhepURLTooLong(t *testing.T) {
	long := "https://camaras.example.com/whep/" + strings.Repeat("a", 500)
	req := CreateDispositivoRequest{Nombre: "Raspberry Pi Horno 2", WhepURL: long}

	err := req.Validate()
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("whepUrl: no puede superar los %d caracteres", maxWhepURLLength)) {
		t.Fatalf("expected whepUrl length error, got: %v", err)
	}
}

func TestCreateDispositivoRequestValidate_WhepURLAtMaxLengthIsValid(t *testing.T) {
	base := "https://camaras.example.com/whep/"
	req := CreateDispositivoRequest{Nombre: "Raspberry Pi Horno 2", WhepURL: base + strings.Repeat("a", maxWhepURLLength-len(base))}

	if err := req.Validate(); err != nil {
		t.Fatalf("expected valid whepUrl at exactly %d runes, got: %v", maxWhepURLLength, err)
	}
}

func TestUpdateDispositivoRequestValidate_WhepURL(t *testing.T) {
	valid := UpdateDispositivoRequest{DispositivoID: testDispositivoUUID, Nombre: "Pi 1", WhepURL: "http://camaras.example.com/whep/horno-1"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid whepUrl in update, got: %v", err)
	}

	invalid := UpdateDispositivoRequest{DispositivoID: testDispositivoUUID, Nombre: "Pi 1", WhepURL: "no-es-url"}
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "whepUrl: debe ser una URL absoluta http o https") {
		t.Fatalf("expected invalid whepUrl error in update, got: %v", err)
	}
}
