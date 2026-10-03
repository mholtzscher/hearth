package automations

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func (service *Service) executionOpen() bool {
	return service.AdmissionOpen() && service.devices != nil && service.devices.CommandAdmissionOpen()
}

func (service *Service) executeSequence(
	ctx context.Context,
	run Run,
	steps []Step,
	positions map[StepID]int,
	decisionPosition *int,
) bool {
	for _, step := range steps {
		switch step.Kind {
		case StepKindCommand:
			position, exists := positions[step.ID]
			if !exists {
				service.interruptBranchRun(ctx, run.ID, step.ID, FailureExecutorFault)
				return false
			}
			if !service.executeCommand(ctx, run, step, position) {
				return false
			}
		case StepKindIf, StepKindChoose:
			if !service.executeBranch(ctx, run, step, positions, decisionPosition) {
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
	positions map[StepID]int,
	decisionPosition *int,
) bool {
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, step.ID, FailureCoreStopping)
		return false
	}
	decision, err := service.evaluateReachedBranch(ctx, run, step)
	if err != nil && decision.Outcome != BranchError {
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
	if decision.Outcome == BranchError || decision.Outcome == BranchUnknown {
		return false // Repository committed the evidence and failed Run atomically.
	}
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, step.ID, FailureCoreStopping)
		return false
	}
	var children []Step
	switch decision.Outcome {
	case BranchThen:
		children = step.If.Then
	case BranchElse:
		children = step.If.Else
	case BranchDefault:
		children = step.Choose.Default
	case BranchChosen:
		for _, branch := range step.Choose.Branches {
			if branch.ID == *decision.SelectedBranchID {
				children = branch.Steps
				break
			}
		}
	case BranchNoMatch:
	case BranchUnknown, BranchError:
		return false
	}
	return service.executeSequence(ctx, run, children, positions, decisionPosition)
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
		return BranchDecision{
			StepID: step.ID, Kind: step.Kind, EvaluatedAt: readAt,
			Outcome: BranchError, FailureCode: &code,
			Evaluations: make([]BranchConditionEvaluation, 0),
		}, err
	}
	return evaluateBranch(step, run.MatchedTriggerIDs, snapshot, service.dependencies.Now().UTC())
}

// interruptBranchRun never attributes a control-flow fault to a command attempt.
func (service *Service) interruptBranchRun(ctx context.Context, runID RunID, stepID StepID, code string) {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.CompleteRun(
		writeContext,
		RunCompletion{RunID: runID, Status: RunInterrupted, FailureCode: &code},
	)
	cancel()
	if code == FailureExecutorFault || err != nil {
		service.latchRunExecutorFault(ctx, runID, stepID)
	}
}
