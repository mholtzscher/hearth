package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

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
		case key == "name_override", key == "area_id":
		case key == "tag_ids":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, &huma.ErrorDetail{Location: "body.tag_ids", Message: "tag_ids must not be null"}
			}
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

// labelPatchEdits converts decoded Label fields to domain edits, leaving
// omitted fields nil.
func labelPatchEdits(
	areaPresent bool, areaID *string, tagsPresent bool, tagIDs []string,
) (*devices.AreaEdit, *devices.TagEdit, error) {
	var areaEdit *devices.AreaEdit
	if areaPresent {
		areaEdit = &devices.AreaEdit{}
		if areaID != nil {
			parsed, err := devices.ParseLabelID(*areaID)
			if err != nil {
				return nil, nil, huma.NewError(http.StatusBadRequest, "area_id must be a canonical Hearth Label ID")
			}
			areaEdit.AreaID = &parsed
		}
	}
	var tagEdit *devices.TagEdit
	if tagsPresent {
		tagEdit = &devices.TagEdit{TagIDs: make([]devices.LabelID, len(tagIDs))}
		for index, value := range tagIDs {
			parsed, err := devices.ParseLabelID(value)
			if err != nil {
				return nil, nil, huma.NewError(http.StatusBadRequest, "tag_ids must contain canonical Hearth Label IDs")
			}
			tagEdit.TagIDs[index] = parsed
		}
	}
	return areaEdit, tagEdit, nil
}
