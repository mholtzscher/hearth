package functionschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxValueBytes     = 64 * 1024
	MaxValueDepth     = 16
	MaxContainerItems = 1024
	maxNumberExponent = 10000
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
	if number, ok := token.(json.Number); ok && !validNumberRange(number) {
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

// Check range without replacing the literal used for validation and encoding.
// Bounding explicit exponents first also protects the schema validator's exact
// arithmetic, including tiny numbers and zero with extreme positive exponents.
func validNumberRange(number json.Number) bool {
	literal := number.String()
	if index := strings.IndexAny(literal, "eE"); index >= 0 {
		exponent := strings.TrimLeft(literal[index+1:], "+-")
		magnitude := 0
		const decimalBase = 10
		for _, digit := range exponent {
			magnitude = magnitude*decimalBase + int(digit-'0')
			if magnitude > maxNumberExponent {
				return false
			}
		}
	}
	_, err := strconv.ParseFloat(literal, 64)
	return err == nil
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
