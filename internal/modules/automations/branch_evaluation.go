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
		EvaluatedAt: evaluatedAt.UTC(),
	}
	for _, id := range required {
		if _, covered := snapshot.Entries[id]; !covered {
			err := &ConditionSnapshotRequiredError{RequiredEntityIDs: required}
			return branchErrorDecision(step.Body, decision, nil, "branch_snapshot_incomplete", err)
		}
	}
	switch body := step.Body.(type) {
	case IfStep:
		evaluation, err := evaluateCoveredConditions(roots[0], snapshot, decision.EvaluatedAt, matchedTriggerIDs)
		if err != nil {
			return branchEvaluationError(step, decision, nil, err)
		}
		switch evaluation.Result {
		case ConditionUnknown:
			decision.Body = IfDecision{Result: IfUnknown{Evaluation: evaluation}}
		case ConditionTrue:
			decision.Body = IfDecision{Result: IfSelected{Arm: IfThen, Evaluation: evaluation}}
		case ConditionFalse:
			arm := IfNoMatch
			if body.Else != nil {
				arm = IfElse
			}
			decision.Body = IfDecision{Result: IfSelected{Arm: arm, Evaluation: evaluation}}
		default:
			return BranchDecision{}, invalid("prepared branch has an impossible root result")
		}
		return decision, nil
	case ChooseStep:
		return evaluateChooseBranch(body, decision, roots, matchedTriggerIDs, snapshot)
	case CommandStep, DelayStep:
		return BranchDecision{}, invalid("Step is not a branch")
	default:
		return BranchDecision{}, invalid("unsupported Step body")
	}
}

func evaluateChooseBranch(
	body ChooseStep,
	decision BranchDecision,
	roots []Condition,
	matchedTriggerIDs []TriggerID,
	snapshot devices.EntityStateSnapshot,
) (BranchDecision, error) {
	prefix := make([]ChooseEvaluation, 0, len(roots))
	for index, root := range roots {
		evaluation, evaluationErr := evaluateCoveredConditions(root, snapshot, decision.EvaluatedAt, matchedTriggerIDs)
		if evaluationErr != nil {
			return branchEvaluationError(Step{Body: body}, decision, prefix, evaluationErr)
		}
		id := body.Branches[index].ID
		prefix = append(prefix, ChooseEvaluation{BranchID: id, Evaluation: evaluation})
		switch evaluation.Result {
		case ConditionUnknown:
			decision.Body = ChooseDecision{Result: ChooseUnknown{Evaluations: prefix}}
			return decision, nil
		case ConditionTrue:
			decision.Body = ChooseDecision{Result: ChooseSelected{BranchID: id, Evaluations: prefix}}
			return decision, nil
		case ConditionFalse:
		default:
			return BranchDecision{}, invalid("prepared branch has an impossible root result")
		}
	}
	arm := ChooseNoMatch
	if body.Default != nil {
		arm = ChooseDefault
	}
	decision.Body = ChooseDecision{Result: ChooseFallback{Arm: arm, Evaluations: prefix}}
	return decision, nil
}

func branchEvaluationError(
	step Step,
	decision BranchDecision,
	prefix []ChooseEvaluation,
	err error,
) (BranchDecision, error) {
	code := branchFailureStateCorrupt
	if _, incomplete := errors.AsType[*ConditionSnapshotRequiredError](err); incomplete {
		code = "branch_snapshot_incomplete"
	} else if !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
		return BranchDecision{}, err
	}
	return branchErrorDecision(step.Body, decision, prefix, code, err)
}

func branchErrorDecision(
	body StepBody,
	decision BranchDecision,
	prefix []ChooseEvaluation,
	code string,
	cause error,
) (BranchDecision, error) {
	switch body.(type) {
	case IfStep:
		decision.Body = IfDecision{Result: IfError{FailureCode: code}}
	case ChooseStep:
		decision.Body = ChooseDecision{Result: ChooseError{FailureCode: code, Evaluations: prefix}}
	case CommandStep, DelayStep:
		return BranchDecision{}, invalid("nonbranch Step cannot have branch failure evidence")
	default:
		return BranchDecision{}, invalid("unsupported branch Step representation")
	}
	return decision, cause
}

func branchRoots(step Step) ([]Condition, error) {
	switch body := step.Body.(type) {
	case IfStep:
		return []Condition{body.Conditions}, nil
	case ChooseStep:
		if len(body.Branches) > 0 {
			roots := make([]Condition, 0, len(body.Branches))
			for _, branch := range body.Branches {
				roots = append(roots, branch.Conditions)
			}
			return roots, nil
		}
	case CommandStep, DelayStep:
	default:
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
	switch body := root.Body.(type) {
	case EntityStateCondition:
		if !seen[body.EntityID] {
			seen[body.EntityID] = true
			*ids = append(*ids, body.EntityID)
		}
	case TriggerCondition:
		if len(body.TriggerIDs) == 0 {
			return invalid("prepared Trigger Condition has no predicate")
		}
	case NotCondition:
		return collectPreparedBranchEntityIDs(body.Child, depth+1, seen, ids)
	case AllCondition:
		return collectPreparedGroupEntityIDs(body.Children, depth, seen, ids)
	case AnyCondition:
		return collectPreparedGroupEntityIDs(body.Children, depth, seen, ids)
	default:
		return invalid("prepared branch Condition has an impossible body")
	}
	return nil
}

func collectPreparedGroupEntityIDs(
	children []Condition,
	depth int,
	seen map[devices.EntityID]bool,
	ids *[]devices.EntityID,
) error {
	if len(children) == 0 {
		return invalid("prepared Condition group has no children")
	}
	for _, child := range children {
		if err := collectPreparedBranchEntityIDs(child, depth+1, seen, ids); err != nil {
			return err
		}
	}
	return nil
}
