package sqlite

import (
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func runStateFromColumns(
	status automations.RunStatus,
	completed *time.Time,
	failure *string,
) (automations.RunState, error) {
	if status == automations.RunRunning {
		if completed != nil || failure != nil {
			return nil, lifecycleCorruption("running Run carries completion evidence")
		}
		return automations.RunningRun{}, nil
	}
	if completed == nil {
		return nil, lifecycleCorruption("completed Run lacks completion time")
	}
	var outcome automations.RunOutcome
	switch status {
	case automations.RunSucceeded:
		if failure != nil {
			return nil, lifecycleCorruption("successful Run carries failure")
		}
		outcome = automations.SucceededRun{}
	case automations.RunFailed:
		if failure == nil {
			return nil, lifecycleCorruption("failed Run lacks failure")
		}
		outcome = automations.FailedRun{FailureCode: *failure}
	case automations.RunInterrupted:
		if failure == nil {
			return nil, lifecycleCorruption("interrupted Run lacks failure")
		}
		outcome = automations.InterruptedRun{FailureCode: *failure}
	case automations.RunRunning:
		return nil, lifecycleCorruption("invalid running Run")
	default:
		return nil, lifecycleCorruption("unknown Run status")
	}
	if err := automations.ValidateRunCompletion(automations.RunCompletion{Outcome: outcome}); err != nil {
		return nil, err
	}
	return automations.CompletedRun{CompletedAt: *completed, Outcome: outcome}, nil
}

func lifecycleCorruption(message string) error {
	return fmt.Errorf("%w: stored lifecycle: %s", automations.ErrInvalidAutomation, message)
}
