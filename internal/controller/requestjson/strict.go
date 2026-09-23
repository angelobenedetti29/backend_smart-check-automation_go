package requestjson

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// errInvalidJSON es el error centinela interno de los helpers de decodificación
// estricta; los llamadores solo lo tratan como fallo booleano.
var errInvalidJSON = errors.New("JSON inválido")

// decodeStrict decodifica exactamente un documento JSON y rechaza miembros
// desconocidos o duplicados en cualquier nivel de anidamiento.
func decodeStrict(b []byte, dst interface{}) error {
	if err := rejectDuplicateJSON(b); err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		return errInvalidJSON
	}
	return nil
}

// rejectDuplicateJSON recorre los tokens del documento y falla si un mismo
// objeto repite una clave o si aparecen documentos adicionales.
func rejectDuplicateJSON(b []byte) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	var walk func() error
	walk = func() error {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		switch d := t.(type) {
		case json.Delim:
			if d == '{' {
				seen := map[string]bool{}
				for dec.More() {
					kt, err := dec.Token()
					if err != nil {
						return err
					}
					key, ok := kt.(string)
					if !ok || seen[key] {
						return errInvalidJSON
					}
					seen[key] = true
					if err := walk(); err != nil {
						return err
					}
				}
				_, err = dec.Token()
				return err
			}
			if d == '[' {
				for dec.More() {
					if err := walk(); err != nil {
						return err
					}
				}
				_, err = dec.Token()
				return err
			}
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		return errInvalidJSON
	}
	return nil
}
