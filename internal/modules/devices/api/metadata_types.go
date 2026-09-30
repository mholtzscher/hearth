package api

import (
	"encoding/json"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

type NameEditBody struct {
	Override *string `json:"override" required:"true" nullable:"true"`
}

type PatchDeviceBody struct {
	NameEdit *NameEditBody `json:"name_edit,omitempty"`
}

// UnmarshalJSON compensates for Huma skipping optional null properties during
// schema validation. Omission is legal; explicit null is not.
func (body *PatchEntityBody) UnmarshalJSON(data []byte) error {
	type decodedBody PatchEntityBody
	var decoded decodedBody
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if err := rejectNullMetadataFields(data, "enabled", "name_edit"); err != nil {
		return err
	}
	*body = PatchEntityBody(decoded)
	return nil
}

func (body *PatchDeviceBody) UnmarshalJSON(data []byte) error {
	type decodedBody PatchDeviceBody
	var decoded decodedBody
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if err := rejectNullMetadataFields(data, "name_edit"); err != nil {
		return err
	}
	*body = PatchDeviceBody(decoded)
	return nil
}

func rejectNullMetadataFields(data []byte, fields ...string) error {
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(data, &properties); err != nil {
		return err
	}
	for key, value := range properties {
		if string(value) != "null" {
			continue
		}
		for _, field := range fields {
			if strings.EqualFold(key, field) {
				return &huma.ErrorDetail{Location: "body." + field, Message: field + " must not be null"}
			}
		}
	}
	return nil
}
