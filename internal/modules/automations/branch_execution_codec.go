package automations

import (
	"encoding/json"
	"maps"
)

const branchEvaluatedAtField = "evaluated_at"
const branchKindField = "kind"
const branchPositionField = "position"
const branchStepIDField = "step_id"

func (details BranchExecutionDetails) encodeValidated() (any, error) {
	decision := details.Decision
	raw, err := encodeValidatedBranchDecision(decision)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, invalid("encode branch evidence")
	}
	for _, key := range []string{branchPositionField, branchStepIDField, branchKindField, branchEvaluatedAtField} {
		delete(fields, key)
	}
	if details.ValueFailure != nil {
		fields["value_failure"], err = json.Marshal(details.ValueFailure)
		if err != nil {
			return nil, err
		}
	}
	return fields, nil
}

func decodeBranchExecution(id StepID, env StepExecutionEnvelope) (StepExecutionDetails, error) {
	if env.CompletedAt == nil || !env.CompletedAt.Equal(env.StartedAt) {
		return nil, invalid("branch must be instantaneous")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(env.EvidenceJSON, &fields); err != nil {
		return nil, invalid("invalid branch evidence")
	}
	var failure *ValueFailure
	if raw, ok := fields["value_failure"]; ok {
		var f ValueFailure
		if err := decodeWire(raw, &f); err != nil {
			return nil, err
		}
		failure = &f
		delete(fields, "value_failure")
		env.EvidenceJSON, _ = json.Marshal(fields)
	}
	decision, err := decodeBranchPayload(id, env)
	if err != nil {
		return nil, err
	}
	return BranchExecutionDetails{Decision: decision, ValueFailure: failure}, nil
}

func decodeBranchPayload(id StepID, env StepExecutionEnvelope) (BranchDecision, error) {
	var fields map[string]json.RawMessage
	if err := decodeWire(env.EvidenceJSON, &fields); err != nil {
		return BranchDecision{}, err
	}
	for _, forbidden := range []string{branchStepIDField, branchPositionField, branchKindField, branchEvaluatedAtField} {
		if _, ok := fields[forbidden]; ok {
			return BranchDecision{}, invalid("branch payload duplicates envelope field")
		}
	}
	for k := range fields {
		if k != "outcome" && k != "evaluations" && k != "selected_branch_id" && k != "failure_code" {
			return BranchDecision{}, invalid("unknown branch evidence field")
		}
	}
	kind, _ := json.Marshal(env.Kind)
	sid, _ := json.Marshal(id)
	at, _ := json.Marshal(env.StartedAt)
	phaseOutcome := ""
	if raw, ok := fields["outcome"]; ok {
		if err := json.Unmarshal(raw, &phaseOutcome); err != nil {
			return BranchDecision{}, invalid("invalid branch outcome")
		}
	} else {
		return BranchDecision{}, invalid("branch evidence requires outcome")
	}
	if env.Phase == executionFailed {
		if phaseOutcome != string(BranchUnknown) && phaseOutcome != string(BranchError) {
			return BranchDecision{}, invalid("branch phase contradicts outcome")
		}
	} else if env.Phase != executionSucceeded || phaseOutcome == string(BranchUnknown) || phaseOutcome == string(BranchError) {
		return BranchDecision{}, invalid("branch phase contradicts outcome")
	}
	outcome, _ := json.Marshal(phaseOutcome)
	position := json.RawMessage("0")
	full := map[string]json.RawMessage{
		branchStepIDField:      sid,
		branchPositionField:    position,
		branchKindField:        kind,
		branchEvaluatedAtField: at,
		"outcome":              outcome,
	}
	maps.Copy(full, fields)
	raw, err := json.Marshal(full)
	if err != nil {
		return BranchDecision{}, err
	}
	return decodeBranchDecision(raw)
}
