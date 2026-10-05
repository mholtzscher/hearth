package automations

import (
	"encoding/json"
	"time"
)

type branchDecisionHeaderJSON struct {
	Position    *int          `json:"position"`
	StepID      StepID        `json:"step_id"`
	Kind        StepKind      `json:"kind"`
	EvaluatedAt time.Time     `json:"evaluated_at"`
	Outcome     BranchOutcome `json:"outcome"`
}
type ifEvaluationJSON struct {
	Evaluation json.RawMessage `json:"evaluation"`
}
type chooseEvaluationJSON struct {
	BranchID   BranchID        `json:"branch_id"`
	Evaluation json.RawMessage `json:"evaluation"`
}
type ifSelectedJSON struct {
	branchDecisionHeaderJSON

	Evaluations *[]ifEvaluationJSON `json:"evaluations"`
}
type ifFailureJSON struct {
	branchDecisionHeaderJSON

	Evaluations *[]ifEvaluationJSON `json:"evaluations"`
	FailureCode string              `json:"failure_code"`
}
type chooseFallbackJSON struct {
	branchDecisionHeaderJSON

	Evaluations *[]chooseEvaluationJSON `json:"evaluations"`
}
type chooseSelectedJSON struct {
	branchDecisionHeaderJSON

	Evaluations      *[]chooseEvaluationJSON `json:"evaluations"`
	SelectedBranchID BranchID                `json:"selected_branch_id"`
}
type chooseFailureJSON struct {
	branchDecisionHeaderJSON

	Evaluations *[]chooseEvaluationJSON `json:"evaluations"`
	FailureCode string                  `json:"failure_code"`
}

// EncodeBranchDecision validates and encodes one concrete retained result.
func EncodeBranchDecision(decision BranchDecision) (json.RawMessage, error) {
	if err := validateBranchDecisionShape(decision); err != nil {
		return nil, err
	}
	header := branchDecisionHeaderJSON{Position: &decision.Position, StepID: decision.StepID,
		Kind: decision.Kind(), EvaluatedAt: decision.EvaluatedAt.UTC(), Outcome: decision.Outcome()}
	switch body := decision.Body.(type) {
	case IfDecision:
		return encodeIfDecision(header, body.Result)
	case ChooseDecision:
		return encodeChooseDecision(header, body.Result)
	default:
		return nil, invalid("unsupported branch decision representation")
	}
}

func encodeIfDecision(header branchDecisionHeaderJSON, result IfDecisionResult) (json.RawMessage, error) {
	switch value := result.(type) {
	case IfSelected:
		evaluations, err := encodeIfEvaluation(value.Evaluation)
		if err != nil {
			return nil, err
		}
		return marshalRetainedEvidence(ifSelectedJSON{branchDecisionHeaderJSON: header, Evaluations: &evaluations})
	case IfUnknown:
		evaluations, err := encodeIfEvaluation(value.Evaluation)
		if err != nil {
			return nil, err
		}
		return marshalRetainedEvidence(
			ifFailureJSON{
				branchDecisionHeaderJSON: header,
				Evaluations:              &evaluations,
				FailureCode:              branchFailureConditionUnknown,
			},
		)
	case IfError:
		evaluations := []ifEvaluationJSON{}
		return marshalRetainedEvidence(
			ifFailureJSON{branchDecisionHeaderJSON: header, Evaluations: &evaluations, FailureCode: value.FailureCode},
		)
	default:
		return nil, invalid("unsupported If result representation")
	}
}

func encodeIfEvaluation(evaluation ConditionEvaluation) ([]ifEvaluationJSON, error) {
	raw, err := encodeAutomationConditionEvaluation(evaluation)
	if err != nil {
		return nil, err
	}
	return []ifEvaluationJSON{{Evaluation: raw}}, nil
}

func encodeChooseDecision(header branchDecisionHeaderJSON, result ChooseDecisionResult) (json.RawMessage, error) {
	var prefix []ChooseEvaluation
	switch value := result.(type) {
	case ChooseSelected:
		prefix = value.Evaluations
	case ChooseFallback:
		prefix = value.Evaluations
	case ChooseUnknown:
		prefix = value.Evaluations
	case ChooseError:
		prefix = value.Evaluations
	default:
		return nil, invalid("unsupported Choose result representation")
	}
	evaluations := make([]chooseEvaluationJSON, 0, len(prefix))
	for _, item := range prefix {
		raw, err := encodeAutomationConditionEvaluation(item.Evaluation)
		if err != nil {
			return nil, err
		}
		evaluations = append(evaluations, chooseEvaluationJSON{BranchID: item.BranchID, Evaluation: raw})
	}
	switch value := result.(type) {
	case ChooseSelected:
		return marshalRetainedEvidence(
			chooseSelectedJSON{
				branchDecisionHeaderJSON: header,
				Evaluations:              &evaluations,
				SelectedBranchID:         value.BranchID,
			},
		)
	case ChooseFallback:
		return marshalRetainedEvidence(chooseFallbackJSON{branchDecisionHeaderJSON: header, Evaluations: &evaluations})
	case ChooseUnknown:
		return marshalRetainedEvidence(
			chooseFailureJSON{
				branchDecisionHeaderJSON: header,
				Evaluations:              &evaluations,
				FailureCode:              branchFailureConditionUnknown,
			},
		)
	case ChooseError:
		return marshalRetainedEvidence(
			chooseFailureJSON{
				branchDecisionHeaderJSON: header,
				Evaluations:              &evaluations,
				FailureCode:              value.FailureCode,
			},
		)
	default:
		return nil, invalid("unsupported Choose result representation")
	}
}

// DecodeBranchDecision selects an exact DTO before binding retained evidence.
func DecodeBranchDecision(raw json.RawMessage) (BranchDecision, error) {
	var header branchDecisionHeaderJSON
	if json.Unmarshal(raw, &header) != nil || header.Position == nil {
		return BranchDecision{}, invalid("branch decision requires its envelope")
	}
	decision := BranchDecision{Position: *header.Position, StepID: header.StepID, EvaluatedAt: header.EvaluatedAt.UTC()}
	var err error
	switch header.Kind {
	case StepKindIf:
		var result IfDecisionResult
		result, err = decodeIfResult(raw, header.Outcome)
		decision.Body = IfDecision{Result: result}
	case StepKindChoose:
		var result ChooseDecisionResult
		result, err = decodeChooseResult(raw, header.Outcome)
		decision.Body = ChooseDecision{Result: result}
	case StepKindCommand, StepKindDelay:
		return BranchDecision{}, invalid("nonbranch Step cannot have a branch decision")
	default:
		return BranchDecision{}, invalid("unknown branch decision kind")
	}
	if err != nil {
		return BranchDecision{}, err
	}
	if err = validateBranchDecisionShape(decision); err != nil {
		return BranchDecision{}, err
	}
	return decision, nil
}

func decodeIfResult(raw json.RawMessage, outcome BranchOutcome) (IfDecisionResult, error) {
	switch outcome {
	case BranchThen, BranchElse, BranchNoMatch:
		var value ifSelectedJSON
		if DecodeStrictJSONObject(raw, &value) != nil || value.Evaluations == nil {
			return nil, invalid("If selection requires exact evaluations")
		}
		evaluation, err := decodeIfEvaluation(*value.Evaluations)
		if err != nil {
			return nil, err
		}
		return IfSelected{Arm: IfArm(outcome), Evaluation: evaluation}, nil
	case BranchUnknown, BranchError:
		var value ifFailureJSON
		if DecodeStrictJSONObject(raw, &value) != nil || value.Evaluations == nil {
			return nil, invalid("If failure requires exact evaluations and failure code")
		}
		if outcome == BranchError {
			if len(*value.Evaluations) != 0 {
				return nil, invalid("If error cannot retain a completed root")
			}
			return IfError{FailureCode: value.FailureCode}, nil
		}
		if value.FailureCode != branchFailureConditionUnknown {
			return nil, invalid("unknown branch has fixed failure code")
		}
		evaluation, err := decodeIfEvaluation(*value.Evaluations)
		if err != nil {
			return nil, err
		}
		return IfUnknown{Evaluation: evaluation}, nil
	case BranchChosen, BranchDefault:
		return nil, invalid("Choose outcome cannot belong to If")
	default:
		return nil, invalid("unknown If outcome")
	}
}

func decodeIfEvaluation(values []ifEvaluationJSON) (ConditionEvaluation, error) {
	if len(values) != 1 {
		return ConditionEvaluation{}, invalid("If requires one completed root")
	}
	return decodeBranchRoot(values[0].Evaluation)
}

func decodeChooseResult(raw json.RawMessage, outcome BranchOutcome) (ChooseDecisionResult, error) {
	switch outcome {
	case BranchChosen:
		var value chooseSelectedJSON
		if DecodeStrictJSONObject(raw, &value) != nil || value.Evaluations == nil {
			return nil, invalid("Choose selection requires exact evidence")
		}
		evaluations, err := decodeChooseEvaluations(*value.Evaluations)
		if err != nil {
			return nil, err
		}
		return ChooseSelected{BranchID: value.SelectedBranchID, Evaluations: evaluations}, nil
	case BranchDefault, BranchNoMatch:
		var value chooseFallbackJSON
		if DecodeStrictJSONObject(raw, &value) != nil || value.Evaluations == nil {
			return nil, invalid("Choose fallback requires exact evidence")
		}
		evaluations, err := decodeChooseEvaluations(*value.Evaluations)
		if err != nil {
			return nil, err
		}
		return ChooseFallback{Arm: ChooseFallbackArm(outcome), Evaluations: evaluations}, nil
	case BranchUnknown, BranchError:
		var value chooseFailureJSON
		if DecodeStrictJSONObject(raw, &value) != nil || value.Evaluations == nil {
			return nil, invalid("Choose failure requires exact evidence")
		}
		evaluations, err := decodeChooseEvaluations(*value.Evaluations)
		if err != nil {
			return nil, err
		}
		if outcome == BranchError {
			return ChooseError{FailureCode: value.FailureCode, Evaluations: evaluations}, nil
		}
		if value.FailureCode != branchFailureConditionUnknown {
			return nil, invalid("unknown branch has fixed failure code")
		}
		return ChooseUnknown{Evaluations: evaluations}, nil
	case BranchThen, BranchElse:
		return nil, invalid("If outcome cannot belong to Choose")
	default:
		return nil, invalid("unknown Choose outcome")
	}
}

func decodeChooseEvaluations(values []chooseEvaluationJSON) ([]ChooseEvaluation, error) {
	if len(values) > automationStepMaxCount {
		return nil, invalid("branch prefix exceeds alternative bound")
	}
	evaluations := make([]ChooseEvaluation, 0, len(values))
	for _, value := range values {
		evaluation, err := decodeBranchRoot(value.Evaluation)
		if err != nil {
			return nil, err
		}
		evaluations = append(evaluations, ChooseEvaluation{BranchID: value.BranchID, Evaluation: evaluation})
	}
	return evaluations, nil
}

func decodeBranchRoot(raw json.RawMessage) (ConditionEvaluation, error) {
	value, err := decodeConditionEvaluationJSON(raw)
	if err != nil || value == nil {
		return ConditionEvaluation{}, invalid("branch decision requires a complete evaluation")
	}
	return automationConditionEvaluationFromJSON(*value)
}
