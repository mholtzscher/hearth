package mcpapi

import (
	"bytes"
	"encoding/json"
)

// The two
// optional ID filters advertise nullable pointers, so guard them here: explicit
// null must not become omission and silently widen a query.
func decodeArguments(data []byte, target any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for field, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			switch field {
			case "device_id":
				return toolError(failureInvalidDeviceID, "device_id must not be null")
			case "entity_id":
				return toolError(failureInvalidEntityID, "entity_id must not be null")
			}
		}
	}
	return decodeJSON(data, target)
}
