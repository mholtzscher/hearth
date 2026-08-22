package powerv1

import (
	"encoding/json"
	"testing"
)

func TestCodecsMatchPowerV1Bindings(t *testing.T) {
	codecs, err := Compile()
	if err != nil {
		t.Fatal(err)
	}

	support, normalized, err := codecs.Support.Decode(json.RawMessage(`{"state":{},"operations":{"set":{}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if normalizedSupport, err := codecs.Support.Encode(support); err != nil || string(normalizedSupport) != string(normalized) {
		t.Fatalf("support round trip = %s, %v", normalizedSupport, err)
	}
	if _, normalizedState, err := codecs.State.Decode(json.RawMessage(` true `)); err != nil || string(normalizedState) != "true" {
		t.Fatalf("state = %s, %v", normalizedState, err)
	}
	if _, normalizedParameters, err := codecs.SetParameters.Decode(json.RawMessage(`{ "value": true }`)); err != nil || string(normalizedParameters) != `{"value":true}` {
		t.Fatalf("parameters = %s, %v", normalizedParameters, err)
	}
}

func TestSupportSchemaRejectsNonExactShapes(t *testing.T) {
	codecs, err := Compile()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		raw  string
	}{
		{"missing set", `{"state":{},"operations":{}}`},
		{"unknown operation", `{"state":{},"operations":{"set":{},"toggle":{}}}`},
		{"state support field", `{"state":{"writable":true},"operations":{"set":{}}}`},
		{"operation support field", `{"state":{},"operations":{"set":{"transition":true}}}`},
		{"outer field", `{"state":{},"operations":{"set":{}},"extra":{}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := codecs.Support.Decode(json.RawMessage(test.raw)); err == nil {
				t.Fatal("support unexpectedly accepted")
			}
		})
	}
}

func TestSchemaFilesExposeStableIDs(t *testing.T) {
	if len(SchemaFiles()) != 3 {
		t.Fatalf("schema count = %d", len(SchemaFiles()))
	}
	for schemaID, path := range SchemaFiles() {
		raw, err := FS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if schema.ID != schemaID {
			t.Fatalf("%s ID = %q, want %q", path, schema.ID, schemaID)
		}
	}
}
