package automations_test

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func executionForStep(run automations.Run, stepID automations.StepID) (automations.StepExecution, bool) {
	for _, execution := range run.Executions {
		if execution.StepID == stepID {
			return execution, true
		}
	}
	return automations.StepExecution{}, false
}

func requireExecutionForStep(t *testing.T, run automations.Run, stepID automations.StepID) automations.StepExecution {
	t.Helper()
	execution, ok := executionForStep(run, stepID)
	if !ok {
		t.Fatalf("Run has no execution for reached Step %q: %#v", stepID, run.Executions)
	}
	return execution
}

func branchDecisions(run automations.Run) []automations.BranchDecision {
	var decisions []automations.BranchDecision
	for _, execution := range run.Executions {
		if details, ok := execution.Details.(automations.BranchExecutionDetails); ok {
			decisions = append(decisions, details.Decision)
		}
	}
	return decisions
}

//nolint:unparam // Callers name the Step identity explicitly to keep evidence assertions local.
func requireNotificationExecution(
	t *testing.T,
	run automations.Run,
	stepID automations.StepID,
) automations.NotificationExecutionDetails {
	t.Helper()
	execution := requireExecutionForStep(t, run, stepID)
	notification, ok := execution.Details.(automations.NotificationExecutionDetails)
	if !ok {
		t.Fatalf("execution %q is not a Notification: %#v", stepID, execution)
	}
	return notification
}
