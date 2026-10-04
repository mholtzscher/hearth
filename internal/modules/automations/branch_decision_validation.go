package automations

import "slices"

// ValidateBranchDecisionWithPreparedSnapshot validates arbitrary decision evidence
// and Run matches against an unchanged snapshot returned by DecodeDefinition,
// NormalizeDefinition, or NormalizeAndEncodeDefinition. It reuses that snapshot's
// structural preparation and size check. Freely constructed or edited snapshots
// must pass through one of those definition boundaries first. It never checks live
// definitions, Devices, or State. SQLite owns parent eligibility, duplicate
// rejection, and contiguous positions.
func ValidateBranchDecisionWithPreparedSnapshot(
	decision BranchDecision,
	snapshot Definition,
	matchedTriggerIDs []TriggerID,
) error {
	if err := validateBranchDecisionShape(decision); err != nil {
		return err
	}
	return validateBranchDecisionSnapshot(decision, snapshot, matchedTriggerIDs)
}

// validateBranchDecisionSnapshot consumes shape-checked evidence and a prepared
// snapshot. The match set and snapshot-relative evidence are still unvalidated.
func validateBranchDecisionSnapshot(decision BranchDecision, snapshot Definition, matchedTriggerIDs []TriggerID) error {
	triggerIDs := make(map[TriggerID]bool)
	for _, trigger := range snapshot.Triggers {
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
	step := findBranchDecisionStep(snapshot.Steps, decision.StepID)
	if step == nil || step.Kind() != decision.Kind() {
		return invalid("branch decision Step is not a matching branch in the immutable snapshot")
	}
	switch body := step.Body.(type) {
	case IfStep:
		return validateIfDecision(decision, body, matched)
	case ChooseStep:
		return validateChooseDecision(decision, body, matched)
	case CommandStep:
		return invalid("branch decision references Command")
	default:
		return invalid("branch decision references unsupported Step")
	}
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
	switch body := step.Body.(type) {
	case CommandStep:
		return nil
	case IfStep:
		return [][]Step{body.Then, body.Else}
	case ChooseStep:
		sequences := make([][]Step, 0, len(body.Branches)+1)
		for _, branch := range body.Branches {
			sequences = append(sequences, branch.Steps)
		}
		return append(sequences, body.Default)
	default:
		panic("invalid normalized Step body")
	}
}

func validateIfDecision(decision BranchDecision, step IfStep, matched map[TriggerID]bool) error {
	body, ok := decision.Body.(IfDecision)
	if !ok {
		return invalid("unsupported If decision representation")
	}
	switch result := body.Result.(type) {
	case IfError:
		return nil
	case IfUnknown:
		return validateBranchRootEvidence(step.Conditions, result.Evaluation, matched)
	case IfSelected:
		if (result.Arm == IfElse && step.Else == nil) || (result.Arm == IfNoMatch && step.Else != nil) {
			return invalid("If fallback outcome disagrees with immutable Else presence")
		}
		return validateBranchRootEvidence(step.Conditions, result.Evaluation, matched)
	default:
		return invalid("unsupported If result representation")
	}
}

func validateChooseDecision(decision BranchDecision, step ChooseStep, matched map[TriggerID]bool) error {
	body, ok := decision.Body.(ChooseDecision)
	if !ok {
		return invalid("unsupported Choose decision representation")
	}
	var evaluations []ChooseEvaluation
	switch result := body.Result.(type) {
	case ChooseSelected:
		evaluations = result.Evaluations
	case ChooseFallback:
		evaluations = result.Evaluations
	case ChooseUnknown:
		evaluations = result.Evaluations
	case ChooseError:
		evaluations = result.Evaluations
	default:
		return invalid("unsupported Choose result representation")
	}
	count := len(evaluations)
	if count > len(step.Branches) {
		return invalid("Choose evaluated prefix exceeds immutable alternatives")
	}
	for index, item := range evaluations {
		branch := step.Branches[index]
		if item.BranchID != branch.ID {
			return invalid("Choose evaluations are not an immutable definition-order prefix")
		}
		if err := validateBranchRootEvidence(branch.Conditions, item.Evaluation, matched); err != nil {
			return err
		}
	}
	switch decision.Outcome() {
	case BranchDefault, BranchNoMatch:
		if count != len(step.Branches) || (decision.Outcome() == BranchDefault) != (step.Default != nil) {
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
	var predicate EntityStateCondition
	var triggerIDs []TriggerID
	isTrigger := false
	switch body := root.Body.(type) {
	case AllCondition:
		return composeBranchGroup(body.Children, ConditionAll, evaluation, position, matched)
	case AnyCondition:
		return composeBranchGroup(body.Children, ConditionAny, evaluation, position, matched)
	case NotCondition:
		result, err := composeBranchEvidence(body.Child, evaluation, position, matched)
		return negateConditionResult(result), err
	case EntityStateCondition:
		predicate = body
	case TriggerCondition:
		triggerIDs, isTrigger = body.TriggerIDs, true
	default:
		return "", invalid("unknown immutable Condition body")
	}
	if *position >= len(evaluation.Nodes) {
		return "", invalid("branch evaluation omits immutable leaf evidence")
	}
	node := evaluation.Nodes[*position]
	*position++
	triggerEvidence, hasTrigger := node.Evidence.(TriggerMatchEvidence)
	if node.ID != root.ID || isTrigger != hasTrigger {
		return "", invalid("branch leaf identity, order, or family differs from immutable predicate")
	}
	if isTrigger {
		intersection := make([]TriggerID, 0)
		for _, id := range triggerIDs {
			if matched[id] {
				intersection = append(intersection, id)
			}
		}
		if !slices.Equal(intersection, triggerEvidence.MatchedTriggerIDs) {
			return "", invalid("Trigger evidence differs from exact configured-order Run intersection")
		}
	} else if err := validateStateEvidencePredicate(predicate, node, evaluation); err != nil {
		return "", err
	}
	return node.Result(), nil
}

func validateStateEvidencePredicate(
	predicate EntityStateCondition,
	node ConditionNodeResult,
	evaluation ConditionEvaluation,
) error {
	var observation *ObservationEvidence
	var selection []byte
	var reason ConditionUnknownReason
	switch evidence := node.Evidence.(type) {
	case KnownStateEvidence:
		observation, selection = &evidence.Observation, evidence.SelectedValue
	case UnknownStateEvidence:
		observation, selection, reason = evidence.Observation, evidence.SelectedValue, evidence.Reason
	case TriggerMatchEvidence:
		return invalid("Trigger evidence cannot belong to State predicate")
	default:
		return invalid("unsupported State evidence representation")
	}
	if observation == nil {
		// Missing Entity or State was shape-checked.
		return nil
	}
	if predicate.Pointer == "" && selection == nil {
		return invalid("whole-State pointer cannot omit its selection")
	}
	ageReason, bounded := boundedEvidenceUnknownReason(predicate, observation.ObservedAt, evaluation.EvaluatedAt)
	if bounded {
		if reason != ageReason {
			return invalid("State leaf evidence age disagrees with immutable predicate")
		}
		return nil
	}
	if reason == ConditionUnknownEvidenceExpired || reason == ConditionUnknownEvidenceInFuture {
		return invalid("State leaf evidence age reason does not apply to immutable predicate")
	}
	if selection == nil {
		return nil
	}
	selected, err := decodeJSONValue(selection)
	if err != nil {
		return invalid("State leaf selection is malformed")
	}
	operand, err := decodeJSONValue(predicate.Operand)
	if err != nil {
		return invalid("immutable State predicate operand is malformed")
	}
	compatible := conditionComparisonCompatible(predicate.Operator, selected, operand)
	typeMismatch := reason == ConditionUnknownTypeMismatch
	if compatible == typeMismatch {
		return invalid("State leaf type evidence disagrees with immutable predicate")
	}
	return nil
}

func composeBranchGroup(
	children []Condition,
	kind ConditionKind,
	evaluation ConditionEvaluation,
	position *int,
	matched map[TriggerID]bool,
) (ConditionResult, error) {
	results := make([]ConditionResult, 0, len(children))
	for _, child := range children {
		result, err := composeBranchEvidence(child, evaluation, position, matched)
		if err != nil {
			return "", err
		}
		results = append(results, result)
	}
	return combineConditionGroup(kind, results), nil
}
