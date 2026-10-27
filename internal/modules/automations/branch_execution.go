package automations

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func (service *Service) executionOpen() bool {
	return service.executionStop.Err() == nil && service.AdmissionOpen() && service.devices != nil &&
		service.devices.CommandAdmissionOpen()
}

//nolint:gocognit // Dispatches the closed Step family while preserving per-kind gates and Run stop behavior.
func (service *Service) executeSequence(
	ctx context.Context,
	run Run,
	steps []Step,
	values *runValues,
) bool {
	for _, step := range steps {
		switch body := step.Body.(type) {
		case CommandStep:
			if !service.executeCommand(ctx, run, step.ID, body, values) {
				return false
			}
		case IfStep, ChooseStep:
			if !service.executeBranch(ctx, run, step, values) {
				return false
			}
		case DelayStep:
			if !service.executeDelay(ctx, run, step.ID, body) {
				return false
			}
		case NotificationStep:
			if !service.executeNotification(ctx, run, step.ID, body, values) {
				return false
			}
		case FunctionStep:
			if !service.executeFunction(ctx, run, step.ID, body, values) {
				return false
			}
		case SetVariablesStep:
			if !service.executeAssignment(ctx, run, values, step.ID, body) {
				return false
			}
		case ReadStateStep:
			if !service.executeReadState(ctx, run, values, step.ID, body) {
				return false
			}
		default:
			service.interruptBranchRun(ctx, run.ID, step.ID, FailureExecutorFault)
			return false
		}
	}
	return true
}

func (service *Service) executeBranch(
	ctx context.Context,
	run Run,
	step Step,
	values *runValues,
) bool {
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, step.ID, service.executionStopReason())
		return false
	}
	values.branchPath = stepBindingPath(run.Snapshot.Steps, step.ID)
	decision, err := service.evaluateReachedBranch(ctx, run, step, values)
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, run.ID, step.ID, service.executionStopReason())
		return false
	}
	if err != nil && decision.Outcome() != BranchError {
		service.interruptBranchRun(ctx, run.ID, step.ID, FailureExecutorFault)
		return false
	}
	var failure *ValueFailure
	if _, ok := errors.AsType[*bindingError](err); ok {
		f := valueFailure(values.branchPath, err, nil)
		failure = &f
	}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err = service.repository.RecordStep(
		writeContext,
		StepExecutionRecord{
			RunID:    run.ID,
			StepID:   step.ID,
			Evidence: BranchDecided{Decision: decision, ValueFailure: failure},
		},
	)
	cancel()
	if err != nil {
		service.interruptBranchRun(ctx, run.ID, step.ID, FailureExecutorFault)
		return false
	}
	if decision.Outcome() == BranchError || decision.Outcome() == BranchUnknown {
		return false // Repository committed the evidence and failed Run atomically.
	}
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, step.ID, service.executionStopReason())
		return false
	}
	children, selectionErr := selectedBranchChildren(step, decision)
	if selectionErr != nil {
		service.interruptBranchRun(ctx, run.ID, step.ID, FailureExecutorFault)
		return false
	}
	return service.executeSequence(ctx, run, children, values)
}

// evaluateReachedBranch reads only a reached construct's immediate references.
// Invalid prepared shapes return no decision; operational errors retain evidence.
func (service *Service) evaluateReachedBranch(
	ctx context.Context,
	run Run,
	step Step,
	values *runValues,
) (BranchDecision, error) {
	values.evaluationContext, values.stopContext = ctx, service.executionStop
	roots, err := branchRoots(step)
	if err != nil {
		return BranchDecision{}, err
	}
	ids, err := requiredPreparedConditionEntityIDs(roots...)
	if err != nil {
		return BranchDecision{}, err
	}
	readAt := service.dependencies.Now().UTC()
	snapshot := devices.EntityStateSnapshot{}
	if len(ids) > 0 {
		readContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
		snapshot, err = service.devices.GetEntityStateSnapshot(readContext, ids)
		cancel()
	}
	if err != nil {
		code := "branch_state_read_failed"
		if errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
			code = branchFailureStateCorrupt
		}
		return branchErrorDecision(step.Body, BranchDecision{StepID: step.ID, EvaluatedAt: readAt}, nil, code, err)
	}
	return evaluateBranch(step, roots, ids, run.MatchedTriggerIDs, snapshot, service.dependencies.Now().UTC(), values)
}

// interruptBranchRun never attributes a control-flow fault to a command attempt.
func (service *Service) interruptBranchRun(ctx context.Context, runID RunID, stepID StepID, code string) {
	if code == FailureExecutorFault {
		service.interruptRunExecutorFault(ctx, runID, stepID)
		return
	}
	service.completeRun(ctx, runID, InterruptedRun{FailureCode: code})
}

func selectedBranchChildren(step Step, decision BranchDecision) ([]Step, error) {
	switch body := step.Body.(type) {
	case IfStep:
		switch decision.Outcome() {
		case BranchThen:
			return body.Then, nil
		case BranchElse:
			return body.Else, nil
		case BranchNoMatch:
			return nil, nil // An omitted fallback executes no children.
		case BranchChosen, BranchDefault, BranchUnknown, BranchError:
			return nil, invalid("invalid If selection")
		}
	case ChooseStep:
		switch decision.Outcome() {
		case BranchDefault:
			return body.Default, nil
		case BranchChosen:
			selected, ok := decision.Body.(ChooseDecision)
			if !ok {
				return nil, invalid("invalid Choose decision body")
			}
			result, ok := selected.Result.(ChooseSelected)
			if !ok {
				return nil, invalid("invalid Choose selected result")
			}
			for _, branch := range body.Branches {
				if branch.ID == result.BranchID {
					return branch.Steps, nil
				}
			}
		case BranchNoMatch:
			return nil, nil // An omitted fallback executes no children.
		case BranchThen, BranchElse, BranchUnknown, BranchError:
			return nil, invalid("invalid Choose selection")
		}
	case CommandStep, DelayStep, NotificationStep, FunctionStep, SetVariablesStep, ReadStateStep:
		return nil, invalid("Step is not a branch")
	default:
		return nil, invalid("unsupported Step body")
	}
	return nil, invalid("invalid branch selection")
}
