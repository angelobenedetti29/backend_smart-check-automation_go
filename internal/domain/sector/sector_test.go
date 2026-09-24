package sector

import (
	"errors"
	"strings"
	"testing"
)

func TestCreateSectorRequestValidate_Valid(t *testing.T) {
	if err := (CreateSectorRequest{Nombre: "Horno 1"}).Validate(); err != nil {
		t.Fatalf("expected valid nombre, got: %v", err)
	}
}

func TestCreateSectorRequestValidate_NombreRequerido(t *testing.T) {
	err := (CreateSectorRequest{Nombre: "   "}).Validate()
	if err == nil {
		t.Fatal("expected validation error for empty nombre")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(ve.Fields) != 1 || ve.Fields[0] != "nombre: es requerido" {
		t.Fatalf("unexpected fields: %v", ve.Fields)
	}
}

func TestCreateSectorRequestValidate_NombreDemasiadoLargo(t *testing.T) {
	long := strings.Repeat("a", MaxNombreLength+1)
	err := (CreateSectorRequest{Nombre: long}).Validate()
	if err == nil || !strings.Contains(err.Error(), "no puede superar los 100 caracteres") {
		t.Fatalf("expected length error, got: %v", err)
	}
}

func TestCreateSectorRequestValidate_NombreEnElMaximo(t *testing.T) {
	long := strings.Repeat("a", MaxNombreLength)
	if err := (CreateSectorRequest{Nombre: long}).Validate(); err != nil {
		t.Fatalf("expected valid at exactly %d chars, got: %v", MaxNombreLength, err)
	}
}

func TestUpdateSectorRequestValidate_IgualQueCreate(t *testing.T) {
	if err := (UpdateSectorRequest{Nombre: "Horno 2"}).Validate(); err != nil {
		t.Fatalf("expected valid update nombre, got: %v", err)
	}
	if err := (UpdateSectorRequest{Nombre: ""}).Validate(); err == nil {
		t.Fatal("expected validation error for empty update nombre")
	}
}

func TestIsValidationError(t *testing.T) {
	if IsValidationError(errors.New("otro")) {
		t.Fatal("expected false for non-validation error")
	}
	if !IsValidationError(&ValidationError{Fields: []string{"x"}}) {
		t.Fatal("expected true for *ValidationError")
	}
}
