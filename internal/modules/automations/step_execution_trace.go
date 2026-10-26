package automations

// ValidateStepExecutionTrace validates an arbitrary definition and its recorded selected path.
func ValidateStepExecutionTrace(
	snapshot Definition,
	matchedTriggerIDs []TriggerID,
	runStatus RunStatus,
	trace []StepExecution,
) error {
	prepared, err := NormalizeDefinition(snapshot)
	if err != nil {
		return err
	}
	return ValidateStepExecutionTraceWithPreparedSnapshot(prepared, matchedTriggerIDs, runStatus, trace)
}

// ValidateStepExecutionTraceWithPreparedSnapshot consumes an unchanged prepared snapshot.
// A non-successful Run may stop at any boundary, but later evidence cannot jump that boundary.
func ValidateStepExecutionTraceWithPreparedSnapshot(
	snapshot Definition,
	matchedTriggerIDs []TriggerID,
	runStatus RunStatus,
	trace []StepExecution,
) error {
	switch runStatus {
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
	validator := traceValidator{
		snapshot:          snapshot,
		matchedTriggerIDs: matchedTriggerIDs,
		trace:             trace,
		runStatus:         runStatus,
	}
	complete, err := validator.validateSequence(snapshot.Steps)
	if err != nil {
		return err
	}
	if validator.nextExecution != len(trace) {
		return invalid("trace contains unselected or unreachable evidence")
	}
	if runStatus == RunSucceeded && !complete {
		return invalid("successful Run omits required selected-path evidence")
	}
	return nil
}

type traceValidator struct {
	snapshot          Definition
	matchedTriggerIDs []TriggerID
	trace             []StepExecution
	runStatus         RunStatus
	nextExecution     int
	branchPosition    int
}

func (validator *traceValidator) validateSequence(steps []Step) (bool, error) {
	for _, step := range steps {
		if validator.nextExecution == len(validator.trace) {
			return false, nil
		}
		execution := validator.trace[validator.nextExecution]
		if execution.StepID != step.ID || executionDetailsKind(execution.Details) != StepBodyKind(step.Body) {
			return false, invalid("trace does not follow the selected Step sequence")
		}
		validator.nextExecution++
		envelope, err := EncodeStepExecutionDetails(execution.Details)
		if err != nil {
			return false, err
		}
		if details, branch := execution.Details.(BranchExecutionDetails); branch {
			complete, childErr := validator.validateBranch(step, details, envelope.Phase)
			if childErr != nil || !complete {
				return false, childErr
			}
			continue
		}
		if envelope.Phase != executionSucceeded {
			return validator.validateStop(execution.Details)
		}
	}
	return true, nil
}

func (validator *traceValidator) validateBranch(step Step, details BranchExecutionDetails, phase string) (bool, error) {
	decision := details.Decision
	if decision.StepID != step.ID {
		return false, invalid("branch evidence names a different Step")
	}
	decision.Position = validator.branchPosition
	validator.branchPosition++
	if err := ValidateBranchDecisionWithPreparedSnapshot(
		decision,
		validator.snapshot,
		validator.matchedTriggerIDs,
	); err != nil {
		return false, err
	}
	if phase == executionFailed {
		return validator.validateStop(details)
	}
	children, err := selectedBranchChildren(step, decision)
	if err != nil {
		return false, err
	}
	return validator.validateSequence(children)
}

func (validator *traceValidator) validateStop(details StepExecutionDetails) (bool, error) {
	if validator.nextExecution != len(validator.trace) {
		return false, invalid("trace continues after non-successful execution")
	}
	envelope, err := EncodeStepExecutionDetails(details)
	if err != nil {
		return false, err
	}
	if envelope.Phase == executionRunning && validator.runStatus != RunRunning {
		return false, invalid("terminal Run contains running Step")
	}
	if outcome, failed := StepExecutionRunFailure(details); failed && validator.runStatus != RunOutcomeStatus(outcome) {
		return false, invalid("Step failure disagrees with terminal Run")
	}
	return false, nil
}
