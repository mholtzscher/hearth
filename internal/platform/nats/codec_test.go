package nats

import (
	"encoding/json"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

func TestCodecUsesAuthoritativeSchemas(t *testing.T) {
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	envelope := Envelope[Observation]{
		ID:            "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Schema:        contractsv1.ObservationSchemaID,
		EmittedAt:     "2026-08-20T12:34:56Z",
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Data: Observation{
			EntityID:          testEntityID,
			Value:             json.RawMessage(`true`),
			AdapterReceivedAt: "2026-08-20T12:34:56Z",
		},
	}
	payload, err := Encode(validator, contractsv1.ObservationSchemaID, envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode[Observation](validator, contractsv1.ObservationSchemaID, payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != envelope.ID || string(decoded.Data.Value) != "true" {
		t.Fatalf("decoded envelope = %#v", decoded)
	}
}
