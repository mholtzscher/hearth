package zigbee2mqtt

import (
	"encoding/json"
	"errors"
	"math/big"
)

// errExactIntegerNotNumber reports a JSON value that is not a JSON number.
// Callers wrap it with their property/domain prefix.
var errExactIntegerNotNumber = errors.New("value must be a JSON number")

// errExactIntegerNotInteger reports a fraction or out-of-int64 JSON number.
var errExactIntegerNotInteger = errors.New("value must be a finite integer")

// parseExactIntegerJSON decodes an exact integer JSON number fitting int64,
// accepting integral decimals and scientific notation without rounding.
// Type and integer errors are distinguished from malformed JSON so callers
// can retain their property-specific diagnostics.
func parseExactIntegerJSON(payload json.RawMessage) (int64, error) {
	var decoded any
	if err := decodeJSON(payload, &decoded); err != nil {
		return 0, err
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return 0, errExactIntegerNotNumber
	}
	exact, ok := new(big.Rat).SetString(number.String())
	if !ok || !exact.IsInt() || !exact.Num().IsInt64() {
		return 0, errExactIntegerNotInteger
	}
	return exact.Num().Int64(), nil
}
