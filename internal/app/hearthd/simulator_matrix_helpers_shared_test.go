//go:build integration || e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"encoding/json"
	"testing"
	"time"
)

type wireObservation struct {
	EntityID          string          `json:"entity_id"`
	Value             json.RawMessage `json:"value"`
	AdapterReceivedAt string          `json:"adapter_received_at"`
	SourceUpdatedAt   *string         `json:"source_updated_at,omitempty"`
	RefreshForCommand *string         `json:"refresh_for_command_id,omitempty"`
}

func waitForMatrixCondition(t *testing.T, timeout time.Duration, condition func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		matched, err := condition()
		if err != nil {
			t.Fatal(err)
		}
		if matched {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	matched, err := condition()
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("timed out waiting for condition")
	}
}
