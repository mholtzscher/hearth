package automations

import "github.com/mholtzscher/hearth/internal/modules/adapters"

// ValidateStepExecutionTrace validates an arbitrary definition and its recorded selected path.
func ValidateStepExecutionTrace(
	snapshot Definition,
	matched []TriggerID,
	status RunStatus,
	trace []StepExecution,
) error {
	prepared, err := NormalizeDefinition(snapshot)
	if err != nil {
		return err
	}
	return ValidateStepExecutionTraceWithPreparedSnapshot(prepared, matched, status, trace)
}

// ValidateStepExecutionTraceWithPreparedSnapshot consumes an unchanged prepared snapshot.
// A non-successful Run may stop at any boundary, but later evidence cannot jump that boundary.
func ValidateStepExecutionTraceWithPreparedSnapshot(
	snapshot Definition,
	matched []TriggerID,
	status RunStatus,
	trace []StepExecution,
) error {
	switch status {
	case RunRunning, RunSucceeded, RunFailed, RunInterrupted:
	default:
		return invalid("invalid trace Run status")
	}
	if len(trace) > automationAllStepsMaxCount {
		return invalid("trace exceeds Step bound")
	}
	seen := make(map[StepID]bool, len(trace))
	for position, execution := range trace {
		if execution.Sequence != position || seen[execution.StepID] {
			return invalid("trace identities or order are inconsistent")
		}
		if _, err := ParseStepID(string(execution.StepID)); err != nil {
			return err
		}
		seen[execution.StepID] = true
		if _, err := EncodeStepExecutionDetails(execution.Details); err != nil {
			return err
		}
	}
	cursor := traceCursor{snapshot: snapshot, matches: matched, trace: trace, status: status}
	complete, err := cursor.sequence(snapshot.Steps)
	if err != nil {
		return err
	}
	if cursor.next != len(trace) {
		return invalid("trace contains unselected or unreachable evidence")
	}
	if status == RunSucceeded && !complete {
		return invalid("successful Run omits required selected-path evidence")
	}
	return nil
}

type traceCursor struct {
	snapshot Definition
	matches  []TriggerID
	trace    []StepExecution
	status   RunStatus
	next     int
	branches int
}

func (cursor *traceCursor) sequence(steps []Step) (bool, error) {
	for _, step := range steps {
		if cursor.next == len(cursor.trace) {
			return false, nil
		}
		execution := cursor.trace[cursor.next]
		if execution.StepID != step.ID || detailsKind(execution.Details) != StepBodyKind(step.Body) {
			return false, invalid("trace does not follow the selected Step sequence")
		}
		cursor.next++
		envelope, err := EncodeStepExecutionDetails(execution.Details)
		if err != nil {
			return false, err
		}
		if details, branch := execution.Details.(BranchExecutionDetails); branch {
			complete, childErr := cursor.branch(step, details, envelope.Phase)
			if childErr != nil || !complete {
				return false, childErr
			}
			continue
		}
		if envelope.Phase != executionSucceeded {
			return cursor.stopped(execution.Details)
		}
	}
	return true, nil
}

func (cursor *traceCursor) branch(step Step, details BranchExecutionDetails, phase string) (bool, error) {
	decision := details.Decision
	if decision.StepID != step.ID {
		return false, invalid("branch evidence names a different Step")
	}
	decision.Position = cursor.branches
	cursor.branches++
	if err := ValidateBranchDecisionWithPreparedSnapshot(decision, cursor.snapshot, cursor.matches); err != nil {
		return false, err
	}
	if phase == executionFailed {
		return cursor.stopped(details)
	}
	children, err := selectedBranchChildren(step, decision)
	if err != nil {
		return false, err
	}
	return cursor.sequence(children)
}

func (cursor *traceCursor) stopped(details StepExecutionDetails) (bool, error) {
	if cursor.next != len(cursor.trace) {
		return false, invalid("trace continues after non-successful execution")
	}
	envelope, err := EncodeStepExecutionDetails(details)
	if err != nil {
		return false, err
	}
	if envelope.Phase == executionRunning && cursor.status != RunRunning {
		return false, invalid("terminal Run contains running Step")
	}
	if outcome, failed := ParentFailure(details); failed && cursor.status != RunOutcomeStatus(outcome) {
		return false, invalid("Step failure disagrees with terminal Run")
	}
	return false, nil
}

// StepBodyKind derives a definition Step's label from its closed value variant.
func StepBodyKind(body StepBody) StepKind {
	switch body.(type) {
	case CommandStep:
		return StepKindCommand
	case DelayStep:
		return StepKindDelay
	case NotificationStep:
		return StepKindNotification
	case FunctionStep:
		return StepKindFunction
	case IfStep:
		return StepKindIf
	case ChooseStep:
		return StepKindChoose
	default:
		return ""
	}
}

func detailsKind(details StepExecutionDetails) StepKind {
	switch details := details.(type) {
	case CommandExecutionDetails:
		return StepKindCommand
	case DelayExecutionDetails:
		return StepKindDelay
	case NotificationExecutionDetails:
		return StepKindNotification
	case FunctionExecutionDetails:
		return StepKindFunction
	case BranchExecutionDetails:
		return details.Decision.Kind()
	default:
		return ""
	}
}

// ParentFailure derives parent failure from the typed result, never from reason-code spelling.
func ParentFailure(details StepExecutionDetails) (RunOutcome, bool) {
	switch details := details.(type) {
	case CommandExecutionDetails:
		switch outcome := details.Outcome.(type) {
		case FailedStep:
			return FailedRun{FailureCode: outcome.FailureCode}, true
		case InterruptedStep:
			return InterruptedRun{FailureCode: outcome.FailureCode}, true
		case SatisfiedStep, DispatchedStep:
			return nil, false
		default:
			return nil, false
		}
	case DelayExecutionDetails:
		if details.FailureCode != nil {
			return InterruptedRun{FailureCode: *details.FailureCode}, true
		}
	case NotificationExecutionDetails:
		if details.CompletedAt != nil && details.ReasonCode != nil {
			if details.Outcome == nil && interruptionCode(*details.ReasonCode) {
				return InterruptedRun{FailureCode: *details.ReasonCode}, true
			}
			return FailedRun{FailureCode: *details.ReasonCode}, true
		}
	case FunctionExecutionDetails:
		switch state := details.State.(type) {
		case FunctionFailed:
			return FailedRun{FailureCode: functionFailureCode(state.Evidence)}, true
		case FunctionInterrupted:
			return InterruptedRun{FailureCode: state.ReasonCode}, true
		case FunctionRunning, FunctionSucceeded:
			return nil, false
		default:
			return nil, false
		}
	case BranchExecutionDetails:
		if code := details.Decision.FailureCode(); code != nil {
			return FailedRun{FailureCode: *code}, true
		}
	default:
		return nil, false
	}
	return nil, false
}

func functionFailureCode(failure FunctionFailure) string {
	switch failure := failure.(type) {
	case FunctionAdmissionRejected:
		return failure.ReasonCode
	case FunctionInvocationFailed:
		switch evidence := failure.Outcome.(type) {
		case adapters.Failed:
			return evidence.ReasonCode
		case adapters.NotExecuted:
			return evidence.ReasonCode
		case adapters.Unknown:
			return evidence.ReasonCode
		case adapters.Succeeded:
			return FailureInternalError
		default:
			return FailureInternalError
		}
	default:
		return FailureInternalError
	}
}

func interruptionCode(code string) bool {
	return code == FailureCoreStopping || code == FailureCoreRestarted || code == FailureExecutorFault
}
