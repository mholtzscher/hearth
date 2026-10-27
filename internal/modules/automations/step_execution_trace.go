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
	validator := traceValidator{
		snapshot:          snapshot,
		matchedTriggerIDs: matchedTriggerIDs,
		trace:             trace,
		runStatus:         runStatus,
		seen:              make(map[StepID]bool, len(trace)),
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
	seen              map[StepID]bool
}

func (validator *traceValidator) validateSequence(steps []Step) (bool, error) {
	for _, step := range steps {
		if validator.nextExecution == len(validator.trace) {
			return false, nil
		}
		details, phase, err := validator.validateReachedStep(step)
		if err != nil {
			return false, err
		}
		if branchDetails, branch := details.(BranchExecutionDetails); branch {
			complete, childErr := validator.validateBranch(step, branchDetails, phase)
			if childErr != nil || !complete {
				return false, childErr
			}
			continue
		}
		if phase != executionSucceeded {
			return validator.validateStop(details, phase)
		}
	}
	return true, nil
}

// validateReachedStep checks one reach and advances the selected-path cursor once.
func (validator *traceValidator) validateReachedStep(step Step) (StepExecutionDetails, string, error) {
	execution := validator.trace[validator.nextExecution]
	if execution.Sequence != validator.nextExecution || validator.seen[execution.StepID] {
		return nil, "", invalid("trace identities or order are inconsistent")
	}
	if _, err := ParseStepID(string(execution.StepID)); err != nil {
		return nil, "", err
	}
	if err := validateStepExecutionDetails(execution.Details); err != nil {
		return nil, "", err
	}
	if execution.StepID != step.ID || execution.Details.Kind() != StepBodyKind(step.Body) {
		return nil, "", invalid("trace does not follow the selected Step sequence")
	}
	validator.nextExecution++
	validator.seen[execution.StepID] = true
	return execution.Details, execution.Details.Phase(), nil
}

func (validator *traceValidator) validateBranch(step Step, details BranchExecutionDetails, phase string) (bool, error) {
	decision := details.Decision
	if decision.StepID != step.ID {
		return false, invalid("branch evidence names a different Step")
	}
	decision.Position = validator.branchPosition
	validator.branchPosition++
	if err := validateBranchDecisionSnapshot(
		decision,
		validator.snapshot,
		validator.matchedTriggerIDs,
	); err != nil {
		return false, err
	}
	if phase == executionFailed {
		return validator.validateStop(details, phase)
	}
	children, err := selectedBranchChildren(step, decision)
	if err != nil {
		return false, err
	}
	return validator.validateSequence(children)
}

func (validator *traceValidator) validateStop(details StepExecutionDetails, phase string) (bool, error) {
	if validator.nextExecution != len(validator.trace) {
		return false, invalid("trace continues after non-successful execution")
	}
	if phase == executionRunning && validator.runStatus != RunRunning {
		return false, invalid("terminal Run contains running Step")
	}
	if outcome, failed := details.RunFailure(); failed && validator.runStatus != RunOutcomeStatus(outcome) {
		return false, invalid("Step failure disagrees with terminal Run")
	}
	return false, nil
}
