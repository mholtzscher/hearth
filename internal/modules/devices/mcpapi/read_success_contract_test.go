package mcpapi //nolint:testpackage // Exercise production success schemas through registration.

import (
	"context"
	"encoding/json"
	"testing"

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
		{"entity summary", deriveResultSchema[EntitySummary](), summary, []string{
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"availability":{"status":"online"}}`,
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"availability":{"status":"online","reason_code":null},"state":null}`,
		}},
		{"entity state", deriveResultSchema[EntitySummary](), `{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"availability":{"status":"online"},"state":{"value":{"n":9007199254740993,"tiny":1e-10000,"huge":1e10000,"nullable":null},"observed_at":"t"}}`, []string{
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"availability":{"status":"online"},"state":{"observed_at":"t"}}`,
		}},
		{"entity page", entityPageSchema(), `{"items":[` + summary + `]}`, []string{`{"items":null}`, `{"items":[],"next_cursor":null}`, `{"items":[],"extra":true}`}},
		{"device", deviceDetailSchema(), `{"device_id":"d","name":"n","kind":"k","entities":[]}`, []string{
			`{"device_id":"d","name":"n","kind":"k","entities":null}`,
			`{"device_id":"d","name":"n","kind":"k","entities":[],"next_entity_cursor":null}`,
		}},
		{"entity detail", entityDetailSchema(), detail, []string{
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"adapter_id":"a","support":[],"availability":{"status":"online","source":"s","since":"t","evidence_at":"t"},"state":null}`,
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"adapter_id":"a","support":{},"availability":{"status":"online","source":"s","since":"t","evidence_at":"t","source_observed_at":null},"state":null}`,
			`{"entity_id":"e","device_id":"d","name":"n","type":"t","enabled":true,"adapter_id":"a","support":{},"availability":{"status":"online","source":"s","since":"t","evidence_at":"t"},"state":{"value":null,"observed_at":"t","observation_id":"o","adapter_received_at":"t","source_updated_at":null}}`,
		}},
		{"command", commandDetailSchema(), command, []string{
			`{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":[],"deadline_at":"t"}`,
			`{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":{},"deadline_at":"t","completed_at":null}`,
			`{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":{},"deadline_at":"t","failure_code":null}`,
			`{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":{},"deadline_at":"t","accepted_at":null}`,
			`{"command_id":"c","entity_id":"e","operation":"set","status":"succeeded","requested_at":"t","parameters":{},"deadline_at":"t","outcome_observation_id":null}`,
		}},
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
