package automations

import "encoding/json"

type branchPayloadHeaderJSON struct {
	Outcome      BranchOutcome   `json:"outcome"`
	ValueFailure json.RawMessage `json:"value_failure,omitempty"`
}
type ifEvaluationJSON struct {
	Evaluation json.RawMessage `json:"evaluation"`
}
type chooseEvaluationJSON struct {
	BranchID   BranchID        `json:"branch_id"`
	Evaluation json.RawMessage `json:"evaluation"`
}
type ifSelectedJSON struct {
	branchPayloadHeaderJSON

	Evaluations *[]ifEvaluationJSON `json:"evaluations"`
}
type ifFailureJSON struct {
	branchPayloadHeaderJSON

	Evaluations *[]ifEvaluationJSON `json:"evaluations"`
	FailureCode string              `json:"failure_code"`
}
type chooseFallbackJSON struct {
	branchPayloadHeaderJSON

	Evaluations *[]chooseEvaluationJSON `json:"evaluations"`
}
type chooseSelectedJSON struct {
	branchPayloadHeaderJSON

	Evaluations      *[]chooseEvaluationJSON `json:"evaluations"`
	SelectedBranchID BranchID                `json:"selected_branch_id"`
}
type chooseFailureJSON struct {
	branchPayloadHeaderJSON

	Evaluations *[]chooseEvaluationJSON `json:"evaluations"`
	FailureCode string                  `json:"failure_code"`
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
