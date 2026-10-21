package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Optional means omitted, not an explicit JSON null. Check presence before
// decoding pointers or scalar zero values, then retain exact opaque numbers.
func decodeArguments(data []byte, target any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for field, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s must not be null", field)
		}
	}
	return decodeJSON(data, target)
}
