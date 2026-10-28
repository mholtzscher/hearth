package automations

// ValidateRunValueTraceWithPreparedSnapshot validates arbitrary initialization and
// execution details before checking their consistency with a prepared snapshot.
func ValidateRunValueTraceWithPreparedSnapshot(
	snapshot Definition,
	matches []TriggerID,
	state RunState,
	initialization *RunValueInitialization,
	trace []StepExecution,
	recorded bool,
) error {
	if len(trace) > automationAllStepsMaxCount {
		return invalid("trace exceeds Step bound")
	}
	if initialization != nil {
		if _, err := EncodeRunValueInitialization(*initialization); err != nil {
			return err
		}
	}
	for _, execution := range trace {
		if err := validateStepExecutionDetails(execution.Details); err != nil {
			return err
		}
	}
	return ValidateRunValueOwnershipWithPreparedSnapshot(snapshot, matches, state, initialization, trace, recorded)
}

// ValidateRunValueOwnershipWithPreparedSnapshot consumes unchanged initialization
// and details returned by the codecs or preparation. It checks their snapshot,
// selected path and parent outcome without repeating intrinsic validation.
//
//nolint:gocognit // Initialization and the selected path must agree with the parent outcome.
func ValidateRunValueOwnershipWithPreparedSnapshot(
	snapshot Definition,
	matches []TriggerID,
	state RunState,
	initialization *RunValueInitialization,
	trace []StepExecution,
	recorded bool,
) error {
	if len(trace) > automationAllStepsMaxCount {
		return invalid("trace exceeds Step bound")
	}
	status := RunStateStatus(state)
	switch run := state.(type) {
	case RunningRun:
	case CompletedRun:
		if !isValidExecutionTimestamp(run.CompletedAt) {
			return invalid("invalid Run completion time")
		}
		if err := ValidateRunCompletion(RunCompletion{Outcome: run.Outcome}); err != nil {
			return err
		}
	default:
		return invalid("unsupported Run state")
	}
	_, parentCode := RunStateCompletion(state)
	if !recorded && initialization != nil {
		return invalid("legacy Run cannot contain initialization")
	}
	if recorded && initialization == nil && (len(trace) > 0 || status == RunSucceeded) {
		return invalid("recorded Run requires initialization")
	}
	if initialization != nil {
		if err := validateInitializationOwnership(*initialization, snapshot); err != nil {
			return err
		}
		if failure, failed := initialization.Body.(InitializationFailed); failed {
			if len(trace) > 0 || status != RunFailed || parentCode == nil ||
				*parentCode != failure.Failure.FailureCode {
				return invalid("initialization failure disagrees with Run outcome")
			}
		}
	}
	validator := traceValidator{
		snapshot: snapshot, matchedTriggerIDs: matches, trace: trace, runStatus: status,
		recorded: recorded, parentCode: parentCode,
	}
	complete, err := validator.validateSequence(snapshot.Steps)
	if err != nil {
		return err
	}
	if validator.nextExecution != len(trace) {
		return invalid("trace contains unselected or unreachable evidence")
	}
	if status == RunSucceeded && !complete {
		return invalid("successful Run omits required selected-path evidence")
	}
	return nil
}

// traceValidator consumes intrinsically validated evidence. The selected-path
// traversal owns order, snapshot-relative values and parent/Step consistency.
type traceValidator struct {
	snapshot          Definition
	matchedTriggerIDs []TriggerID
	trace             []StepExecution
	runStatus         RunStatus
	nextExecution     int
	recorded          bool
	parentCode        *string
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
	if execution.Sequence != validator.nextExecution {
		return nil, "", invalid("trace identities or order are inconsistent")
	}
	if execution.StepID != step.ID || execution.Details.Kind() != StepBodyKind(step.Body) {
		return nil, "", invalid("trace does not follow the selected Step sequence")
	}
	if err := validateStepValueOwnership(execution, step, validator.recorded); err != nil {
		return nil, "", err
	}
	validator.nextExecution++
	return execution.Details, execution.Details.Phase(), nil
}

func (validator *traceValidator) validateBranch(step Step, details BranchExecutionDetails, phase string) (bool, error) {
	decision := details.Decision
	if decision.StepID != step.ID {
		return false, invalid("branch evidence names a different Step")
	}
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
	if outcome, failed := details.RunFailure(); failed {
		code := RunOutcomeFailureCode(outcome)
		if validator.runStatus != RunOutcomeStatus(outcome) || code == nil || validator.parentCode == nil ||
			*code != *validator.parentCode {
			return false, invalid("Step failure disagrees with terminal Run")
		}
	}
	return false, nil
}
