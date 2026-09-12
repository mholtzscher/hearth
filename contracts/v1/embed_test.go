package v1_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestEmbeddedSchemasCompile(t *testing.T) {
	t.Parallel()
	schemas := compileSchemas(t)
	if len(schemas) != 22 {
		t.Fatalf("compiled %d schemas, want 22", len(schemas))
	}
}

func TestSchemaFixtures(t *testing.T) {
	t.Parallel()
	schemas := compileSchemas(t)
	fixtures := map[string]string{
		contractsv1.RegistrationRequestSchemaID: `{
			"id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:registration-request:v1",
			"emitted_at":"2026-08-20T12:34:56.123Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"binding_key":"office-light","device":{"name":"Office Light","kind":"light"},"entities":[{"key":"power","external_id":"light.office","name":"Power","type":"hearth.power/v1","support":{"state":{},"operations":{"set":{}}}}]}
		}`,
		contractsv1.RegistrationResponseSchemaID: `{
			"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:registration-response:v1",
			"emitted_at":"2026-08-20T12:34:56Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"causation_id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"status":"accepted","binding":{"binding_key":"office-light","device_id":"dev_01890f47-7a6b-7c4d-8e9f-0123456789ab","entities":[{"key":"power","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","enabled":true}]}}
		}`,
		contractsv1.ObservationSchemaID: `{
			"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:observation:v1",
			"emitted_at":"2026-08-20T12:34:56Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","value":false,"adapter_received_at":"2026-08-20T12:34:56Z"}
		}`,
		contractsv1.EntityEventSchemaID: `{
			"id":"evt_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:entity-event:v1",
			"emitted_at":"2026-08-20T12:34:56.123456789Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","name":"single_press"}
		}`,
		contractsv1.CommandRequestSchemaID: `{
			"id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:command-request:v1",
			"emitted_at":"2026-08-20T12:34:56Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","operation":"set","parameters":{"value":true},"deadline":"2026-08-20T12:35:06Z"}
		}`,
		contractsv1.CommandResponseSchemaID: `{
			"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:command-response:v1",
			"emitted_at":"2026-08-20T12:34:57Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"command_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab","status":"accepted"}
		}`,
		contractsv1.EntityEnablementRequestSchemaID: `{
			"id":"ena_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:entity-enablement-request:v1",
			"emitted_at":"2026-08-20T12:34:56Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","enabled":false}
		}`,
		contractsv1.EntityEnablementResponseSchemaID: `{
			"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"schema":"urn:hearth:schema:entity-enablement-response:v1",
			"emitted_at":"2026-08-20T12:34:57Z",
			"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"causation_id":"ena_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			"data":{"status":"accepted","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","enabled":false}
		}`,
	}
	for schemaID, fixture := range fixtures {
		t.Run(schemaID, func(t *testing.T) {
			t.Parallel()
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

func TestRegistrationSchemaRequiresUnifiedSupport(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.RegistrationRequestSchemaID]
	for _, test := range []struct {
		name   string
		entity string
	}{
		{"old parallel fields", `"constraints":{},"operations":["set"]`},
		{"missing state", `"support":{"operations":{"set":{}}}`},
		{"invalid operation name", `"support":{"state":{},"operations":{"bad.name":{}}}`},
		{"non-object operation support", `"support":{"state":{},"operations":{"set":true}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload := `{
				"id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"schema":"urn:hearth:schema:registration-request:v1",
				"emitted_at":"2026-08-20T12:34:56Z",
				"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"data":{"binding_key":"office-light","device":{"name":"Office Light","kind":"light"},"entities":[{
					"key":"power","external_id":"light.office","name":"Power","type":"hearth.power/v1",` + test.entity + `
				}]}
			}`
			var value any
			if err := json.Unmarshal([]byte(payload), &value); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(value); err == nil {
				t.Fatal("registration unexpectedly accepted")
			}
		})
	}
}

//nolint:gocognit // The table-driven schema boundary matrix is intentionally kept together.
func TestRegistrationSchemaEntityBounds(t *testing.T) {
	t.Parallel()
	schemas := compileSchemas(t)
	for _, test := range []struct {
		name     string
		schemaID string
		payload  string
	}{
		{
			name: "request", schemaID: contractsv1.RegistrationRequestSchemaID,
			payload: `{"id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:registration-request:v1","emitted_at":"2026-08-20T12:34:56Z","correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"binding_key":"office-light","device":{"name":"Office Light","kind":"light"},"entities":[{"key":"power","external_id":"light.office","name":"Power","type":"hearth.power/v1","support":{"state":{},"operations":{"set":{}}}}]}}`,
		},
		{
			name: "response", schemaID: contractsv1.RegistrationResponseSchemaID,
			payload: `{"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab","schema":"urn:hearth:schema:registration-response:v1","emitted_at":"2026-08-20T12:34:56Z","correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab","causation_id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{"status":"accepted","binding":{"binding_key":"office-light","device_id":"dev_01890f47-7a6b-7c4d-8e9f-0123456789ab","entities":[{"key":"power","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","enabled":true}]}}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var payload map[string]any
			if err := json.Unmarshal([]byte(test.payload), &payload); err != nil {
				t.Fatal(err)
			}
			binding := payload["data"].(map[string]any)
			if test.schemaID == contractsv1.RegistrationResponseSchemaID {
				binding = binding["binding"].(map[string]any)
			}
			entity := binding["entities"].([]any)[0]
			for _, count := range []int{0, 1, 2, 64, 65} {
				entities := make([]any, count)
				for index := range entities {
					entities[index] = entity
				}
				binding["entities"] = entities
				err := schemas[test.schemaID].Validate(payload)
				valid := count >= 1 && count <= 64
				if valid && err != nil {
					t.Fatalf("%d entities rejected: %v", count, err)
				}
				if !valid && err == nil {
					t.Fatalf("%d entities accepted", count)
				}
			}
		})
	}
}

func TestRegistrationResponseRequiresCausationID(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.RegistrationResponseSchemaID]
	var value any
	if err := json.Unmarshal([]byte(`{
		"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:registration-response:v1",
		"emitted_at":"2026-08-20T12:34:56Z",
		"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"data":{"status":"rejected","error":{"code":"invalid_descriptor","message":"invalid descriptor"}}
	}`), &value); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err == nil {
		t.Fatal("registration response without causation_id unexpectedly accepted")
	}
}

func TestObservationSchemaLeavesValueSemanticsToCatalog(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.ObservationSchemaID]
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
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.CommandRequestSchemaID]
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

func TestEntityEnablementAndRegistrationBooleanShapes(t *testing.T) {
	t.Parallel()
	schemas := compileSchemas(t)
	var registration map[string]any
	if err := json.Unmarshal([]byte(`{
		"id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:registration-request:v1",
		"emitted_at":"2026-08-20T12:34:56Z",
		"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"data":{"binding_key":"office-light","device":{"name":"Office Light","kind":"light"},"entities":[{"key":"power","external_id":"light.office","name":"Power","type":"hearth.power/v1","support":{"state":{},"operations":{"set":{}}}}]}
	}`), &registration); err != nil {
		t.Fatal(err)
	}
	entity := registration["data"].(map[string]any)["entities"].([]any)[0].(map[string]any)
	for _, value := range []any{nil, true, false, "false"} {
		if value == nil {
			delete(entity, "initially_enabled")
		} else {
			entity["initially_enabled"] = value
		}
		err := schemas[contractsv1.RegistrationRequestSchemaID].Validate(registration)
		if value == "false" && err == nil {
			t.Fatal("non-boolean initially_enabled unexpectedly accepted")
		}
		if value != "false" && err != nil {
			t.Fatalf("initially_enabled %v rejected: %v", value, err)
		}
	}

	var response map[string]any
	if err := json.Unmarshal([]byte(`{
		"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:entity-enablement-response:v1",
		"emitted_at":"2026-08-20T12:34:57Z",
		"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"causation_id":"ena_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"data":{"status":"accepted","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","enabled":false}
	}`), &response); err != nil {
		t.Fatal(err)
	}
	data := response["data"].(map[string]any)
	if err := schemas[contractsv1.EntityEnablementResponseSchemaID].Validate(response); err != nil {
		t.Fatal(err)
	}
	delete(data, "enabled")
	if err := schemas[contractsv1.EntityEnablementResponseSchemaID].Validate(response); err == nil {
		t.Fatal("accepted response without enabled unexpectedly accepted")
	}
	data["status"] = "rejected"
	data["error"] = map[string]any{"code": "unknown_entity", "message": "entity not found"}
	delete(data, "entity_id")
	if err := schemas[contractsv1.EntityEnablementResponseSchemaID].Validate(response); err != nil {
		t.Fatalf("rejected response rejected: %v", err)
	}
}

// TestEntityEventSchemaEnforcesStrictEnvelope pins the durable report contract:
// one canonical evt_ identity, one slug name, UTC SDK time, and no causation,
// Command link, payload, or source time.
func TestEntityEventSchemaEnforcesStrictEnvelope(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.EntityEventSchemaID]
	for _, test := range []struct {
		name     string
		mutate   func(value map[string]any)
		accepted bool
	}{
		{name: "canonical", accepted: true},
		{name: "unknown envelope property", mutate: func(value map[string]any) { value["extra"] = true }},
		{
			name: "causation is refused",
			mutate: func(value map[string]any) {
				value["causation_id"] = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
			},
		},
		{name: "wrong id prefix", mutate: func(value map[string]any) {
			value["id"] = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		}},
		{name: "non-canonical id", mutate: func(value map[string]any) {
			value["id"] = "evt_01890F47-7A6B-7C4D-8E9F-0123456789AB"
		}},
		{name: "version four id", mutate: func(value map[string]any) {
			value["id"] = "evt_01890f47-7a6b-4c4d-8e9f-0123456789ab"
		}},
		{name: "wrong schema", mutate: func(value map[string]any) {
			value["schema"] = contractsv1.ObservationSchemaID
		}},
		{name: "non-UTC emitted time", mutate: func(value map[string]any) {
			value["emitted_at"] = "2026-08-20T12:34:56+02:00"
		}},
		{name: "missing emitted time", mutate: func(value map[string]any) { delete(value, "emitted_at") }},
		{name: "wrong entity prefix", mutate: func(value map[string]any) {
			entityEventData(value)["entity_id"] = "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab"
		}},
		{name: "missing name", mutate: func(value map[string]any) { delete(entityEventData(value), "name") }},
		{name: "uppercase name", mutate: func(value map[string]any) {
			entityEventData(value)["name"] = "Single_Press"
		}},
		{name: "spaced name", mutate: func(value map[string]any) {
			entityEventData(value)["name"] = "single press"
		}},
		{name: "dot name", mutate: func(value map[string]any) {
			entityEventData(value)["name"] = "single.press"
		}},
		{name: "overlong name", mutate: func(value map[string]any) {
			entityEventData(value)["name"] = strings.Repeat("a", 64)
		}},
		{name: "payload is refused", mutate: func(value map[string]any) {
			entityEventData(value)["payload"] = map[string]any{"battery": 90}
		}},
		{name: "source time is refused", mutate: func(value map[string]any) {
			entityEventData(value)["source_updated_at"] = "2026-08-20T12:34:56Z"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := decodeEntityEventFixture(t)
			if test.mutate != nil {
				test.mutate(value)
			}
			accepted := schema.Validate(value) == nil
			if accepted != test.accepted {
				t.Fatalf("accepted = %v, want %v", accepted, test.accepted)
			}
		})
	}
}

func entityEventData(value map[string]any) map[string]any {
	return value["data"].(map[string]any)
}

func decodeEntityEventFixture(t *testing.T) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(`{
		"id":"evt_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"schema":"urn:hearth:schema:entity-event:v1",
		"emitted_at":"2026-08-20T12:34:56.123456789Z",
		"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","name":"single_press"}
	}`), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// TestEntityEventIDIsNotACausationID pins the deliberate omission: this slice
// produces no message caused by an event, so an evt_ identity must not satisfy
// the shared causation union even though the definition is registered.
func TestEntityEventIDIsNotACausationID(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.RegistrationRequestSchemaID]
	for _, test := range []struct {
		name        string
		causationID string
		accepted    bool
	}{
		{
			name:        "registration causation",
			causationID: "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			accepted:    true,
		},
		{
			name:        "entity event causation",
			causationID: "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := map[string]any{
				"id":             "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"schema":         contractsv1.RegistrationRequestSchemaID,
				"emitted_at":     "2026-08-20T12:34:56Z",
				"correlation_id": "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"causation_id":   test.causationID,
				"data": map[string]any{
					"binding_key": "office-light",
					"device":      map[string]any{"name": "Office Light", "kind": "light"},
					"entities": []any{map[string]any{
						"key": "power", "external_id": "light.office", "name": "Power",
						"type": "hearth.power/v1",
						"support": map[string]any{
							"state": map[string]any{}, "operations": map[string]any{"set": map[string]any{}},
						},
					}},
				},
			}
			accepted := schema.Validate(request) == nil
			if accepted != test.accepted {
				t.Fatalf("accepted = %v, want %v", accepted, test.accepted)
			}
		})
	}
}

// TestRegistrationRequestCarriesOptionalEventSupport pins the outer wire shape
// that lets an event-source descriptor reach Core unchanged. Per-type schemas
// stay authoritative for membership, so only the shared structural bounds live
// here.
func TestRegistrationRequestCarriesOptionalEventSupport(t *testing.T) {
	t.Parallel()
	schema := compileSchemas(t)[contractsv1.RegistrationRequestSchemaID]
	for _, test := range []struct {
		name     string
		support  map[string]any
		accepted bool
	}{
		{
			name: "event source support",
			support: map[string]any{
				"state": map[string]any{}, "operations": map[string]any{},
				"events": map[string]any{"names": []any{"single_press", "double_press"}},
			},
			accepted: true,
		},
		{
			name: "single name",
			support: map[string]any{
				"state": map[string]any{}, "operations": map[string]any{},
				"events": map[string]any{"names": []any{"single_press"}},
			},
			accepted: true,
		},
		{
			name: "non-event type support",
			support: map[string]any{
				"state": map[string]any{}, "operations": map[string]any{"set": map[string]any{}},
			},
			accepted: true,
		},
		{
			name: "no names",
			support: map[string]any{
				"state": map[string]any{}, "operations": map[string]any{},
				"events": map[string]any{"names": []any{}},
			},
		},
		{
			name: "duplicate names",
			support: map[string]any{
				"state": map[string]any{}, "operations": map[string]any{},
				"events": map[string]any{"names": []any{"single_press", "single_press"}},
			},
		},
		{
			name: "unsafe name",
			support: map[string]any{
				"state": map[string]any{}, "operations": map[string]any{},
				"events": map[string]any{"names": []any{"single press"}},
			},
		},
		{
			name: "missing names",
			support: map[string]any{
				"state": map[string]any{}, "operations": map[string]any{},
				"events": map[string]any{},
			},
		},
		{
			name: "unknown events property",
			support: map[string]any{
				"state": map[string]any{}, "operations": map[string]any{},
				"events": map[string]any{"names": []any{"single_press"}, "extra": true},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := map[string]any{
				"id":             "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"schema":         contractsv1.RegistrationRequestSchemaID,
				"emitted_at":     "2026-08-20T12:34:56Z",
				"correlation_id": "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"data": map[string]any{
					"binding_key": "office-button",
					"device":      map[string]any{"name": "Office Button", "kind": "sensor"},
					"entities": []any{map[string]any{
						"key": "events", "external_id": "button.office", "name": "Office Button",
						"type": "hearth.enumevent/v1", "support": test.support,
					}},
				},
			}
			accepted := schema.Validate(request) == nil
			if accepted != test.accepted {
				t.Fatalf("accepted = %v, want %v", accepted, test.accepted)
			}
		})
	}
}

func compileSchemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	for schemaID, path := range contractsv1.SchemaFiles() {
		raw, err := contractsv1.FS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if addErr := compiler.AddResource(schemaID, document); addErr != nil {
			t.Fatalf("add %s: %v", path, addErr)
		}
	}
	compiled := make(map[string]*jsonschema.Schema, len(contractsv1.SchemaFiles()))
	for schemaID := range contractsv1.SchemaFiles() {
		schema, err := compiler.Compile(schemaID)
		if err != nil {
			t.Fatalf("compile %s: %v", schemaID, err)
		}
		compiled[schemaID] = schema
	}
	return compiled
}
