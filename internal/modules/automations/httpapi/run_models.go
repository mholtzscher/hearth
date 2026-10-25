package httpapi

import (
	"encoding/json"
	"time"
)

// AutomationRunFields are common to active and terminal Runs.
type AutomationRunFields struct {
	DataEvidence      string                                `json:"data_evidence"       enum:"recorded,legacy_unavailable"`
	Data              []json.RawMessage                     `json:"data"`
	ID                string                                `json:"id"`
	AutomationID      string                                `json:"automation_id"`
	AutomationName    string                                `json:"automation_name"`
	Revision          int64                                 `json:"revision"`
	Cause             AdmissionCauseBody                    `json:"cause"`
	MatchedTriggerIDs []string                              `json:"matched_trigger_ids"`
	StartedAt         time.Time                             `json:"started_at"`
	Snapshot          AutomationDefinitionBody              `json:"snapshot"`
	ConditionDecision AutomationConditionDecisionBody       `json:"condition_decision"`
	Steps             []AutomationStepAttemptBody           `json:"steps"`
	BranchDecisions   []AutomationBranchDecisionBody        `json:"branch_decisions"`
	Delays            []AutomationDelayExecutionBody        `json:"delays"`
	Notifications     []AutomationNotificationExecutionBody `json:"notifications"`
	Functions         []AutomationFunctionExecutionBody     `json:"functions"`
}

// AutomationNotificationExecutionBody retains Step outcome independently of
// notification attempt retention. A reserved link need not have admitted a send.
type AutomationNotificationExecutionBody struct {
	StepID      string     `json:"step_id"`
	Position    int        `json:"position"              minimum:"0" maximum:"31"`
	AttemptID   string     `json:"attempt_id"`
	Status      string     `json:"status"                                         enum:"running,accepted,failed,interrupted"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Outcome     *string    `json:"outcome,omitempty"`
	ReasonCode  *string    `json:"reason_code,omitempty"`
}
