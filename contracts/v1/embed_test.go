package v1

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestEmbeddedSchemasCompile(t *testing.T) {
	schemas := compileSchemas(t)
	if len(schemas) != 6 {
		t.Fatalf("compiled %d schemas, want 6", len(schemas))
	}
}

func TestSchemaFixtures(t *testing.T) {
	schemas := compileSchemas(t)
	fixtures := map[string]string{
		RegistrationRequestSchemaID: `{
			"id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:registration-request:v1",
			"emitted_at":"2026-08-20T12:34:56.123Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"binding_key":"office-light","device":{"name":"Office Light","kind":"light"},"entities":[{"key":"power","external_id":"light.office","name":"Power","type":"hearth.power/v1","constraints":{},"operations":["set"]}]}
		}`,
		RegistrationResponseSchemaID: `{
			"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:registration-response:v1",
			"emitted_at":"2026-08-20T12:34:56Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"causation_id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"status":"accepted","binding":{"binding_key":"office-light","device_id":"dev_01890f47-7a6b-7c4d-8e9f-0123456789ab","entities":[{"key":"power","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"}]}}
		}`,
		ObservationSchemaID: `{
			"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:observation:v1",
			"emitted_at":"2026-08-20T12:34:56Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","value":false,"adapter_received_at":"2026-08-20T12:34:56Z"}
		}`,
		CommandRequestSchemaID: `{
			"id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:command-request:v1",
			"emitted_at":"2026-08-20T12:34:56Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","operation":"set","parameters":{"value":true},"deadline":"2026-08-20T12:35:06Z"}
		}`,
		CommandResponseSchemaID: `{
			"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:command-response:v1",
			"emitted_at":"2026-08-20T12:34:57Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"command_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab","status":"accepted"}
		}`,
	}
	for schemaID, fixture := range fixtures {
		t.Run(schemaID, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(fixture), &value); err != nil {
				t.Fatal(err)
			}
			if err := schemas[schemaID].Validate(value); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestObservationSchemaLeavesValueSemanticsToCatalog(t *testing.T) {
	schema := compileSchemas(t)[ObservationSchemaID]
	var value any
	if err := json.Unmarshal([]byte(`{
		"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:observation:v1",
		"emitted_at":"2026-08-20T12:34:56Z",
		"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","value":{"future":"shape"},"adapter_received_at":"2026-08-20T12:34:56Z"}
	}`), &value); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("generic observation value rejected structurally: %v", err)
	}
}

func TestSchemaRejectsUnknownEnvelopeProperty(t *testing.T) {
	schema := compileSchemas(t)[CommandRequestSchemaID]
	var value any
	if err := json.Unmarshal([]byte(`{
		"id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:command-request:v1",
		"emitted_at":"2026-08-20T12:34:56Z",
		"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"unexpected":true,
		"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","operation":"set","parameters":{},"deadline":"2026-08-20T12:35:06Z"}
	}`), &value); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err == nil {
		t.Fatal("unknown envelope property unexpectedly accepted")
	}
}

func compileSchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	for schemaID, path := range SchemaFiles() {
		raw, err := FS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if err := compiler.AddResource(schemaID, document); err != nil {
			t.Fatalf("add %s: %v", path, err)
		}
	}
	compiled := make(map[string]*jsonschema.Schema, len(SchemaFiles()))
	for schemaID := range SchemaFiles() {
		schema, err := compiler.Compile(schemaID)
		if err != nil {
			t.Fatalf("compile %s: %v", schemaID, err)
		}
		compiled[schemaID] = schema
	}
	return compiled
}
