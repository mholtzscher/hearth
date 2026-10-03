package automations

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// BranchOutcome records selection or failure, never command completion.
type BranchOutcome string

const branchFailureStateCorrupt = "branch_state_corrupt"

const (
	BranchThen    BranchOutcome = "then"
	BranchElse    BranchOutcome = "else"
	BranchChosen  BranchOutcome = "branch"
	BranchDefault BranchOutcome = "default"
	BranchNoMatch BranchOutcome = "no_match"
	BranchUnknown BranchOutcome = "unknown"
	BranchError   BranchOutcome = "error"
)

// BranchConditionEvaluation retains one fully evaluated root. BranchID is nil
// for If and identifies every evaluated Choose alternative.
type BranchConditionEvaluation struct {
	BranchID   *BranchID
	Evaluation ConditionEvaluation
}

// BranchDecision is immutable evidence for one reached branching Step.
// Position is reached-decision order, independent of Command leaf positions.
type BranchDecision struct {
	Position         int
	StepID           StepID
	Kind             StepKind
	EvaluatedAt      time.Time
	Outcome          BranchOutcome
	SelectedBranchID *BranchID
	Evaluations      []BranchConditionEvaluation
	FailureCode      *string
}

type branchDecisionJSON struct {
	Position         *int                    `json:"position"`
	StepID           StepID                  `json:"step_id"`
	Kind             StepKind                `json:"kind"`
	EvaluatedAt      time.Time               `json:"evaluated_at"`
	Outcome          BranchOutcome           `json:"outcome"`
	SelectedBranchID json.RawMessage         `json:"selected_branch_id,omitempty"`
	Evaluations      *[]branchEvaluationJSON `json:"evaluations"`
	FailureCode      json.RawMessage         `json:"failure_code,omitempty"`
}

type branchEvaluationJSON struct {
	BranchID   json.RawMessage `json:"branch_id,omitempty"`
	Evaluation json.RawMessage `json:"evaluation"`
}

// EncodeBranchDecision validates the record's standalone shape and encodes its
// retained wire form. Repository writes must also call ValidateBranchDecision
// against the immutable Run snapshot and matches.
func EncodeBranchDecision(decision BranchDecision) (json.RawMessage, error) {
	if err := validateBranchDecisionShape(decision); err != nil {
		return nil, err
	}
	evaluations := make([]branchEvaluationJSON, 0, len(decision.Evaluations))
	for _, item := range decision.Evaluations {
		raw, err := encodeAutomationConditionEvaluation(item.Evaluation)
		if err != nil {
			return nil, err
		}
		evaluations = append(evaluations, branchEvaluationJSON{
			BranchID: encodeOptionalBranchMember(item.BranchID), Evaluation: raw,
		})
	}
	value := branchDecisionJSON{
		Position: &decision.Position, StepID: decision.StepID, Kind: decision.Kind,
		EvaluatedAt: decision.EvaluatedAt.UTC(), Outcome: decision.Outcome,
		SelectedBranchID: encodeOptionalBranchMember(decision.SelectedBranchID),
		Evaluations:      &evaluations, FailureCode: encodeOptionalBranchMember(decision.FailureCode),
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: branch decision cannot be encoded: %w", ErrInvalidAutomation, err)
	}
	return raw, nil
}

// DecodeBranchDecision strictly decodes retained evidence without consulting
// current State or definitions. Snapshot-dependent coherence is checked by
// ValidateBranchDecision at the repository boundary.
func DecodeBranchDecision(raw json.RawMessage) (BranchDecision, error) {
	var value branchDecisionJSON
	if err := DecodeStrictJSONObject(raw, &value); err != nil || value.Position == nil || value.Evaluations == nil {
		return BranchDecision{}, invalid("branch decision requires a strict non-null object and all required members")
	}
	selected, err := decodeOptionalConditionMember[BranchID](value.SelectedBranchID, "selected_branch_id")
	if err != nil {
		return BranchDecision{}, err
	}
	failure, err := decodeOptionalConditionMember[string](value.FailureCode, "failure_code")
	if err != nil {
		return BranchDecision{}, err
	}
	decision := BranchDecision{
		Position: *value.Position, StepID: value.StepID, Kind: value.Kind,
		EvaluatedAt: value.EvaluatedAt.UTC(), Outcome: value.Outcome,
		SelectedBranchID: selected, FailureCode: failure,
		Evaluations: make([]BranchConditionEvaluation, 0, len(*value.Evaluations)),
	}
	if len(*value.Evaluations) > automationStepMaxCount {
		return BranchDecision{}, invalid("branch decision exceeds the alternative bound")
	}
	for _, item := range *value.Evaluations {
		id, decodeErr := decodeOptionalConditionMember[BranchID](item.BranchID, "branch_id")
		if decodeErr != nil {
			return BranchDecision{}, decodeErr
		}
		evaluationJSON, decodeErr := decodeConditionEvaluationJSON(item.Evaluation)
		if decodeErr != nil || evaluationJSON == nil {
			return BranchDecision{}, invalid("branch decision requires a complete evaluation")
		}
		evaluation, decodeErr := automationConditionEvaluationFromJSON(*evaluationJSON)
		if decodeErr != nil {
			return BranchDecision{}, decodeErr
		}
		decision.Evaluations = append(
			decision.Evaluations,
			BranchConditionEvaluation{BranchID: id, Evaluation: evaluation},
		)
	}
	if err = validateBranchDecisionShape(decision); err != nil {
		return BranchDecision{}, err
	}
	return decision, nil
}

func encodeOptionalBranchMember[T ~string](value *T) json.RawMessage {
	if value == nil {
		return nil
	}
	raw, _ := json.Marshal(*value) // String members cannot fail JSON encoding.
	return raw
}

func validateBranchDecisionShape(decision BranchDecision) error {
	if decision.Position < 0 || decision.Position >= automationAllStepsMaxCount ||
		!subjectSlugPattern.MatchString(string(decision.StepID)) || decision.EvaluatedAt.IsZero() {
		return invalid("branch decision has invalid position, Step identity, or time")
	}
	if decision.Kind != StepKindIf && decision.Kind != StepKindChoose {
		return invalid("branch decision requires If or Choose kind")
	}
	if len(decision.Evaluations) > automationStepMaxCount {
		return invalid("branch decision exceeds the alternative bound")
	}
	seen := make(map[BranchID]bool)
	for _, item := range decision.Evaluations {
		if decision.Kind == StepKindIf {
			if item.BranchID != nil || len(decision.Evaluations) != 1 {
				return invalid("If decision requires one unlabelled evaluation")
			}
		} else {
			if item.BranchID == nil || !subjectSlugPattern.MatchString(string(*item.BranchID)) || seen[*item.BranchID] {
				return invalid("Choose evaluations require unique valid branch IDs")
			}
			seen[*item.BranchID] = true
		}
		if !item.Evaluation.EvaluatedAt.Equal(decision.EvaluatedAt) {
			return invalid("branch evaluation time differs from decision time")
		}
		if err := validateBranchEvaluationShape(item.Evaluation); err != nil {
			return err
		}
	}
	return validateBranchOutcome(decision)
}

func validateBranchOutcome(decision BranchDecision) error {
	failing := decision.Outcome == BranchUnknown || decision.Outcome == BranchError
	if failing != (decision.FailureCode != nil) ||
		(decision.Outcome == BranchChosen) != (decision.SelectedBranchID != nil) {
		return invalid("branch outcome has inconsistent failure or selection members")
	}
	if decision.SelectedBranchID != nil && !subjectSlugPattern.MatchString(string(*decision.SelectedBranchID)) {
		return invalid("branch selection ID is invalid")
	}
	last := ConditionFalse
	for index, item := range decision.Evaluations {
		last = item.Evaluation.Result
		if index < len(decision.Evaluations)-1 && last != ConditionFalse {
			return invalid("branch evaluations must have a false prefix")
		}
	}
	if decision.Outcome != BranchError && len(decision.Evaluations) == 0 {
		return invalid("branch selection or unknown requires evaluation evidence")
	}
	if !branchOutcomeMatchesResult(decision, last) {
		return invalid("branch outcome, kind, root result, or failure code is inconsistent")
	}
	return nil
}

func branchOutcomeMatchesResult(decision BranchDecision, last ConditionResult) bool {
	switch decision.Outcome {
	case BranchThen:
		return decision.Kind == StepKindIf && last == ConditionTrue
	case BranchElse:
		return decision.Kind == StepKindIf && last == ConditionFalse
	case BranchChosen:
		return decision.Kind == StepKindChoose && last == ConditionTrue &&
			*decision.SelectedBranchID == *decision.Evaluations[len(decision.Evaluations)-1].BranchID
	case BranchDefault:
		return decision.Kind == StepKindChoose && last == ConditionFalse
	case BranchNoMatch:
		return last == ConditionFalse
	case BranchUnknown:
		return last == ConditionUnknown && *decision.FailureCode == "branch_condition_unknown"
	case BranchError:
		return last == ConditionFalse && branchErrorHasValidPrefix(decision)
	default:
		return false
	}
}

func branchErrorHasValidPrefix(decision BranchDecision) bool {
	if decision.Kind == StepKindIf && len(decision.Evaluations) != 0 {
		return false
	}
	switch *decision.FailureCode {
	case "branch_state_read_failed", "branch_snapshot_incomplete":
		return len(decision.Evaluations) == 0
	case branchFailureStateCorrupt:
		return true
	default:
		return false
	}
}

func validateBranchEvaluationShape(evaluation ConditionEvaluation) error {
	if evaluation.EvaluatedAt.IsZero() || !validConditionResult(evaluation.Result) ||
		len(evaluation.Nodes) < 1 || len(evaluation.Nodes) > automationConditionMaxNodes {
		return invalid("branch evaluation requires bounded leaf evidence, a result, and time")
	}
	seen := make(map[ConditionID]bool)
	for _, node := range evaluation.Nodes {
		if !subjectSlugPattern.MatchString(string(node.ID)) || seen[node.ID] || !validConditionResult(node.Result) {
			return invalid("branch evaluation has invalid or duplicate leaf identities or results")
		}
		seen[node.ID] = true
		if err := validateBranchLeafShape(node); err != nil {
			return err
		}
	}
	return nil
}

func validConditionResult(result ConditionResult) bool {
	return result == ConditionTrue || result == ConditionFalse || result == ConditionUnknown
}

func validateBranchLeafShape(node ConditionNodeResult) error {
	if node.Trigger != nil {
		return validateTriggerLeafShape(node)
	}
	return validateStateLeafShape(node)
}

func validateTriggerLeafShape(node ConditionNodeResult) error {
	if node.Result == ConditionUnknown || node.UnknownReason != nil || node.SelectedValue != nil ||
		node.ObservationID != nil || node.ObservedAt != nil {
		return invalid("Trigger leaf cannot carry unknown or State evidence")
	}
	ids := node.Trigger.MatchedTriggerIDs
	if len(ids) > automationTriggerMaxCount || node.Result != conditionResultFromMatch(len(ids) > 0) {
		return invalid("Trigger leaf result disagrees with its bounded intersection")
	}
	seen := make(map[TriggerID]bool)
	for _, id := range ids {
		if !subjectSlugPattern.MatchString(string(id)) || seen[id] {
			return invalid("Trigger evidence IDs must be valid and unique")
		}
		seen[id] = true
	}
	return nil
}

func validateStateLeafShape(node ConditionNodeResult) error {
	if (node.Result == ConditionUnknown) != (node.UnknownReason != nil) ||
		(node.ObservationID == nil) != (node.ObservedAt == nil) {
		return invalid("State leaf has inconsistent reason or Observation evidence")
	}
	if node.ObservationID != nil {
		if _, err := devices.ParseObservationID(string(*node.ObservationID)); err != nil {
			return invalid("State leaf Observation identity is invalid")
		}
		if node.ObservedAt.IsZero() {
			return invalid("State leaf Observation time is zero")
		}
	}
	if node.SelectedValue != nil {
		if _, err := decodeJSONValue(node.SelectedValue); err != nil {
			return invalid("State leaf selection must be exactly one JSON value")
		}
	}
	if node.UnknownReason == nil {
		if node.SelectedValue == nil || node.ObservationID == nil {
			return invalid("known State leaf requires selection and Observation evidence")
		}
		return nil
	}
	if !stateUnknownHasValidShape(node) {
		return invalid("State leaf unknown reason disagrees with its evidence shape")
	}
	return nil
}

func stateUnknownHasValidShape(node ConditionNodeResult) bool {
	switch *node.UnknownReason {
	case ConditionUnknownEntityMissing, ConditionUnknownStateMissing:
		return node.SelectedValue == nil && node.ObservationID == nil
	case ConditionUnknownPointerMissing:
		return node.SelectedValue == nil && node.ObservationID != nil
	case ConditionUnknownTypeMismatch:
		return node.SelectedValue != nil && node.ObservationID != nil
	case ConditionUnknownEvidenceInFuture, ConditionUnknownEvidenceExpired:
		return node.ObservationID != nil
	default:
		return false
	}
}
