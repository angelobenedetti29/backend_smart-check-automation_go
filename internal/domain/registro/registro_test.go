package registro

import (
	"strings"
	"testing"
)

func TestCreateRequestValidate_TypeValido(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"entrada", "ENTRADA_HORNO", "ENTRADA_HORNO"},
		{"salida", "SALIDA_HORNO", "SALIDA_HORNO"},
		{"minúsculas", "entrada_horno", "ENTRADA_HORNO"},
		{"con espacios", "  salida_horno  ", "SALIDA_HORNO"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := CreateRequest{Hostname: "rpi-01", Type: tc.raw}
			if err := req.Validate(); err != nil {
				t.Fatalf("expected valid type, got: %v", err)
			}
			if req.Type != tc.want {
				t.Fatalf("expected normalized type %q, got %q", tc.want, req.Type)
			}
		})
	}
}

func TestCreateRequestValidate_TypeInvalido(t *testing.T) {
	for _, raw := range []string{"", "   ", "ENTRADA", "HORNO", "entrada-horno"} {
		t.Run(raw, func(t *testing.T) {
			req := CreateRequest{Hostname: "rpi-01", Type: raw}
			err := req.Validate()
			if err == nil {
				t.Fatal("expected validation error for invalid/absent type")
			}
			if !strings.Contains(err.Error(), "type: debe ser ENTRADA_HORNO o SALIDA_HORNO") {
				t.Fatalf("expected clear type error, got: %v", err)
			}
		})
	}
}

func TestCreateRequestValidate_HostnameInvalido(t *testing.T) {
	req := CreateRequest{Hostname: "  ", Type: "ENTRADA_HORNO"}
	if err := req.Validate(); err == nil {
		t.Fatal("expected hostname validation error")
	}
}
