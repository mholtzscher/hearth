package automations_test

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// projectRunTrace derives the legacy read assertions from the canonical trace.
func projectRunTrace(t testing.TB, run automations.Run) automations.RunEvidenceProjection {
	t.Helper()
	projection, err := automations.ProjectStepExecutions(run.Snapshot, run.Executions)
	if err != nil {
		t.Fatalf("project Run trace: %v", err)
	}
	return projection
}
