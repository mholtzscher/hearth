package automations

import "slices"

// ValidateBranchDecision validates freely constructed decision input against an
// immutable Run snapshot and its recorded match set. It structurally prepares
// the snapshot, bounding arbitrary and cyclic input before recursive lookup or
// evidence composition. It never checks live definitions, Devices, or State.
// SQLite owns parent eligibility, duplicate rejection, and contiguous positions.
// Execution's private prepared evaluator must not call this whole-snapshot boundary.
func ValidateBranchDecision(decision BranchDecision, snapshot Definition, matchedTriggerIDs []TriggerID) error {
	if err := validateBranchDecisionShape(decision); err != nil {
		return err
	}
	normalized, err := NormalizeDefinition(snapshot)
	if err != nil {
		return err
	}
	triggerIDs := make(map[TriggerID]bool)
	for _, trigger := range normalized.Triggers {
		triggerIDs[trigger.ID] = true
	}
	matched := make(map[TriggerID]bool)
	if len(matchedTriggerIDs) > automationTriggerMaxCount {
		return invalid("Run match set exceeds Trigger bound")
	}
	for _, id := range matchedTriggerIDs {
		if !triggerIDs[id] || matched[id] {
			return invalid("Run matches must be unique immutable snapshot Trigger references")
		}
		matched[id] = true
	}
	step := findBranchDecisionStep(normalized.Steps, decision.StepID)
	if step == nil || step.Kind != decision.Kind {
		return invalid("branch decision Step is not a matching branch in the immutable snapshot")
	}
	if step.Kind == StepKindIf {
		return validateIfDecision(decision, *step.If, matched)
	}
	return validateChooseDecision(decision, *step.Choose, matched)
}

func findBranchDecisionStep(steps []Step, id StepID) *Step {
	for index := range steps {
		step := &steps[index]
		if step.ID == id {
			return step
		}
		for _, sequence := range branchDecisionChildSequences(*step) {
			if found := findBranchDecisionStep(sequence, id); found != nil {
				return found
			}
		}
	}
	return nil
}

func branchDecisionChildSequences(step Step) [][]Step {
	switch step.Kind {
	case StepKindCommand:
		return nil
	case StepKindIf:
		return [][]Step{step.If.Then, step.If.Else}
	case StepKindChoose:
		sequences := make([][]Step, 0, len(step.Choose.Branches)+1)
		for _, branch := range step.Choose.Branches {
			sequences = append(sequences, branch.Steps)
		}
		return append(sequences, step.Choose.Default)
	default:
		return nil
	}
}

func validateIfDecision(decision BranchDecision, step IfStep, matched map[TriggerID]bool) error {
	if decision.Outcome == BranchError {
		if len(decision.Evaluations) != 0 {
			return invalid("If error cannot retain a completed root")
		}
		return nil
	}
	if (decision.Outcome == BranchElse && step.Else == nil) || (decision.Outcome == BranchNoMatch && step.Else != nil) {
		return invalid("If fallback outcome disagrees with immutable Else presence")
	}
	return validateBranchRootEvidence(step.Conditions, decision.Evaluations[0].Evaluation, matched)
}

func validateChooseDecision(decision BranchDecision, step ChooseStep, matched map[TriggerID]bool) error {
	count := len(decision.Evaluations)
	if count > len(step.Branches) {
		return invalid("Choose evaluated prefix exceeds immutable alternatives")
	}
	for index, item := range decision.Evaluations {
		branch := step.Branches[index]
		if *item.BranchID != branch.ID {
			return invalid("Choose evaluations are not an immutable definition-order prefix")
		}
		if err := validateBranchRootEvidence(branch.Conditions, item.Evaluation, matched); err != nil {
			return err
		}
	}
	switch decision.Outcome {
	case BranchDefault, BranchNoMatch:
		if count != len(step.Branches) || (decision.Outcome == BranchDefault) != (step.Default != nil) {
			return invalid("Choose fallback requires all false alternatives and matching Default presence")
		}
	case BranchError:
		if count == len(step.Branches) {
			return invalid("Choose error cannot follow a complete false prefix")
		}
	case BranchChosen, BranchUnknown:
		// Selection and unknown already require a terminating root result.
	case BranchThen, BranchElse:
		return invalid("If outcome cannot belong to Choose")
	}
	return nil
}

// Root composition uses recorded leaf results, not current State or another
// evaluation. Every leaf must occur exactly once in immutable pre-order.
func validateBranchRootEvidence(root Condition, evaluation ConditionEvaluation, matched map[TriggerID]bool) error {
	position := 0
	result, err := composeBranchEvidence(root, evaluation, &position, matched)
	if err != nil {
		return err
	}
	if position != len(evaluation.Nodes) || result != evaluation.Result {
		return invalid("branch root result or leaf count disagrees with immutable Condition composition")
	}
	return nil
}

func composeBranchEvidence(
	root Condition,
	evaluation ConditionEvaluation,
	position *int,
	matched map[TriggerID]bool,
) (ConditionResult, error) {
	switch root.Kind {
	case ConditionAll, ConditionAny:
		results := make([]ConditionResult, 0, len(root.Children))
		for _, child := range root.Children {
			result, err := composeBranchEvidence(child, evaluation, position, matched)
			if err != nil {
				return "", err
			}
			results = append(results, result)
		}
		return combineConditionGroup(root.Kind, results), nil
	case ConditionNot:
		result, err := composeBranchEvidence(*root.Child, evaluation, position, matched)
		return negateConditionResult(result), err
	case ConditionEntityState, ConditionTrigger:
		// Leaves consume retained evidence below.
	default:
		return "", invalid("unknown immutable Condition kind")
	}
	if *position >= len(evaluation.Nodes) {
		return "", invalid("branch evaluation omits immutable leaf evidence")
	}
	node := evaluation.Nodes[*position]
	*position++
	if node.ID != root.ID || (root.Kind == ConditionTrigger) != (node.Trigger != nil) {
		return "", invalid("branch leaf identity, order, or family differs from immutable predicate")
	}
	if root.Kind == ConditionTrigger {
		intersection := make([]TriggerID, 0)
		for _, id := range root.Trigger.TriggerIDs {
			if matched[id] {
				intersection = append(intersection, id)
			}
		}
		if !slices.Equal(intersection, node.Trigger.MatchedTriggerIDs) {
			return "", invalid("Trigger evidence differs from exact configured-order Run intersection")
		}
	} else if err := validateStateEvidencePredicate(*root.EntityState, node, evaluation); err != nil {
		return "", err
	}
	return node.Result, nil
}

func validateStateEvidencePredicate(
	predicate EntityStateCondition,
	node ConditionNodeResult,
	evaluation ConditionEvaluation,
) error {
	if node.ObservationID == nil {
		// Missing Entity or State was shape-checked.
		return nil
	}
	if predicate.Pointer == "" && node.SelectedValue == nil {
		return invalid("whole-State pointer cannot omit its selection")
	}
	ageReason, bounded := boundedEvidenceUnknownReason(predicate, *node.ObservedAt, evaluation.EvaluatedAt)
	if bounded {
		if node.UnknownReason == nil || *node.UnknownReason != ageReason {
			return invalid("State leaf evidence age disagrees with immutable predicate")
		}
		return nil
	}
	if node.UnknownReason != nil &&
		(*node.UnknownReason == ConditionUnknownEvidenceExpired || *node.UnknownReason == ConditionUnknownEvidenceInFuture) {
		return invalid("State leaf evidence age reason does not apply to immutable predicate")
	}
	if node.SelectedValue == nil {
		return nil
	}
	selected, err := decodeJSONValue(node.SelectedValue)
	if err != nil {
		return invalid("State leaf selection is malformed")
	}
	operand, err := decodeJSONValue(predicate.Operand)
	if err != nil {
		return invalid("immutable State predicate operand is malformed")
	}
	compatible := conditionComparisonCompatible(predicate.Operator, selected, operand)
	typeMismatch := node.UnknownReason != nil && *node.UnknownReason == ConditionUnknownTypeMismatch
	if compatible == typeMismatch {
		return invalid("State leaf type evidence disagrees with immutable predicate")
	}
	return nil
}
