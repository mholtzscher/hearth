package nats //nolint:testpackage // Wire mapping is private to the producer.

import (
	"encoding/json"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// TestObservationDeviceFactWirePreservesPreviousValuePresence protects the v1
// distinction between no predecessor and a predecessor whose value is JSON
// null. It fails if RawMessage null is omitted or absent evidence is encoded as
// an explicit null.
func TestObservationDeviceFactWirePreservesPreviousValuePresence(t *testing.T) {
	t.Parallel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		previousValue devices.Value
		wantPresent   bool
		wantValue     string
	}{
		{name: "absent", wantPresent: false},
		{name: "json null", previousValue: devices.Value(`null`), wantPresent: true, wantValue: `null`},
		{name: "object", previousValue: devices.Value(`{"on":false}`), wantPresent: true, wantValue: `{"on":false}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fact := testObservationFact(
				t, mustEntityID(t), observedAt, `true`, devices.DispositionApplied,
			)
			fact.PreviousValue = test.previousValue
			message, mapErr := mapObservationDeviceFact(validator, fact)
			if mapErr != nil {
				t.Fatal(mapErr)
			}
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			if decodeErr := json.Unmarshal(message.payload, &envelope); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			var data map[string]json.RawMessage
			if decodeErr := json.Unmarshal(envelope.Data, &data); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			previous, present := data["previous_value"]
			if present != test.wantPresent {
				t.Fatalf("previous_value presence = %t, want %t; data=%s", present, test.wantPresent, envelope.Data)
			}
			if present && string(previous) != test.wantValue {
				t.Fatalf("previous_value = %s, want exact JSON %s", previous, test.wantValue)
			}
		})
	}
}
