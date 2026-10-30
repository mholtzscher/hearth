package mcpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// The SDK rejects null for non-nullable fields before typed decoding. The two
// optional ID filters advertise nullable pointers, so guard them here: explicit
// null must not become omission and silently widen a query.
func decodeArguments(data []byte, target any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for field, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			rejection := fmt.Errorf("%s must not be null", field)
			switch field {
			case "device_id":
				return decodeFailure(failureInvalidDeviceID, rejection)
			case "entity_id":
				return decodeFailure(failureInvalidEntityID, rejection)
			default:
				return rejection
			}
		}
	}
	return decodeJSON(data, target)
}
