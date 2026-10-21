package mcpapi_test

import (
	"encoding/json"
	"time"
)

// These read models belong to transport assertions, not production output.
// Tests inspect the flat wire fields without requiring output DTOs to decode.
type AutomationComparisonRead struct {
	Pointer  string          `json:"value_pointer"`
	Operator string          `json:"operator"`
	Operand  json.RawMessage `json:"operand"`
}

type AutomationTriggerRead struct {
	ID                  string                     `json:"id"`
	Kind                string                     `json:"kind"`
	EntityID            string                     `json:"entity_id"`
	Dispositions        []string                   `json:"dispositions"`
	PreviousComparisons []AutomationComparisonRead `json:"previous_comparisons"`
	Comparisons         []AutomationComparisonRead `json:"comparisons"`
	EventName           string                     `json:"event_name"`
	ForSeconds          *int64                     `json:"for_seconds"`
	Expression          string                     `json:"expression"`
}

type AutomationConditionRead struct {
	ID            string                    `json:"id"`
	Kind          string                    `json:"kind"`
	EntityID      string                    `json:"entity_id"`
	ValuePointer  *string                   `json:"value_pointer"`
	Operator      string                    `json:"operator"`
	Operand       json.RawMessage           `json:"operand"`
	MaxAgeSeconds *int64                    `json:"max_age_seconds"`
	TriggerIDs    []string                  `json:"trigger_ids"`
	Children      []AutomationConditionRead `json:"children"`
	Child         *AutomationConditionRead  `json:"child"`
}

type AutomationStepRead struct {
	ID         string                       `json:"id"`
	Kind       string                       `json:"kind"`
	EntityID   string                       `json:"entity_id"`
	Operation  string                       `json:"operation"`
	Parameters json.RawMessage              `json:"parameters"`
	Conditions *AutomationConditionRead     `json:"conditions"`
	Then       []AutomationStepRead         `json:"then"`
	Else       []AutomationStepRead         `json:"else"`
	Branches   []AutomationChooseBranchRead `json:"branches"`
	Default    []AutomationStepRead         `json:"default"`
}

type AutomationChooseBranchRead struct {
	ID         string                  `json:"id"`
	Conditions AutomationConditionRead `json:"conditions"`
	Steps      []AutomationStepRead    `json:"steps"`
}

type AutomationDefinitionRead struct {
	Name       string                   `json:"name"`
	Enabled    bool                     `json:"enabled"`
	Triggers   []AutomationTriggerRead  `json:"triggers"`
	Conditions *AutomationConditionRead `json:"conditions"`
	Steps      []AutomationStepRead     `json:"steps"`
}

type AutomationDetailRead struct {
	ID         string                   `json:"automation_id"`
	Revision   int64                    `json:"revision"`
	CreatedAt  time.Time                `json:"created_at"`
	UpdatedAt  time.Time                `json:"updated_at"`
	Definition AutomationDefinitionRead `json:"definition"`
}

type AutomationCollectionRead struct {
	Items      []AutomationSummaryRead `json:"items"`
	NextCursor *string                 `json:"next_cursor"`
}

type AutomationSummaryRead struct {
	ID       string `json:"automation_id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Revision int64  `json:"revision"`
}

type AutomationStepAttemptRead struct {
	Position          int        `json:"position"`
	StepID            string     `json:"step_id"`
	Status            string     `json:"status"`
	VerifiedCommandID *string    `json:"verified_command_id"`
	FailureCode       *string    `json:"failure_code"`
	StartedAt         *time.Time `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
}

type AutomationConditionNodeResultRead struct {
	ID                string          `json:"id"`
	Kind              string          `json:"kind"`
	Result            string          `json:"result"`
	UnknownReason     *string         `json:"unknown_reason"`
	SelectedValue     json.RawMessage `json:"selected_value"`
	ObservationID     *string         `json:"observation_id"`
	ObservedAt        *time.Time      `json:"observed_at"`
	MatchedTriggerIDs *[]string       `json:"matched_trigger_ids"`
}

type AutomationConditionEvaluationRead struct {
	EvaluatedAt time.Time                           `json:"evaluated_at"`
	Result      string                              `json:"result"`
	Nodes       []AutomationConditionNodeResultRead `json:"nodes"`
}

type AutomationConditionDecisionRead struct {
	Mode            string                             `json:"mode"`
	BypassRequested bool                               `json:"bypass_requested"`
	Snapshot        *AutomationConditionRead           `json:"snapshot"`
	Evaluation      *AutomationConditionEvaluationRead `json:"evaluation"`
}

type AutomationBranchConditionEvaluationRead struct {
	BranchID   *string                           `json:"branch_id"`
	Evaluation AutomationConditionEvaluationRead `json:"evaluation"`
}

type AutomationBranchDecisionRead struct {
	Position         int                                       `json:"position"`
	StepID           string                                    `json:"step_id"`
	Kind             string                                    `json:"kind"`
	EvaluatedAt      time.Time                                 `json:"evaluated_at"`
	Outcome          string                                    `json:"outcome"`
	SelectedBranchID *string                                   `json:"selected_branch_id"`
	Evaluations      []AutomationBranchConditionEvaluationRead `json:"evaluations"`
	FailureCode      *string                                   `json:"failure_code"`
}

type AutomationRunRead struct {
	ID                string                          `json:"entry_id"`
	AutomationID      string                          `json:"automation_id"`
	AutomationName    string                          `json:"automation_name"`
	Revision          int64                           `json:"revision"`
	Cause             AdmissionCauseRead              `json:"cause"`
	MatchedTriggerIDs []string                        `json:"matched_trigger_ids"`
	Status            string                          `json:"status"`
	FailureCode       *string                         `json:"failure_code"`
	StartedAt         time.Time                       `json:"started_at"`
	CompletedAt       *time.Time                      `json:"completed_at"`
	Snapshot          AutomationDefinitionRead        `json:"snapshot"`
	ConditionDecision AutomationConditionDecisionRead `json:"condition_decision"`
	Steps             []AutomationStepAttemptRead     `json:"steps"`
	BranchDecisions   []AutomationBranchDecisionRead  `json:"branch_decisions"`
}

type AutomationHistorySummaryRead struct {
	ID           string    `json:"entry_id"`
	Kind         string    `json:"kind"`
	AutomationID string    `json:"automation_id"`
	Revision     int64     `json:"revision"`
	RecordedAt   time.Time `json:"recorded_at"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason"`
	Source       string    `json:"source"`
}

type AutomationHistoryCollectionRead struct {
	Items      []AutomationHistorySummaryRead `json:"items"`
	NextCursor *string                        `json:"next_cursor"`
}

// Provenance evidence is asserted independently as canonical JSON.
type AdmissionCauseRead struct {
	Kind string `json:"kind"`
}

type AutomationReceiptRead struct {
	ID       string `json:"automation_id"`
	Revision int64  `json:"revision"`
}
type RunReceiptRead struct {
	ID           string `json:"run_id"`
	AutomationID string `json:"automation_id"`
	Status       string `json:"status"`
}
type HistoryDetailRead struct {
	AutomationRunRead

	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}
