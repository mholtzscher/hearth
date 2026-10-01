package api

import (
	"bytes"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type PatchDeviceBody struct {
	NameOverride        *string `json:"name_override,omitempty" nullable:"true"`
	nameOverridePresent bool
}

func domainNameEdit(present bool, override *string) *devices.NameEdit {
	if !present {
		return nil
	}
	return &devices.NameEdit{Override: override}
}

// UnmarshalJSON tracks removal separately from omission and compensates for
// Huma skipping optional null properties during schema validation.
func (body *PatchEntityBody) UnmarshalJSON(data []byte) error {
	properties, err := metadataPatchProperties(data, true)
	if err != nil {
		return err
	}
	type decodedBody PatchEntityBody
	var decoded decodedBody
	if err = json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	_, decoded.nameOverridePresent = properties["name_override"]
	*body = PatchEntityBody(decoded)
	return nil
}

func (body *PatchDeviceBody) UnmarshalJSON(data []byte) error {
	properties, err := metadataPatchProperties(data, false)
	if err != nil {
		return err
	}
	type decodedBody PatchDeviceBody
	var decoded decodedBody
	if err = json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	_, decoded.nameOverridePresent = properties["name_override"]
	*body = PatchDeviceBody(decoded)
	return nil
}

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
