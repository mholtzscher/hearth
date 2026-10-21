package mcpapi //nolint:testpackage // Verify the shared receipt mapping of every domain lifecycle variant.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestRunReceiptReportsReturnedLifecycle(t *testing.T) {
	t.Parallel()
	completed := time.Date(2026, 10, 21, 12, 0, 0, 123, time.UTC)
	for _, test := range []struct {
		name  string
		state automations.RunState
		want  string
	}{
		{"running", automations.RunningRun{}, `{"automation_id":"aut","run_id":"run","status":"running"}`},
		{"succeeded", automations.CompletedRun{CompletedAt: completed, Outcome: automations.SucceededRun{}}, `{"automation_id":"aut","run_id":"run","status":"succeeded","completed_at":"2026-10-21T12:00:00.000000123Z"}`},
		{"failed", automations.CompletedRun{CompletedAt: completed, Outcome: automations.FailedRun{FailureCode: "executor_fault"}}, `{"automation_id":"aut","run_id":"run","status":"failed","completed_at":"2026-10-21T12:00:00.000000123Z","failure_code":"executor_fault"}`},
		{"interrupted", automations.CompletedRun{CompletedAt: completed, Outcome: automations.InterruptedRun{FailureCode: "core_restarted"}}, `{"automation_id":"aut","run_id":"run","status":"interrupted","completed_at":"2026-10-21T12:00:00.000000123Z","failure_code":"core_restarted"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			receipt := runReceipt(automations.Run{ID: "run", AutomationID: "aut", State: test.state})
			raw, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != test.want {
				t.Fatalf("receipt = %s, want %s", raw, test.want)
			}
		})
	}
}
