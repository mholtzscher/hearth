package natswire_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

type testEntityEvent struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
}

type testRegistration struct {
	BindingKey string                     `json:"binding_key"`
	Device     testDeviceDescriptor       `json:"device"`
	Entities   []testEntityDescriptorWire `json:"entities"`
}

type testDeviceDescriptor struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type testEntityDescriptorWire struct {
	Key        string          `json:"key"`
	ExternalID string          `json:"external_id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Support    json.RawMessage `json:"support"`
}

const (
	testEntityEventID = "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testEnvelopeTime  = "2026-08-20T12:34:56.123456789Z"
	testEntityID      = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

// TestEntityEventEnvelopeRoundTrip pins the D1 wire contract: a strict
// evt_/cor_ envelope carrying only the Entity identity and the reported name
// survives validation, decode, and re-encode unchanged.
func TestEntityEventEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	envelope := natswire.Envelope[testEntityEvent]{
		ID:            testEntityEventID,
		Schema:        contractsv1.EntityEventSchemaID,
		EmittedAt:     testEnvelopeTime,
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Data:          testEntityEvent{EntityID: testEntityID, Name: "single_press"},
	}
	payload, err := natswire.Encode(validator, contractsv1.EntityEventSchemaID, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("causation_id")) {
		t.Fatalf("encoded entity event carries a causation ID: %s", payload)
	}
	decoded, err := natswire.Decode[testEntityEvent](validator, contractsv1.EntityEventSchemaID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != envelope.ID || decoded.Schema != envelope.Schema ||
		decoded.EmittedAt != envelope.EmittedAt || decoded.CorrelationID != envelope.CorrelationID ||
		decoded.Data != envelope.Data {
		t.Fatalf("decoded entity event = %#v, want %#v", decoded, envelope)
	}
	reencoded, err := natswire.Encode(validator, contractsv1.EntityEventSchemaID, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reencoded, payload) {
		t.Fatalf("entity event encoding is not stable: first %q, second %q", payload, reencoded)
	}
}

// TestEntityEventEnvelopeRejectsNonContractMessages pins the strict envelope:
// no causation, no Command link, no payload, no source time, one canonical ID
// shape, and a slug name.
func TestEntityEventEnvelopeRejectsNonContractMessages(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"id":"` + testEntityEventID + `",` +
		`"schema":"urn:hearth:schema:entity-event:v1",` +
		`"emitted_at":"` + testEnvelopeTime + `",` +
		`"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"data":{"entity_id":"` + testEntityID + `","name":"single_press"}}`
	if _, decodeErr := natswire.Decode[testEntityEvent](
		validator, contractsv1.EntityEventSchemaID, []byte(valid),
	); decodeErr != nil {
		t.Fatalf("canonical entity event rejected: %v", decodeErr)
	}
	payloads := map[string]string{
		"causation id": `{"id":"` + testEntityEventID + `",` +
			`"schema":"urn:hearth:schema:entity-event:v1",` +
			`"emitted_at":"` + testEnvelopeTime + `",` +
			`"correlation_id":"cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
			`"causation_id":"cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
			`"data":{"entity_id":"` + testEntityID + `","name":"single_press"}}`,
		"wrong id prefix": strings.Replace(valid, "evt_", "obs_", 1),
		"upper case id":   strings.Replace(valid, "evt_01890f47", "evt_01890F47", 1),
		"payload":         strings.Replace(valid, `"data":{`, `"data":{"payload":true,`, 1),
		"source time": strings.Replace(
			valid, `"data":{"entity_id"`, `"data":{"source_updated_at":"`+testEnvelopeTime+`","entity_id"`, 1,
		),
		"missing name": strings.Replace(valid, `,"name":"single_press"`, "", 1),
		"unsafe name":  strings.Replace(valid, `"single_press"`, `"single press"`, 1),
		"missing data": strings.Replace(valid, `,"data":{`, `,"unused":{`, 1),
		"other schema": strings.Replace(
			valid, "urn:hearth:schema:entity-event:v1", "urn:hearth:schema:observation:v1", 1,
		),
		"trailing value": valid + ` true`,
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, decodeErr := natswire.Decode[testEntityEvent](
				validator, contractsv1.EntityEventSchemaID, []byte(payload),
			); decodeErr == nil {
				t.Fatalf("entity event payload unexpectedly accepted: %s", payload)
			}
		})
	}
}

// TestRegistrationEnvelopePreservesEventSupport pins the D1 round trip that
// carries an event-source descriptor from an Adapter to Core without dropping
// its supported names.
func TestRegistrationEnvelopePreservesEventSupport(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	support := json.RawMessage(
		`{"state":{},"operations":{},"events":{"names":["single_press","double_press"]}}`,
	)
	envelope := natswire.Envelope[testRegistration]{
		ID:            "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Schema:        contractsv1.RegistrationRequestSchemaID,
		EmittedAt:     testEnvelopeTime,
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Data: testRegistration{
			BindingKey: "office-button",
			Device:     testDeviceDescriptor{Name: "Office Button", Kind: "sensor"},
			Entities: []testEntityDescriptorWire{{
				Key: "events", ExternalID: "button.office", Name: "Office Button",
				Type: "hearth.enumevent/v1", Support: support,
			}},
		},
	}
	payload, err := natswire.Encode(validator, contractsv1.RegistrationRequestSchemaID, envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := natswire.Decode[testRegistration](validator, contractsv1.RegistrationRequestSchemaID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Data.Entities) != 1 {
		t.Fatalf("decoded entities = %#v", decoded.Data.Entities)
	}
	preserved := decoded.Data.Entities[0].Support
	if !bytes.Equal(compactJSON(t, preserved), compactJSON(t, support)) {
		t.Fatalf("event support = %s, want %s", preserved, support)
	}
	var names struct {
		Events struct {
			Names []string `json:"names"`
		} `json:"events"`
	}
	if unmarshalErr := json.Unmarshal(preserved, &names); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if len(names.Events.Names) != 2 || names.Events.Names[0] != "single_press" ||
		names.Events.Names[1] != "double_press" {
		t.Fatalf("preserved Entity Event names = %#v", names.Events.Names)
	}
}

func compactJSON(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, raw); err != nil {
		t.Fatal(err)
	}
	return compacted.Bytes()
}
