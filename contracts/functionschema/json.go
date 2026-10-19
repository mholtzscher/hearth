package functionschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	MaxValueBytes     = 64 * 1024
	MaxValueDepth     = 16
	MaxContainerItems = 1024
)

// NormalizeObject checks one bounded JSON object and returns an owned, canonical
// encoding. Number literals are preserved; duplicate keys are never overwritten.
func NormalizeObject(document json.RawMessage) (json.RawMessage, error) {
	value, err := decodeObject(document, MaxValueDepth)
	if err != nil {
		return nil, ErrInvalidValue
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxValueBytes {
		return nil, ErrInvalidValue
	}
	return encoded, nil
}

func decodeObject(document []byte, depth int) (map[string]any, error) {
	if !utf8.Valid(document) {
		return nil, ErrInvalidValue
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	value, err := decodeValue(decoder, depth)
	if err != nil {
		return nil, ErrInvalidValue
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidValue
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, ErrInvalidValue
	}
	return object, nil
}

func decodeValue(decoder *json.Decoder, remaining int) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, ErrInvalidValue
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	if remaining < 1 {
		return nil, ErrInvalidValue
	}
	switch delimiter {
	case '{':
		return decodeMembers(decoder, remaining-1)
	case '[':
		return decodeItems(decoder, remaining-1)
	default:
		return nil, ErrInvalidValue
	}
}

func decodeMembers(decoder *json.Decoder, remaining int) (map[string]any, error) {
	object := make(map[string]any)
	for decoder.More() {
		if len(object) >= MaxContainerItems {
			return nil, ErrInvalidValue
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrInvalidValue
		}
		key, ok := token.(string)
		if !ok {
			return nil, ErrInvalidValue
		}
		if _, duplicate := object[key]; duplicate {
			return nil, ErrInvalidValue
		}
		value, err := decodeValue(decoder, remaining)
		if err != nil {
			return nil, err
		}
		object[key] = value
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
		return nil, ErrInvalidValue
	}
	return object, nil
}

func decodeItems(decoder *json.Decoder, remaining int) ([]any, error) {
	items := make([]any, 0)
	for decoder.More() {
		if len(items) >= MaxContainerItems {
			return nil, ErrInvalidValue
		}
		value, err := decodeValue(decoder, remaining)
		if err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
		return nil, ErrInvalidValue
	}
	return items, nil
}
