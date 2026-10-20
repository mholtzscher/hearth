package nats //nolint:testpackage // Tests exercise package-private stream and consumer policy.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// testTailFacts builds count distinct pending Observation facts with strictly
// increasing observed and commit times, so stream order is unambiguous.
func testTailFacts(t *testing.T, count int) []devices.PendingDeviceFact {
	t.Helper()
	entityID := mustEntityID(t)
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	facts := make([]devices.PendingDeviceFact, 0, count)
	for index := range count {
		facts = append(facts, pendingFact(int64(index+1), testObservationFact(
			t,
			entityID,
			observedAt.Add(time.Duration(index)*time.Minute),
			`"fact-`+string(rune('a'+index))+`"`,
			devices.DispositionApplied,
		)))
	}
	return facts
}

func publishedDeviceFactID(t *testing.T, payload []byte) string {
	t.Helper()
	var envelope struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode published device fact: %v", err)
	}
	return envelope.ID
}
