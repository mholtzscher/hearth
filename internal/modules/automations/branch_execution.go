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

func (service *Service) executeSequence(
	ctx context.Context,
	run Run,
	steps []Step,
	positions map[StepID]int,
	decisionPosition *int,
	delayPosition *int,
) bool {
	for _, step := range steps {
		switch body := step.Body.(type) {
		case CommandStep:
			position, exists := positions[step.ID]
			if !exists {
				service.interruptBranchRun(ctx, run.ID, step.ID, FailureExecutorFault)
				return false
			}
			if !service.executeCommand(ctx, run, step.ID, body, position) {
				return false
			}
		case IfStep, ChooseStep:
			if !service.executeBranch(ctx, run, step, positions, decisionPosition, delayPosition) {
				return false
			}
		case DelayStep:
			if !service.executeDelay(ctx, run, step.ID, body, *delayPosition) {
				return false
			}
			*delayPosition++
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
	positions map[StepID]int,
	decisionPosition *int,
	delayPosition *int,
) bool {
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, step.ID, service.executionStopReason())
		return false
	}
	decision, err := service.evaluateReachedBranch(ctx, run, step)
	if err != nil && decision.Outcome() != BranchError {
		service.interruptBranchRun(ctx, run.ID, step.ID, FailureExecutorFault)
		return false
	}
	decision.Position = *decisionPosition
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err = service.repository.RecordBranchDecision(writeContext, run.ID, decision)
	cancel()
	if err != nil {
		service.interruptBranchRun(ctx, run.ID, step.ID, FailureExecutorFault)
		return false
	}
	*decisionPosition++
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
	return service.executeSequence(ctx, run, children, positions, decisionPosition, delayPosition)
}

// evaluateReachedBranch reads only a reached construct's immediate references.
// Invalid prepared shapes return no decision; operational errors retain evidence.
func (service *Service) evaluateReachedBranch(ctx context.Context, run Run, step Step) (BranchDecision, error) {
	roots, err := branchRoots(step)
	if err != nil {
		return BranchDecision{}, err
	}
	ids, err := branchEntityIDs(roots)
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
	return evaluateBranch(step, roots, ids, run.MatchedTriggerIDs, snapshot, service.dependencies.Now().UTC())
}

// interruptBranchRun never attributes a control-flow fault to a command attempt.
func (service *Service) interruptBranchRun(ctx context.Context, runID RunID, stepID StepID, code string) {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.CompleteRun(
		writeContext,
		RunCompletion{RunID: runID, Outcome: InterruptedRun{FailureCode: code}},
	)
	cancel()
	if code == FailureExecutorFault || err != nil {
		service.latchRunExecutorFault(ctx, runID, stepID)
	}
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
	case CommandStep, DelayStep:
		return nil, invalid("Step is not a branch")
	default:
		return nil, invalid("unsupported Step body")
	}
	return nil, invalid("invalid branch selection")
}
