package httpapi

import (
	"bytes"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func domainNameEdit(present bool, override *string) *devices.NameEdit {
	if !present {
		return nil
	}
	return &devices.NameEdit{Override: override}
}

// metadataPatchProperties validates writable Merge Patch fields, including
// nulls that Huma skips for optional properties.
func metadataPatchProperties(data []byte, allowEnabled bool) (map[string]json.RawMessage, error) {
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(data, &properties); err != nil {
		return nil, err
	}
	if properties == nil {
		return nil, &huma.ErrorDetail{Location: "body", Message: "patch must be an object"}
	}
	for key, value := range properties {
		switch {
		case key == "name_override":
		case key == "enabled" && allowEnabled:
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, &huma.ErrorDetail{Location: "body.enabled", Message: "enabled must not be null"}
			}
		default:
			return nil, &huma.ErrorDetail{Location: "body." + key, Message: "property is not editable"}
		}
	}
	return properties, nil
}
