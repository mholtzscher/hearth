package mcpapi

import (
	"bytes"
	"encoding/json"
)

// nullRejection reports the failure for an explicit null on a field that must
// not become omission. An ID filter or selector would silently widen a query,
// and an enabled flag or tag set has no meaningful null. The nullable
// name_override and area_id are not listed: their null is an edit.
func nullRejection(field string) (failureCode, string, bool) {
	switch field {
	case "device_id":
		return failureInvalidDeviceID, "device_id must not be null", true
	case "entity_id":
		return failureInvalidEntityID, "entity_id must not be null", true
	case "label_ids", "tag_ids":
		return failureInvalidLabelID, field + " must not be null", true
	case "types", "enabled":
		return failureInvalidRequest, field + " must not be null", true
	}
	return "", "", false
}

// decodeArguments decodes one tool's arguments after rejecting explicit nulls on
// the fields above.
func decodeArguments(data []byte, target any) error {
	_, err := decodePresentArguments(data, target)
	return err
}

// decodePresentArguments also reports which fields were present, so an optional
// nullable field can tell omission from an explicit null.
func decodePresentArguments(data []byte, target any) (map[string]bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	present := make(map[string]bool, len(fields))
	for field, value := range fields {
		present[field] = true
		if code, message, reject := nullRejection(field); reject &&
			bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, toolError(code, message)
		}
	}
	return present, decodeJSON(data, target)
}
