package mcpapi //nolint:testpackage // Pin the operation-local DTO schema hooks.

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestInputSchemasPreserveOptionalIDsAndPageArguments(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, field, description string
		schema                   map[string]any
	}{
		{"entities", "device_id", "canonical Hearth Device ID; omit to list every Device", generatedSuccessSchema[ListEntitiesInput](t)},
		{"commands", "entity_id", "canonical Hearth Entity ID; omit to list every Entity's Commands", generatedSuccessSchema[ListCommandsInput](t)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(test.schema["properties"])
			if err != nil {
				t.Fatal(err)
			}
			var properties map[string]map[string]any
			if decodeErr := json.Unmarshal(raw, &properties); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			wantID := map[string]any{
				"description": test.description,
				"anyOf":       []any{map[string]any{"type": "null"}, map[string]any{"type": "string"}},
			}
			if !reflect.DeepEqual(properties[test.field], wantID) {
				t.Fatalf("%s = %#v, want %#v", test.field, properties[test.field], wantID)
			}
			assertPageArgumentSchemas(t, properties)
		})
	}
}

func assertPageArgumentSchemas(t *testing.T, properties map[string]map[string]any) {
	t.Helper()
	limit := properties["limit"]
	if limit["type"] != "integer" || limit["description"] != "page size from 1 to 200; defaults to 50" {
		t.Fatalf("limit = %#v", limit)
	}
	for _, constraint := range []string{"minimum", "maximum", "default"} {
		if _, present := limit[constraint]; present {
			t.Fatalf("limit unexpectedly advertises %s", constraint)
		}
	}
	if properties["cursor"]["type"] != "string" ||
		properties["cursor"]["description"] != "opaque cursor from a previous page's next_cursor" {
		t.Fatalf("cursor = %#v", properties["cursor"])
	}
}
