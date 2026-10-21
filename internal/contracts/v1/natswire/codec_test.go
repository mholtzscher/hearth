package natswire //nolint:testpackage // Tests share package-private subject fixtures with routing tests.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

type testObservation struct {
	EntityID            string          `json:"entity_id"`
	Value               json.RawMessage `json:"value"`
	AdapterReceivedAt   string          `json:"adapter_received_at"`
	SourceUpdatedAt     *string         `json:"source_updated_at,omitempty"`
	RefreshForCommandID *string         `json:"refresh_for_command_id,omitempty"`
}

func TestCodecUsesAuthoritativeSchemas(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	envelope := Envelope[testObservation]{
		ID:            "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Schema:        contractsv1.ObservationSchemaID,
		EmittedAt:     "2026-08-20T12:34:56Z",
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Data: testObservation{
			EntityID:          testEntityID,
			Value:             json.RawMessage(`true`),
			AdapterReceivedAt: "2026-08-20T12:34:56Z",
		},
	}
	payload, err := Encode(validator, contractsv1.ObservationSchemaID, envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode[testObservation](validator, contractsv1.ObservationSchemaID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != envelope.ID || string(decoded.Data.Value) != "true" {
		t.Fatalf("decoded envelope = %#v", decoded)
	}

	envelope.Schema = contractsv1.CommandRequestSchemaID
	if _, encodeErr := Encode(validator, contractsv1.ObservationSchemaID, envelope); encodeErr == nil {
		t.Fatal("schema-invalid envelope unexpectedly encoded")
	}
	if _, decodeErr := Decode[testObservation](
		validator,
		contractsv1.ObservationSchemaID,
		[]byte(`{}`),
	); decodeErr == nil {
		t.Fatal("schema-invalid payload unexpectedly decoded")
	}
}

func TestObservationCodecPreservesEnvelopeAndRawValues(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	const prefix = `{"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"schema":"urn:hearth:schema:observation:v1","emitted_at":"2026-08-20T12:34:56Z",` +
		`"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"adapter_received_at":"2026-08-20T12:34:56Z",` +
		`"source_updated_at":"2026-08-20T12:34:55.123456789Z",` +
		`"refresh_for_command_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab","value":`
	command := "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	sourceTime := "2026-08-20T12:34:55.123456789Z"
	for _, value := range []string{
		`true`, `null`, `1`, `1.0`, `1e0`, `123456789012345678901234567890.125`, `{"nested":[1,true,null]}`,
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			want := Envelope[testObservation]{
				ID: "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab", Schema: "urn:hearth:schema:observation:v1",
				EmittedAt: "2026-08-20T12:34:56Z", CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
				CausationID: &command,
				Data: testObservation{
					EntityID:            "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
					Value:               json.RawMessage(value),
					AdapterReceivedAt:   "2026-08-20T12:34:56Z",
					SourceUpdatedAt:     &sourceTime,
					RefreshForCommandID: &command,
				},
			}
			decoded, decodeErr := Decode[testObservation](
				validator,
				contractsv1.ObservationSchemaID,
				[]byte(prefix+value+`}}`),
			)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if !reflect.DeepEqual(decoded, want) {
				t.Fatalf("decoded = %#v, want %#v", decoded, want)
			}
			encoded, encodeErr := Encode(validator, contractsv1.ObservationSchemaID, decoded)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			again, decodeErr := Decode[testObservation](validator, contractsv1.ObservationSchemaID, encoded)
			if decodeErr != nil || !reflect.DeepEqual(again, want) {
				t.Fatalf("second decode = %#v, %v, want %#v", again, decodeErr, want)
			}
			reencoded, encodeErr := Encode(validator, contractsv1.ObservationSchemaID, again)
			if encodeErr != nil || !bytes.Equal(reencoded, encoded) {
				t.Fatalf("encoding is not stable: %s, %v; want %s", reencoded, encodeErr, encoded)
			}
		})
	}
}

func TestObservationCodecRejectsMalformedEnvelopes(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	const valid = `{"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"schema":"urn:hearth:schema:observation:v1","emitted_at":"2026-08-20T12:34:56Z",` +
		`"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"adapter_received_at":"2026-08-20T12:34:56Z","value":true}}`
	if _, decodeErr := Decode[testObservation](
		validator,
		contractsv1.ObservationSchemaID,
		[]byte(valid),
	); decodeErr != nil {
		t.Fatalf("valid control: %v", decodeErr)
	}
	for name, payload := range map[string]string{
		"malformed JSON": `{`,
		"null data": `{"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
			`"schema":"urn:hearth:schema:observation:v1","emitted_at":"2026-08-20T12:34:56Z",` +
			`"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":null}`,
		"wrong entity identity":  strings.Replace(valid, "ent_", "dev_", 1),
		"wrong schema":           strings.Replace(valid, "schema:observation", "schema:command-request", 1),
		"non UTC time":           strings.Replace(valid, "12:34:56Z", "12:34:56+01:00", 1),
		"unknown envelope field": strings.Replace(valid, `"data":{`, `"extra":true,"data":{`, 1),
		"unknown data field":     strings.Replace(valid, `"value":true`, `"value":true,"extra":true`, 1),
		"missing value":          strings.Replace(valid, `,"value":true`, "", 1),
		"trailing input":         valid + ` false`,
		"causation without command link": strings.Replace(valid, `"data":{`,
			`"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab","data":{`, 1),
		"command link without causation": strings.Replace(valid, `"value":true`,
			`"value":true,"refresh_for_command_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, decodeErr := Decode[testObservation](
				validator,
				contractsv1.ObservationSchemaID,
				[]byte(payload),
			); decodeErr == nil {
				t.Fatalf("invalid envelope accepted: %s", payload)
			}
		})
	}
}
