package dispositivo

import (
	"errors"
	"testing"
)

func TestPingRequestValidate_Valid(t *testing.T) {
	req := PingRequest{
		DispositivoID:      "b1c2d3e4-5678-90ab-cdef-1234567890ab",
		CpuPct:             42.5,
		MemRamDisponibleMb: 512.0,
		TempChip:           58.3,
	}

	if err := req.Validate(); err != nil {
		t.Fatalf("expected no validation error, got: %v", err)
	}
}

func TestPingRequestValidate_Invalid(t *testing.T) {
	req := PingRequest{
		DispositivoID:      "   ",
		CpuPct:             120.0,
		MemRamDisponibleMb: -1.0,
		TempChip:           200.0,
	}

	err := req.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}

	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(ve.Fields) != 4 {
		t.Fatalf("expected 4 field errors, got %d: %v", len(ve.Fields), ve.Fields)
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
			req := PingRequest{DispositivoID: "d1", CpuPct: tt.cpu}
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
