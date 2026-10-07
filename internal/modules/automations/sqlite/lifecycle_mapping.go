package sqlite

import (
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
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

func stepStateFromColumns(
	row dbsqlc.AutomationRunStep,
	started, completed *time.Time,
) (automations.StepAttemptState, error) {
	reservation, err := reservationFromColumns(row)
	if err != nil {
		return nil, err
	}
	status := automations.StepStatus(row.Status)
	switch status {
	case automations.StepNotAttempted:
		if started != nil || completed != nil || reservation != nil || row.VerifiedCommandID.Valid ||
			row.FailureCode.Valid {
			return nil, lifecycleCorruption("unattempted Step carries execution evidence")
		}
		return automations.NotAttemptedStep{}, nil
	case automations.StepRunning:
		if started == nil || reservation == nil || completed != nil || row.VerifiedCommandID.Valid ||
			row.FailureCode.Valid {
			return nil, lifecycleCorruption("invalid running Step evidence")
		}
		return automations.RunningStep{StartedAt: *started, Reservation: *reservation}, nil
	case automations.StepSatisfied, automations.StepDispatched, automations.StepFailed, automations.StepInterrupted:
		if started == nil || completed == nil {
			return nil, lifecycleCorruption("invalid completed Step times")
		}
	default:
		return nil, lifecycleCorruption("unknown Step status")
	}
	outcome, err := stepOutcomeFromColumns(row, reservation)
	if err != nil {
		return nil, err
	}
	return automations.CompletedStep{
		StartedAt: *started, CompletedAt: *completed, Reservation: reservation, Outcome: outcome,
	}, nil
}

func reservationFromColumns(row dbsqlc.AutomationRunStep) (*automations.CommandReservation, error) {
	if row.ReservedCommandID.Valid != row.ReservedCorrelationID.Valid {
		return nil, lifecycleCorruption("partial Command reservation")
	}
	if !row.ReservedCommandID.Valid {
		return nil, nil //nolint:nilnil // Completed interruptions may have no reservation.
	}
	commandID, err := devices.ParseCommandID(row.ReservedCommandID.String)
	if err != nil {
		return nil, lifecycleCorruption("invalid reserved Command ID")
	}
	correlationID, err := devices.ParseCorrelationID(row.ReservedCorrelationID.String)
	if err != nil {
		return nil, lifecycleCorruption("invalid reserved correlation ID")
	}
	return &automations.CommandReservation{CommandID: commandID, CorrelationID: correlationID}, nil
}

func stepOutcomeFromColumns(
	row dbsqlc.AutomationRunStep,
	reservation *automations.CommandReservation,
) (automations.StepOutcome, error) {
	verified := commandIDPointer(row.VerifiedCommandID)
	if verified != nil && (reservation == nil || *verified != reservation.CommandID) {
		return nil, lifecycleCorruption("verified Command disagrees with reservation")
	}
	var outcome automations.StepOutcome
	switch automations.StepStatus(row.Status) {
	case automations.StepSatisfied:
		if verified == nil || row.FailureCode.Valid {
			return nil, lifecycleCorruption("invalid satisfied Step evidence")
		}
		outcome = automations.SatisfiedStep{VerifiedCommandID: *verified}
	case automations.StepDispatched:
		if verified == nil || row.FailureCode.Valid {
			return nil, lifecycleCorruption("invalid dispatched Step evidence")
		}
		outcome = automations.DispatchedStep{VerifiedCommandID: *verified}
	case automations.StepFailed:
		if reservation == nil {
			return nil, lifecycleCorruption("failed Step lacks reservation")
		}
		if !row.FailureCode.Valid {
			return nil, lifecycleCorruption("failed Step lacks failure")
		}
		outcome = automations.FailedStep{FailureCode: row.FailureCode.String, VerifiedCommandID: verified}
	case automations.StepInterrupted:
		if !row.FailureCode.Valid {
			return nil, lifecycleCorruption("interrupted Step lacks failure")
		}
		outcome = automations.InterruptedStep{FailureCode: row.FailureCode.String, VerifiedCommandID: verified}
	case automations.StepNotAttempted, automations.StepRunning:
		return nil, lifecycleCorruption("invalid terminal Step")
	default:
		return nil, lifecycleCorruption("unknown Step status")
	}
	if err := automations.ValidateStepCompletion(automations.StepCompletion{Outcome: outcome}); err != nil {
		return nil, err
	}
	return outcome, nil
}

func lifecycleCorruption(message string) error {
	return fmt.Errorf("%w: stored lifecycle: %s", automations.ErrInvalidAutomation, message)
}
