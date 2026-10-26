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
		if _, err := EncodeStepExecutionBody(execution.Body); err != nil {
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
		if execution.StepID != step.ID || bodyKind(execution.Body) != StepBodyKind(step.Body) {
			return false, invalid("trace does not follow the selected Step sequence")
		}
		cursor.next++
		envelope, err := EncodeStepExecutionBody(execution.Body)
		if err != nil {
			return false, err
		}
		if body, branch := execution.Body.(BranchExecutionBody); branch {
			complete, childErr := cursor.branch(step, body, envelope.Phase)
			if childErr != nil || !complete {
				return false, childErr
			}
			continue
		}
		if envelope.Phase != executionSucceeded {
			return cursor.stopped(execution.Body)
		}
	}
	return true, nil
}

func (cursor *traceCursor) branch(step Step, body BranchExecutionBody, phase string) (bool, error) {
	decision := body.Decision
	if decision.StepID != step.ID {
		return false, invalid("branch evidence names a different Step")
	}
	decision.Position = cursor.branches
	cursor.branches++
	if err := ValidateBranchDecisionWithPreparedSnapshot(decision, cursor.snapshot, cursor.matches); err != nil {
		return false, err
	}
	if phase == executionFailed {
		return cursor.stopped(body)
	}
	children, err := selectedBranchChildren(step, decision)
	if err != nil {
		return false, err
	}
	return cursor.sequence(children)
}

func (cursor *traceCursor) stopped(body StepExecutionBody) (bool, error) {
	if cursor.next != len(cursor.trace) {
		return false, invalid("trace continues after non-successful execution")
	}
	envelope, err := EncodeStepExecutionBody(body)
	if err != nil {
		return false, err
	}
	if envelope.Phase == executionRunning && cursor.status != RunRunning {
		return false, invalid("terminal Run contains running Step")
	}
	if outcome, failed := ParentFailure(body); failed && cursor.status != RunOutcomeStatus(outcome) {
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

func bodyKind(body StepExecutionBody) StepKind {
	switch body := body.(type) {
	case CommandExecution:
		return StepKindCommand
	case DelayExecutionBody:
		return StepKindDelay
	case NotificationExecutionBody:
		return StepKindNotification
	case FunctionExecutionBody:
		return StepKindFunction
	case BranchExecutionBody:
		return body.Decision.Kind()
	default:
		return ""
	}
}

// ParentFailure derives parent failure from the typed result, never from reason-code spelling.
func ParentFailure(body StepExecutionBody) (RunOutcome, bool) {
	switch body := body.(type) {
	case CommandExecution:
		switch outcome := body.Outcome.(type) {
		case FailedStep:
			return FailedRun{FailureCode: outcome.FailureCode}, true
		case InterruptedStep:
			return InterruptedRun{FailureCode: outcome.FailureCode}, true
		case SatisfiedStep, DispatchedStep:
			return nil, false
		default:
			return nil, false
		}
	case DelayExecutionBody:
		if body.FailureCode != nil {
			return InterruptedRun{FailureCode: *body.FailureCode}, true
		}
	case NotificationExecutionBody:
		if body.CompletedAt != nil && body.ReasonCode != nil {
			if body.Outcome == nil && interruptionCode(*body.ReasonCode) {
				return InterruptedRun{FailureCode: *body.ReasonCode}, true
			}
			return FailedRun{FailureCode: *body.ReasonCode}, true
		}
	case FunctionExecutionBody:
		switch state := body.State.(type) {
		case FunctionFailed:
			return FailedRun{FailureCode: functionFailureCode(state.Evidence)}, true
		case FunctionInterrupted:
			return InterruptedRun{FailureCode: state.ReasonCode}, true
		case FunctionRunning, FunctionSucceeded:
			return nil, false
		default:
			return nil, false
		}
	case BranchExecutionBody:
		if code := body.Decision.FailureCode(); code != nil {
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
