package mcpapi //nolint:testpackage // Exercise production success schemas through registration.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestReadSuccessContractsPreserveNullabilityAndClosedRecords(t *testing.T) {
	t.Parallel()
	const summary = `{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"availability":{"status":"online"},"state":null}`
	const detail = `{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"adapter_id":"a","support":{},"availability":{"status":"online","source":"s","since":"t","evidence_at":"t"},"state":null}`
	const command = `{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":{},"deadline_at":"t"}`
	for _, test := range []struct {
		name    string
		schema  any
		valid   string
		invalid []string
	}{
		{"entity summary", generatedSuccessSchema[EntitySummary](t), summary, []string{
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"availability":{"status":"online"}}`,
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"availability":{"status":"online","reason_code":null},"state":null}`,
		}},
		{"entity state", generatedSuccessSchema[EntitySummary](t), `{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"availability":{"status":"online"},"state":{"value":{"n":9007199254740993,"tiny":1e-10000,"huge":1e10000,"nullable":null},"observed_at":"t"}}`, []string{
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"availability":{"status":"online"},"state":{"observed_at":"t"}}`,
		}},
		{"entity page", generatedSuccessSchema[entityPage](t), `{"items":[` + summary + `]}`, []string{`{"items":null}`, `{"items":[],"next_cursor":null}`, `{"items":[],"extra":true}`}},
		{"device", generatedSuccessSchema[DeviceDetail](t), `{"device_id":"d","name":"n","kind":"k","entities":[]}`, []string{
			`{"device_id":"d","name":"n","kind":"k","entities":null}`,
			`{"device_id":"d","name":"n","kind":"k","entities":[],"next_entity_cursor":null}`,
		}},
		{"entity detail", generatedSuccessSchema[EntityDetail](t), detail, []string{
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"adapter_id":"a","support":[],"availability":{"status":"online","source":"s","since":"t","evidence_at":"t"},"state":null}`,
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"adapter_id":"a","support":{},"availability":{"status":"online","source":"s","since":"t","evidence_at":"t","source_observed_at":null},"state":null}`,
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"adapter_id":"a","support":{},"availability":{"status":"online","source":"s","since":"t","evidence_at":"t"},"state":{"value":null,"observed_at":"t","observation_id":"o","adapter_received_at":"t","source_updated_at":null}}`,
		}},
		{"command", generatedSuccessSchema[CommandDetail](t), command, []string{
			`{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":[],"deadline_at":"t"}`,
			`{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":{},"deadline_at":"t","completed_at":null}`,
			`{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":{},"deadline_at":"t","failure_code":null}`,
			`{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":{},"deadline_at":"t","accepted_at":null}`,
			`{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":{},"deadline_at":"t","outcome_observation_id":null}`,
		}},
		{"ordinary page nullable collection and cursor", generatedSuccessSchema[Page[DeviceSummary]](t), `{"items":null,"next_cursor":null}`, nil},
		{"command summary nullable optional strings", generatedSuccessSchema[CommandSummary](t), `{"command_id":"c","entity_id":"e","operation":"set","status":"satisfied","requested_at":"t","completed_at":null,"failure_code":null}`, nil},
		{"availability history nullable source time", generatedSuccessSchema[HealthTransition](t), `{"status":"available","source":"s","observed_at":"t","source_observed_at":null}`, nil},
		{"entity event nullable rejection", generatedSuccessSchema[EntityEvent](t), `{"event_id":"v","entity_id":"e","name":"n","disposition":"accepted","emitted_at":"t","received_at":"t","recorded_at":"t","rejection_code":null}`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for index, document := range append([]string{test.valid}, test.invalid...) {
				server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
				mcptools.Register(server, mcptools.Tool[struct{}, json.RawMessage]{
					Name:          "read",
					SuccessSchema: test.schema,
					Handler:       func(context.Context, struct{}) (json.RawMessage, error) { return json.RawMessage(document), nil },
				})
				result := callWire(t, server.HTTPHandler(), "read", `{}`)
				if index == 0 {
					assertWireJSON(t, result, test.valid)
				} else {
					assertInternalWireFailure(t, result)
				}
			}
		})
	}
}

// Serialize the schema independently so the existing wire fixtures exercise the
// DTO hooks even before the platform's Huma generator is integrated.
func generatedSuccessSchema[T any](t *testing.T) map[string]any {
	t.Helper()
	registry := huma.NewMapRegistry("#/$defs/", huma.DefaultSchemaNamer)
	schema := registry.Schema(reflect.TypeFor[T](), false, "")
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if decodeErr := json.Unmarshal(raw, &document); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	document["$defs"] = registry.Map()
	return document
}
