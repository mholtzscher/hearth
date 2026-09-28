package automations

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

func decodeJSONValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing content after JSON value")
	}
	return value, nil
}

// DecodeStrictJSONObject decodes exactly one JSON object, rejecting unknown
// fields and trailing content. It is the module's shared strict-envelope
// decoder for persisted documents and transport bodies.
func DecodeStrictJSONObject(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing content after JSON object")
	}
	return nil
}
