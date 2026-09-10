package natswire_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

const fuzzEnvelopeMaxPayloadSize = 64 << 10

type fuzzObservation struct {
	EntityID            string          `json:"entity_id"`
	Value               json.RawMessage `json:"value"`
	AdapterReceivedAt   string          `json:"adapter_received_at"`
	SourceUpdatedAt     *string         `json:"source_updated_at,omitempty"`
	RefreshForCommandID *string         `json:"refresh_for_command_id,omitempty"`
}

// FuzzCodecObservationRoundTrip protects authoritative schema validation and typed envelope binding.
// Any payload accepted by Decode must encode and decode to the same typed envelope with stable bytes.
func FuzzCodecObservationRoundTrip(f *testing.F) {
	validator, err := contractsv1.Compile()
	if err != nil {
		f.Fatal(err)
	}

	const validEnvelopePrefix = `"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"schema":"urn:hearth:schema:observation:v1",` +
		`"emitted_at":"2026-08-20T12:34:56Z",` +
		`"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",`
	const validDataPrefix = `"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"adapter_received_at":"2026-08-20T12:34:56Z",`

	seeds := [][]byte{
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validDataPrefix + `"value":true}}`),
		[]byte(" \n\t{" + validEnvelopePrefix + `"data":{` + validDataPrefix +
			`"value": { "nested" : [1, true, null] }}} \r\n`),
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validDataPrefix + `"value":null}}`),
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validDataPrefix + `"value":1}}`),
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validDataPrefix + `"value":1.0}}`),
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validDataPrefix + `"value":1e0}}`),
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validDataPrefix +
			`"value":123456789012345678901234567890.125}}`),
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validDataPrefix + `"value":{"key":1,"key":2}}}`),
		[]byte(`{"id":"invalid",` + validEnvelopePrefix + `"data":{` + validDataPrefix +
			`"value":false,"value":[1,2,3]}}`),
		[]byte(`{` + validEnvelopePrefix +
			`"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
			`"data":{` + validDataPrefix + `"value":"linked",` +
			`"source_updated_at":"2026-08-20T12:34:55.123456789Z",` +
			`"refresh_for_command_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"}}`),

		[]byte(``),
		[]byte(`{`),
		[]byte(`null`),
		[]byte(`{}`),
		[]byte(`{` + validEnvelopePrefix + `"data":null}`),
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validDataPrefix +
			`"value":true}} false`),
		[]byte(`{` + validEnvelopePrefix + `"extra":true,"data":{` + validDataPrefix +
			`"value":true}}`),
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validDataPrefix +
			`"value":true,"extra":true}}`),
		[]byte(`{` + validEnvelopePrefix +
			`"data":{"entity_id":"wrong","adapter_received_at":"not-a-time","value":true}}`),
		[]byte(`{` + validEnvelopePrefix +
			`"schema":"urn:hearth:schema:command-request:v1","data":{` + validDataPrefix +
			`"value":true}}`),
		[]byte(`{` + validEnvelopePrefix +
			`"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
			`"data":{` + validDataPrefix + `"value":true}}`),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > fuzzEnvelopeMaxPayloadSize {
			return
		}

		decoded, decodeErr := natswire.Decode[fuzzObservation](
			validator,
			contractsv1.ObservationSchemaID,
			payload,
		)
		if decodeErr != nil {
			return
		}

		encoded, encodeErr := natswire.Encode(validator, contractsv1.ObservationSchemaID, decoded)
		if encodeErr != nil {
			t.Fatalf("Decode accepted %q as %#v but Encode rejected it: %v", payload, decoded, encodeErr)
		}
		redecoded, redecodeErr := natswire.Decode[fuzzObservation](
			validator,
			contractsv1.ObservationSchemaID,
			encoded,
		)
		if redecodeErr != nil {
			t.Fatalf("Decode accepted %q but rejected its encoded envelope %q: %v", payload, encoded, redecodeErr)
		}
		if !equivalentFuzzObservationEnvelopes(decoded, redecoded) {
			t.Fatalf("typed envelope changed after Encode and Decode: first %#v, second %#v", decoded, redecoded)
		}

		reencoded, reencodeErr := natswire.Encode(validator, contractsv1.ObservationSchemaID, redecoded)
		if reencodeErr != nil {
			t.Fatalf("redecoded envelope %#v could not be encoded: %v", redecoded, reencodeErr)
		}
		if !bytes.Equal(reencoded, encoded) {
			t.Fatalf("encoding is not stable: first %q, second %q", encoded, reencoded)
		}
	})
}

func equivalentFuzzObservationEnvelopes(
	left, right natswire.Envelope[fuzzObservation],
) bool {
	leftValue, leftValueOK := decodeFuzzJSONValue(left.Data.Value)
	rightValue, rightValueOK := decodeFuzzJSONValue(right.Data.Value)
	left.Data.Value = nil
	right.Data.Value = nil
	return leftValueOK && rightValueOK &&
		reflect.DeepEqual(left, right) && reflect.DeepEqual(leftValue, rightValue)
}

func decodeFuzzJSONValue(raw json.RawMessage) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	return value, true
}

type fuzzDeviceEvent struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
}

// FuzzCodecDeviceEventRoundTrip protects the durable report contract: any
// payload Decode accepts must encode and decode to the same typed envelope with
// stable bytes.
func FuzzCodecDeviceEventRoundTrip(f *testing.F) {
	validator, err := contractsv1.Compile()
	if err != nil {
		f.Fatal(err)
	}

	const validEnvelopePrefix = `"id":"evt_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"schema":"urn:hearth:schema:device-event:v1",` +
		`"emitted_at":"2026-08-20T12:34:56Z",` +
		`"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",`
	const validData = `"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","name":"single_press"`

	seeds := [][]byte{
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validData + `}}`),
		[]byte(" \n\t{" + validEnvelopePrefix + `"data":{` + validData + `}} \r\n`),
		[]byte(`{` + validEnvelopePrefix +
			`"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
			`"data":{` + validData + `}}`),
		[]byte(`{` + validEnvelopePrefix +
			`"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","name":"Single_Press"}}`),
		[]byte(`{` + validEnvelopePrefix +
			`"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"}}`),
		[]byte(`{"id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",` + validEnvelopePrefix +
			`"data":{` + validData + `}}`),
		[]byte(`{` + validEnvelopePrefix +
			`"data":{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","name":"single_press","payload":{}}}`),

		[]byte(``),
		[]byte(`{`),
		[]byte(`null`),
		[]byte(`{}`),
		[]byte(`{` + validEnvelopePrefix + `"data":null}`),
		[]byte(`{` + validEnvelopePrefix + `"data":{` + validData + `}} false`),
		[]byte(`{` + validEnvelopePrefix + `"extra":true,"data":{` + validData + `}}`),
		[]byte(`{` + validEnvelopePrefix +
			`"schema":"urn:hearth:schema:observation:v1","data":{` + validData + `}}`),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > fuzzEnvelopeMaxPayloadSize {
			return
		}

		decoded, decodeErr := natswire.Decode[fuzzDeviceEvent](
			validator,
			contractsv1.DeviceEventSchemaID,
			payload,
		)
		if decodeErr != nil {
			return
		}

		encoded, encodeErr := natswire.Encode(validator, contractsv1.DeviceEventSchemaID, decoded)
		if encodeErr != nil {
			t.Fatalf("Decode accepted %q as %#v but Encode rejected it: %v", payload, decoded, encodeErr)
		}
		redecoded, redecodeErr := natswire.Decode[fuzzDeviceEvent](
			validator,
			contractsv1.DeviceEventSchemaID,
			encoded,
		)
		if redecodeErr != nil {
			t.Fatalf("Decode accepted %q but rejected its encoded envelope %q: %v", payload, encoded, redecodeErr)
		}
		if decoded != redecoded {
			t.Fatalf("typed envelope changed after Encode and Decode: first %#v, second %#v", decoded, redecoded)
		}

		reencoded, reencodeErr := natswire.Encode(validator, contractsv1.DeviceEventSchemaID, redecoded)
		if reencodeErr != nil {
			t.Fatalf("redecoded envelope %#v could not be encoded: %v", redecoded, reencodeErr)
		}
		if !bytes.Equal(reencoded, encoded) {
			t.Fatalf("encoding is not stable: first %q, second %q", encoded, reencoded)
		}
	})
}
