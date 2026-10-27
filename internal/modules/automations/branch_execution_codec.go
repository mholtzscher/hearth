package automations

import "encoding/json"

func (details BranchExecutionDetails) encodeValidated() (any, error) {
	decision := details.Decision
	// Keep the existing sorted payload encoding, including exact evaluation numbers.
	fields := map[string]any{"outcome": decision.Outcome()}
	if code := decision.FailureCode(); code != nil {
		fields["failure_code"] = code
	}
	if details.ValueFailure != nil {
		fields["value_failure"] = details.ValueFailure
	}
	switch body := decision.Body.(type) {
	case IfDecision:
		evaluations := []ifEvaluationJSON{}
		var evaluation *ConditionEvaluation
		switch result := body.Result.(type) {
		case IfSelected:
			evaluation = &result.Evaluation
		case IfUnknown:
			evaluation = &result.Evaluation
		case IfError:
		}
		if evaluation != nil {
			raw, err := encodeAutomationConditionEvaluation(*evaluation)
			if err != nil {
				return nil, err
			}
			evaluations = append(evaluations, ifEvaluationJSON{Evaluation: raw})
		}
		fields["evaluations"] = evaluations
	case ChooseDecision:
		evaluations := []chooseEvaluationJSON{}
		var prefix []ChooseEvaluation
		switch result := body.Result.(type) {
		case ChooseSelected:
			prefix = result.Evaluations
			fields["selected_branch_id"] = result.BranchID
		case ChooseFallback:
			prefix = result.Evaluations
		case ChooseUnknown:
			prefix = result.Evaluations
		case ChooseError:
			prefix = result.Evaluations
		}
		for _, item := range prefix {
			raw, err := encodeAutomationConditionEvaluation(item.Evaluation)
			if err != nil {
				return nil, err
			}
			evaluations = append(evaluations, chooseEvaluationJSON{BranchID: item.BranchID, Evaluation: raw})
		}
		fields["evaluations"] = evaluations
	default:
		return nil, invalid("unsupported branch decision representation")
	}
	return fields, nil
}

func decodeBranchExecution(
	id StepID,
	env StepExecutionEnvelope,
	fields map[string]json.RawMessage,
) (StepExecutionDetails, error) {
	if env.CompletedAt == nil || !env.CompletedAt.Equal(env.StartedAt) {
		return nil, invalid("branch must be instantaneous")
	}
	for key := range fields {
		switch key {
		case "outcome", "evaluations", "selected_branch_id", "failure_code", "value_failure":
		default:
			return nil, invalid("unknown branch evidence field")
		}
	}
	var header branchPayloadHeaderJSON
	if err := json.Unmarshal(env.EvidenceJSON, &header); err != nil {
		return nil, invalid("invalid branch evidence")
	}
	details := BranchExecutionDetails{Decision: BranchDecision{StepID: id, EvaluatedAt: env.StartedAt}}
	var err error
	if env.Kind == StepKindIf {
		var result IfDecisionResult
		result, err = decodeIfResult(env.EvidenceJSON, header.Outcome)
		details.Decision.Body = IfDecision{Result: result}
	} else {
		var result ChooseDecisionResult
		result, err = decodeChooseResult(env.EvidenceJSON, header.Outcome)
		details.Decision.Body = ChooseDecision{Result: result}
	}
	if err != nil {
		return nil, err
	}
	if header.ValueFailure != nil {
		var failure ValueFailure
		if err = decodeWire(header.ValueFailure, &failure); err != nil {
			return nil, err
		}
		details.ValueFailure = &failure
	}
	return details, nil
}
