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

func TestCreateRequestValidate_RenameURL(t *testing.T) {
	t.Run("ausente es válido (nodo legacy)", func(t *testing.T) {
		req := CreateRequest{Hostname: "rpi-01", Type: "ENTRADA_HORNO"}
		if err := req.Validate(); err != nil {
			t.Fatalf("expected valid request without rename_url, got %v", err)
		}
		if req.RenameURL != nil {
			t.Fatalf("expected nil rename_url, got %v", req.RenameURL)
		}
	})

	t.Run("presente es válido y trimea la URL", func(t *testing.T) {
		raw := "  http://192.168.0.10:9000  "
		req := CreateRequest{Hostname: "rpi-01", Type: "ENTRADA_HORNO", RenameURL: &raw}
		if err := req.Validate(); err != nil {
			t.Fatalf("expected valid request with rename_url, got %v", err)
		}
		if req.RenameURL == nil || *req.RenameURL != "http://192.168.0.10:9000" {
			t.Fatalf("expected trimmed rename_url, got %v", req.RenameURL)
		}
	})

	t.Run("una URL https de túnel es válida", func(t *testing.T) {
		raw := "https://mi-tunel.trycloudflare.com"
		req := CreateRequest{Hostname: "rpi-01", Type: "ENTRADA_HORNO", RenameURL: &raw}
		if err := req.Validate(); err != nil {
			t.Fatalf("expected valid https rename_url, got %v", err)
		}
		if req.RenameURL == nil || *req.RenameURL != raw {
			t.Fatalf("expected rename_url preserved, got %v", req.RenameURL)
		}
	})

	t.Run("vacío o en blanco se trata como no configurado", func(t *testing.T) {
		for _, raw := range []string{"", "   "} {
			v := raw
			req := CreateRequest{Hostname: "rpi-01", Type: "ENTRADA_HORNO", RenameURL: &v}
			if err := req.Validate(); err != nil {
				t.Fatalf("expected valid request for blank rename_url %q, got %v", raw, err)
			}
			if req.RenameURL != nil {
				t.Fatalf("expected blank rename_url normalized to nil, got %v", req.RenameURL)
			}
		}
	})

	t.Run("URL inválida es error", func(t *testing.T) {
		for name, raw := range map[string]string{
			"sin esquema": "192.168.0.10:9000",
			"sin host":    "http://",
			"basura":      "no es una url",
			"relativa":    "/solo/path",
		} {
			t.Run(name, func(t *testing.T) {
				v := raw
				req := CreateRequest{Hostname: "rpi-01", Type: "ENTRADA_HORNO", RenameURL: &v}
				if err := req.Validate(); err == nil {
					t.Fatal("expected validation error for invalid rename_url")
				}
			})
		}
	})
}
