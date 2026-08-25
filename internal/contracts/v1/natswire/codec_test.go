package natswire

import (
	"encoding/json"
	"testing"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
)

type testObservation struct {
	EntityID          string          `json:"entity_id"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
}

func TestCodecUsesAuthoritativeSchemas(t *testing.T) {
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
	if _, err := Encode(validator, contractsv1.ObservationSchemaID, envelope); err == nil {
		t.Fatal("schema-invalid envelope unexpectedly encoded")
	}
	if _, err := Decode[testObservation](validator, contractsv1.ObservationSchemaID, []byte(`{}`)); err == nil {
		t.Fatal("schema-invalid payload unexpectedly decoded")
	}
}
