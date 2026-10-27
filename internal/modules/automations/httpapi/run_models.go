package httpapi

import (
	"encoding/json"
	"time"
)

// AutomationRunFields are common to active and terminal Runs.
type AutomationRunFields struct {
	ID                  string                          `json:"id"`
	AutomationID        string                          `json:"automation_id"`
	AutomationName      string                          `json:"automation_name"`
	Revision            int64                           `json:"revision"`
	Cause               AdmissionCauseBody              `json:"cause"`
	MatchedTriggerIDs   []string                        `json:"matched_trigger_ids"`
	StartedAt           time.Time                       `json:"started_at"`
	Snapshot            AutomationDefinitionBody        `json:"snapshot"`
	ConditionDecision   AutomationConditionDecisionBody `json:"condition_decision"`
	Executions          []AutomationExecutionBody       `json:"executions"`
	DataEvidence        string                          `json:"data_evidence"`
	ValueInitialization json.RawMessage                 `json:"value_initialization"`
}

// AutomationExecutionBody is one durably reached instruction in global Run order.
type AutomationExecutionBody struct{ encoded []byte }

func (body AutomationExecutionBody) MarshalJSON() ([]byte, error) { return body.encoded, nil }
