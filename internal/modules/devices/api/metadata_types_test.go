package api //nolint:testpackage // Tests exercise request decoding and exported HTTP schemas.

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

func TestMetadataPatchDecodingClearsPreviousPresence(t *testing.T) {
	t.Parallel()
	for _, body := range []any{&PatchDeviceBody{}, &PatchEntityBody{}} {
		for _, input := range []string{`{"name_override":"Kitchen"}`, `{"name_override":null}`, `{}`} {
			if err := json.Unmarshal([]byte(input), body); err != nil {
				t.Fatal(err)
			}
		}
		switch decoded := body.(type) {
		case *PatchDeviceBody:
			if decoded.nameOverridePresent || decoded.NameOverride != nil {
				t.Fatalf("omission retained a previous edit: %#v", decoded)
			}
		case *PatchEntityBody:
			if decoded.nameOverridePresent || decoded.NameOverride != nil {
				t.Fatalf("omission retained a previous edit: %#v", decoded)
			}
		}
	}
}

func TestMetadataPatchExportedSchemas(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	response := performRequest(router, "/openapi.json")
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI status = %d", response.Code)
	}
	var document struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
		Paths map[string]struct {
			Patch struct {
				RequestBody struct {
					Content map[string]json.RawMessage `json:"content"`
				} `json:"requestBody"`
			} `json:"patch"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document.Components.Schemas["NameEditBody"]; exists {
		t.Fatal("obsolete NameEditBody schema remains")
	}
	for _, test := range []struct{ schema, path string }{
		{"PatchDeviceBody", "/v1/devices/{device_id}"},
		{"PatchEntityBody", "/v1/entities/{entity_id}"},
	} {
		assertMetadataPatchSchema(t, document.Components.Schemas[test.schema], test.schema == "PatchEntityBody")
		content := document.Paths[test.path].Patch.RequestBody.Content
		if len(content) != 1 || content[mergePatchContentType] == nil {
			t.Fatalf("request media types = %#v", content)
		}
	}
}

func assertMetadataPatchSchema(t *testing.T, raw json.RawMessage, entity bool) {
	t.Helper()
	patch := decodeMetadataSchema(t, raw)
	if len(patch.Required) != 0 || string(patch.AdditionalProperties) != "false" {
		t.Fatalf("PATCH must allow omission and reject unknown fields: %s", raw)
	}
	override := decodeMetadataSchema(t, patch.Properties["name_override"])
	var types []string
	if err := json.Unmarshal(override.Type, &types); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(types, "string") || !slices.Contains(types, "null") {
		t.Fatalf("name_override must allow string and null: %s", override.Type)
	}
	wantProperties := 1
	if entity {
		wantProperties = 2
		if string(decodeMetadataSchema(t, patch.Properties["enabled"]).Type) != `"boolean"` {
			t.Fatal("enabled must be a non-null boolean")
		}
	}
	writableProperties := 0
	for _, property := range patch.Properties {
		if !decodeMetadataSchema(t, property).ReadOnly {
			writableProperties++
		}
	}
	if writableProperties != wantProperties {
		t.Fatalf("unexpected writable properties: %#v", patch.Properties)
	}
}

type metadataSchema struct {
	Type                 json.RawMessage            `json:"type"`
	Required             []string                   `json:"required"`
	Properties           map[string]json.RawMessage `json:"properties"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	ReadOnly             bool                       `json:"readOnly"`
}

func decodeMetadataSchema(t *testing.T, raw json.RawMessage) metadataSchema {
	t.Helper()
	var value metadataSchema
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
