package automations

import (
	"errors"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// evaluateBranch consumes an unchanged prepared Step and the roots and Entity IDs
// collected for its State read. Coverage is checked before any root is evaluated.
func evaluateBranch(
	step Step,
	roots []Condition,
	required []devices.EntityID,
	matchedTriggerIDs []TriggerID,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
) (BranchDecision, error) {
	decision := BranchDecision{
		StepID:      step.ID,
		Kind:        step.Kind,
		EvaluatedAt: evaluatedAt.UTC(),
		Evaluations: make([]BranchConditionEvaluation, 0),
	}
	for _, id := range required {
		if _, covered := snapshot.Entries[id]; !covered {
			return branchEvaluationError(decision, &ConditionSnapshotRequiredError{RequiredEntityIDs: required})
		}
	}
	for index, root := range roots {
		evaluation, evaluationErr := evaluateCoveredConditions(root, snapshot, decision.EvaluatedAt, matchedTriggerIDs)
		if evaluationErr != nil {
			return branchEvaluationError(decision, evaluationErr)
		}
		item := BranchConditionEvaluation{Evaluation: evaluation}
		if step.Kind == StepKindChoose {
			id := step.Choose.Branches[index].ID
			item.BranchID = &id
		}
		decision.Evaluations = append(decision.Evaluations, item)
		switch evaluation.Result {
		case ConditionUnknown:
			code := "branch_condition_unknown"
			decision.Outcome, decision.FailureCode = BranchUnknown, &code
			return decision, nil
		case ConditionTrue:
			decision.Outcome = BranchThen
			if step.Kind == StepKindChoose {
				decision.Outcome, decision.SelectedBranchID = BranchChosen, item.BranchID
			}
			return decision, nil
		case ConditionFalse:
		default:
			return BranchDecision{}, invalid("prepared branch has an impossible root result")
		}
	}
	decision.Outcome = BranchNoMatch
	if step.Kind == StepKindIf && step.If.Else != nil {
		decision.Outcome = BranchElse
	}
	if step.Kind == StepKindChoose && step.Choose.Default != nil {
		decision.Outcome = BranchDefault
	}
	return decision, nil
}

func branchEvaluationError(decision BranchDecision, err error) (BranchDecision, error) {
	code := branchFailureStateCorrupt
	if _, incomplete := errors.AsType[*ConditionSnapshotRequiredError](err); incomplete {
		code = "branch_snapshot_incomplete"
	} else if !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
		return BranchDecision{}, err
	}
	decision.Outcome, decision.FailureCode = BranchError, &code
	return decision, err
}

func branchRoots(step Step) ([]Condition, error) {
	switch step.Kind {
	case StepKindIf:
		if step.If != nil && step.Choose == nil {
			return []Condition{step.If.Conditions}, nil
		}
	case StepKindChoose:
		if step.Choose != nil && step.If == nil && len(step.Choose.Branches) > 0 {
			roots := make([]Condition, 0, len(step.Choose.Branches))
			for _, branch := range step.Choose.Branches {
				roots = append(roots, branch.Conditions)
			}
			return roots, nil
		}
	case StepKindCommand, StepKindDelay:
	}
	return nil, invalid("Step %q is not a prepared branch", step.ID)
}

// branchEntityIDs checks only the prepared shapes needed for safe traversal and
// evaluation. Definition preparation still owns IDs, operands, and references.
func branchEntityIDs(roots []Condition) ([]devices.EntityID, error) {
	ids := make([]devices.EntityID, 0)
	seen := make(map[devices.EntityID]bool)
	for _, root := range roots {
		if err := collectPreparedBranchEntityIDs(root, 1, seen, &ids); err != nil {
			return nil, err
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func collectPreparedBranchEntityIDs(
	root Condition,
	depth int,
	seen map[devices.EntityID]bool,
	ids *[]devices.EntityID,
) error {
	// This traversal must also terminate if a supposedly prepared Not cycles.
	if depth > automationConditionMaxDepth {
		return invalid("prepared branch Condition exceeds traversal depth")
	}
	switch root.Kind {
	case ConditionEntityState:
		if root.EntityState == nil {
			return invalid("prepared State Condition has no payload")
		}
		id := root.EntityState.EntityID
		if !seen[id] {
			seen[id] = true
			*ids = append(*ids, id)
		}
	case ConditionTrigger:
		if root.Trigger == nil || len(root.Trigger.TriggerIDs) == 0 {
			return invalid("prepared Trigger Condition has no predicate")
		}
	case ConditionNot:
		if root.Child == nil {
			return invalid("prepared Not Condition has no child")
		}
		return collectPreparedBranchEntityIDs(*root.Child, depth+1, seen, ids)
	case ConditionAll, ConditionAny:
		if len(root.Children) == 0 {
			return invalid("prepared Condition group has no children")
		}
		for _, child := range root.Children {
			if err := collectPreparedBranchEntityIDs(child, depth+1, seen, ids); err != nil {
				return err
			}
		}
	default:
		return invalid("prepared branch Condition has an impossible kind")
	}
	return nil
}
