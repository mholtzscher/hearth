package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// JSONObject is owned, exact object-valued JSON. Operation-specific validation
// remains with the Command or Function service consuming it.
type JSONObject json.RawMessage

func (value *JSONObject) UnmarshalJSON(raw []byte) error {
	if !json.RawMessage(raw).IsValid() {
		return invalidArguments("invalid_json")
	}
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return &json.UnmarshalTypeError{Value: "non-object", Type: reflect.TypeFor[JSONObject]()}
	}
	*value = bytes.Clone(raw)
	return nil
}

func (value JSONObject) MarshalJSON() ([]byte, error) {
	if !json.RawMessage(value).IsValid() || !bytes.HasPrefix(bytes.TrimSpace(value), []byte("{")) {
		return nil, fmt.Errorf("JSONObject must contain one JSON object")
	}
	return value, nil
}

// Schema advertises the same object constraint enforced during decoding.
func (JSONObject) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: schemaTypeObject}
}

// DecodeInteger parses an exact mathematical JSON integer in the int64 range.
// The consuming scalar decoder supplies its own bounds and modeled error.
func DecodeInteger(raw json.RawMessage) (int64, error) {
	if !raw.IsValid() {
		return 0, fmt.Errorf("invalid JSON integer")
	}
	raw = bytes.TrimSpace(raw)
	if raw[0] != '-' && (raw[0] < '0' || raw[0] > '9') {
		return 0, fmt.Errorf("expected a JSON number")
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, fmt.Errorf("expected a JSON number")
	}
	value, ok := new(big.Rat).SetString(number.String())
	if !ok || !value.IsInt() || !value.Num().IsInt64() {
		return 0, fmt.Errorf("expected an int64 JSON integer")
	}
	return value.Num().Int64(), nil
}
