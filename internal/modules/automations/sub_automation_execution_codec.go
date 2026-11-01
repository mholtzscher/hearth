package automations

import (
	"bytes"
	"encoding/json"
)

type subAutomationWire struct {
	ChildRunID       RunID     `json:"child_run_id,omitempty"`
	ReasonCode       string    `json:"reason_code,omitempty"`
	SkipID           *SkipID   `json:"skip_id,omitempty"`
	ChildStatus      RunStatus `json:"child_status,omitempty"`
	ChildFailureCode string    `json:"child_failure_code,omitempty"`
}

func (d SubAutomationExecutionDetails) encodeValidated() (any, error) {
	var w subAutomationWire
	switch s := d.State.(type) {
	case SubAutomationRunning:
		w.ChildRunID = s.ChildRunID
	case SubAutomationSucceeded:
		w.ChildRunID = s.ChildRunID
	case SubAutomationRejected:
		w.ReasonCode, w.SkipID = s.ReasonCode, s.SkipID
	case SubAutomationInterrupted:
		w.ChildRunID, w.ReasonCode = s.ChildRunID, s.ReasonCode
	case SubAutomationChildFailed:
		w.ChildRunID, w.ChildStatus = s.ChildRunID, RunOutcomeStatus(s.Outcome)
		w.ChildFailureCode = *RunOutcomeFailureCode(s.Outcome)
		if w.ChildStatus == RunFailed {
			w.ReasonCode = subAutomationChildFailedCode
		} else {
			w.ReasonCode = subAutomationChildInterruptedCode
		}
	default:
		return nil, invalid("invalid SubAutomation state")
	}
	return w, nil
}

//nolint:gocognit,gocyclo,cyclop,funlen // Each lifecycle shape has distinct mandatory and forbidden fields.
func decodeSubAutomationExecution(env StepExecutionEnvelope) (StepExecutionDetails, error) {
	fields, err := decodeEvidenceObject(env.EvidenceJSON)
	if err != nil {
		return nil, err
	}
	for name, value := range fields {
		switch name {
		case "child_run_id", "reason_code", "skip_id", "child_status", "child_failure_code":
		default:
			return nil, invalid("unknown SubAutomation evidence field")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, invalid("null SubAutomation evidence field")
		}
	}
	var w subAutomationWire
	if err = decodeWire(env.EvidenceJSON, &w); err != nil {
		return nil, err
	}
	d := SubAutomationExecutionDetails{StartedAt: env.StartedAt}
	switch env.Phase {
	case executionRunning:
		if env.CompletedAt != nil || w.ReasonCode != "" || w.SkipID != nil || w.ChildStatus != "" ||
			w.ChildFailureCode != "" {
			return nil, invalid("invalid running SubAutomation")
		}
		d.State = SubAutomationRunning{ChildRunID: w.ChildRunID}
	case executionSucceeded:
		if env.CompletedAt == nil || w.ReasonCode != "" || w.SkipID != nil || w.ChildStatus != "" ||
			w.ChildFailureCode != "" {
			return nil, invalid("invalid successful SubAutomation")
		}
		d.State = SubAutomationSucceeded{ChildRunID: w.ChildRunID, CompletedAt: *env.CompletedAt}
	case executionInterrupted:
		if env.CompletedAt == nil || w.SkipID != nil || w.ChildStatus != "" || w.ChildFailureCode != "" {
			return nil, invalid("invalid interrupted SubAutomation")
		}
		d.State = SubAutomationInterrupted{
			ChildRunID:  w.ChildRunID,
			CompletedAt: *env.CompletedAt,
			ReasonCode:  w.ReasonCode,
		}
	case executionFailed:
		if env.CompletedAt == nil {
			return nil, invalid("missing SubAutomation completion")
		}
		if w.ChildRunID == "" {
			if w.ChildStatus != "" || w.ChildFailureCode != "" {
				return nil, invalid("invalid rejected SubAutomation")
			}
			d.State = SubAutomationRejected{CompletedAt: *env.CompletedAt, ReasonCode: w.ReasonCode, SkipID: w.SkipID}
		} else {
			if w.SkipID != nil {
				return nil, invalid("invalid child failure")
			}
			var outcome RunOutcome
			switch {
			case w.ChildStatus == RunFailed && w.ReasonCode == subAutomationChildFailedCode:
				outcome = FailedRun{FailureCode: w.ChildFailureCode}
			case w.ChildStatus == RunInterrupted && w.ReasonCode == subAutomationChildInterruptedCode:
				outcome = InterruptedRun{FailureCode: w.ChildFailureCode}
			default:
				return nil, invalid("invalid child failure")
			}
			d.State = SubAutomationChildFailed{
				ChildRunID:  w.ChildRunID,
				CompletedAt: *env.CompletedAt,
				Outcome:     outcome,
			}
		}
	default:
		return nil, invalid("invalid SubAutomation phase")
	}
	if err = d.Validate(); err != nil {
		return nil, err
	}
	canonical, err := d.encodeValidated()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	expected, err := decodeEvidenceObject(raw)
	if err != nil {
		return nil, err
	}
	if len(expected) != len(fields) {
		return nil, invalid("unexpected SubAutomation evidence field")
	}
	for name := range fields {
		if _, ok := expected[name]; !ok {
			return nil, invalid("unexpected SubAutomation evidence field")
		}
	}
	return d, nil
}
