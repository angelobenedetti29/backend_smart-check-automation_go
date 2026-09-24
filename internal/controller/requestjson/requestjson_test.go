package requestjson

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type sampleDTO struct {
	Nombre   string                 `json:"nombre"`
	SectorID *string                `json:"sectorId,omitempty"`
	Extra    map[string]interface{} `json:"extra,omitempty"`
}

func decodeSample(t *testing.T, body string, limit int64) (bool, int) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	rw := httptest.NewRecorder()
	var dst sampleDTO
	ok := Decode(rw, r, limit, &dst)
	code := rw.Code
	if ok {
		code = http.StatusOK
	}
	return ok, code
}

func TestDecodeStrictObjectAcceptsCanonicalPayloads(t *testing.T) {
	for name, body := range map[string]string{
		"full":      `{"nombre":"Pi 1","sectorId":"horno-1"}`,
		"optional":  `{"nombre":"Pi 1"}`,
		"reordered": `{"sectorId":"horno-1","nombre":"Pi 1"}`,
		"empty-obj": `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			ok, code := decodeSample(t, body, DefaultLimit)
			if !ok || code != http.StatusOK {
				t.Fatalf("ok=%v code=%d, want accepted", ok, code)
			}
		})
	}
}

func TestDecodeStrictObjectRejectsDuplicateMembersRecursively(t *testing.T) {
	for name, body := range map[string]string{
		"top-level": `{"nombre":"a","nombre":"b"}`,
		"nested":    `{"nombre":"a","extra":{"k":1,"k":2}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ok, code := decodeSample(t, body, DefaultLimit)
			if ok || code != http.StatusBadRequest {
				t.Fatalf("ok=%v code=%d, want 400", ok, code)
			}
		})
	}
}

func TestDecodeStrictObjectRejectsAliasesAndUnknownFields(t *testing.T) {
	for name, body := range map[string]string{
		"case-alias": `{"Nombre":"a"}`,
		"unknown":    `{"otro":"a"}`,
	} {
		t.Run(name, func(t *testing.T) {
			ok, code := decodeSample(t, body, DefaultLimit)
			if ok || code != http.StatusBadRequest {
				t.Fatalf("ok=%v code=%d, want 400", ok, code)
			}
		})
	}
}

func TestDecodeStrictObjectRejectsNonObjectAndTrailingDocuments(t *testing.T) {
	for name, body := range map[string]string{
		"null":     `null`,
		"array":    `[]`,
		"scalar":   `"x"`,
		"empty":    ``,
		"trailing": `{"nombre":"a"}{"nombre":"b"}`,
	} {
		t.Run(name, func(t *testing.T) {
			ok, code := decodeSample(t, body, DefaultLimit)
			if ok || code != http.StatusBadRequest {
				t.Fatalf("ok=%v code=%d, want 400", ok, code)
			}
		})
	}
}

func TestDecodeStrictObjectRejectsOversizedBody(t *testing.T) {
	ok, code := decodeSample(t, `{"nombre":"`+strings.Repeat("x", 100)+`"}`, 16)
	if ok || code != http.StatusBadRequest {
		t.Fatalf("ok=%v code=%d, want 400", ok, code)
	}
}
