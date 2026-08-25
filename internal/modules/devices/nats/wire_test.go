package nats

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	sdkadapter "github.com/mholtzscher/hearth/sdk/adapter"
)

func TestCrossBinaryFixtures(t *testing.T) {
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		name     string
		schemaID string
		payload  string
		sdkData  any
		coreData any
	}{
		{
			name: "registration request", schemaID: contractsv1.RegistrationRequestSchemaID,
			payload: `{
				"id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"schema":"urn:hearth:schema:registration-request:v1",
				"emitted_at":"2026-08-20T12:34:56.123Z",
				"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"data":{"binding_key":"office-light","device":{"name":"Office Light","kind":"light"},"entities":[{"key":"power","external_id":"light.office","name":"Power","type":"hearth.power/v1","support":{"state":{},"operations":{"set":{}}}}]}
			}`,
			sdkData: &sdkadapter.Registration{}, coreData: &registration{},
		},
		{
			name: "registration response", schemaID: contractsv1.RegistrationResponseSchemaID,
			payload: `{
				"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"schema":"urn:hearth:schema:registration-response:v1",
				"emitted_at":"2026-08-20T12:34:56Z",
				"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"causation_id":"reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"data":{"status":"accepted","binding":{"binding_key":"office-light","device_id":"dev_01890f47-7a6b-7c4d-8e9f-0123456789ab","entities":[{"key":"power","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"}]}}
			}`,
			sdkData: &sdkadapter.RegistrationResponse{}, coreData: &registrationResponse{},
		},
		{
			name: "observation", schemaID: contractsv1.ObservationSchemaID,
			payload: `{
				"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"schema":"urn:hearth:schema:observation:v1",
				"emitted_at":"2026-08-20T12:34:56Z",
				"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","value":false,"adapter_received_at":"2026-08-20T12:34:56Z"}
			}`,
			sdkData: &sdkadapter.Observation{}, coreData: &observation{},
		},
		{
			name: "command request", schemaID: contractsv1.CommandRequestSchemaID,
			payload: `{
				"id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"schema":"urn:hearth:schema:command-request:v1",
				"emitted_at":"2026-08-20T12:34:56Z",
				"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","operation":"set","parameters":{"value":true},"deadline":"2026-08-20T12:35:06Z"}
			}`,
			sdkData: &sdkadapter.Command{}, coreData: &command{},
		},
		{
			name: "command response", schemaID: contractsv1.CommandResponseSchemaID,
			payload: `{
				"id":"rep_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"schema":"urn:hearth:schema:command-response:v1",
				"emitted_at":"2026-08-20T12:34:57Z",
				"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				"data":{"command_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab","status":"accepted"}
			}`,
			sdkData: &sdkadapter.CommandResponse{}, coreData: &commandResponse{},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			payload := []byte(fixture.payload)
			if err := validator.Validate(fixture.schemaID, payload); err != nil {
				t.Fatal(err)
			}
			lowerPayload := strings.ToLower(fixture.payload)
			for _, forbidden := range []string{"light.turn_on", "light.turn_off", "call_service", "home_assistant"} {
				if strings.Contains(lowerPayload, forbidden) {
					t.Fatalf("fixture contains Home Assistant vocabulary %q", forbidden)
				}
			}

			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(payload, &envelope); err != nil {
				t.Fatal(err)
			}
			assertDataRoundTrip(t, envelope.Data, fixture.sdkData)
			assertDataRoundTrip(t, envelope.Data, fixture.coreData)
		})
	}
}

func assertDataRoundTrip(t *testing.T, fixture json.RawMessage, target any) {
	t.Helper()
	if err := json.Unmarshal(fixture, target); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decodeJSON(t, fixture), decodeJSON(t, encoded)) {
		t.Fatalf("%T round-trip changed fixture: got %s, want %s", target, encoded, fixture)
	}
}

func decodeJSON(t *testing.T, raw []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
