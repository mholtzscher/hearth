package api_test

import (
	"encoding/json"
	"time"

	automationsapi "github.com/mholtzscher/hearth/internal/modules/automations/api"
)

// These read models belong to transport assertions, not production output.
// Tests inspect the flat wire fields without requiring output DTOs to decode.
type AutomationComparisonBody struct {
	Pointer  string          `json:"value_pointer"`
	Operator string          `json:"operator"`
	Operand  json.RawMessage `json:"operand"`
}

type AutomationTriggerBody struct {
	ID                  string                     `json:"id"`
	Kind                string                     `json:"kind"`
	EntityID            string                     `json:"entity_id"`
	Dispositions        []string                   `json:"dispositions"`
	PreviousComparisons []AutomationComparisonBody `json:"previous_comparisons"`
	Comparisons         []AutomationComparisonBody `json:"comparisons"`
	EventName           string                     `json:"event_name"`
	ForSeconds          *int64                     `json:"for_seconds"`
	Expression          string                     `json:"expression"`
}

type AutomationConditionBody struct {
	ID            string                    `json:"id"`
	Kind          string                    `json:"kind"`
	EntityID      string                    `json:"entity_id"`
	ValuePointer  *string                   `json:"value_pointer"`
	Operator      string                    `json:"operator"`
	Operand       json.RawMessage           `json:"operand"`
	MaxAgeSeconds *int64                    `json:"max_age_seconds"`
	TriggerIDs    []string                  `json:"trigger_ids"`
	Children      []AutomationConditionBody `json:"children"`
	Child         *AutomationConditionBody  `json:"child"`
}

type AutomationStepBody struct {
	ID         string                       `json:"id"`
	Kind       string                       `json:"kind"`
	EntityID   string                       `json:"entity_id"`
	Operation  string                       `json:"operation"`
	Parameters json.RawMessage              `json:"parameters"`
	Conditions *AutomationConditionBody     `json:"conditions"`
	Then       []AutomationStepBody         `json:"then"`
	Else       []AutomationStepBody         `json:"else"`
	Branches   []AutomationChooseBranchBody `json:"branches"`
	Default    []AutomationStepBody         `json:"default"`
}

type AutomationChooseBranchBody struct {
	ID         string                  `json:"id"`
	Conditions AutomationConditionBody `json:"conditions"`
	Steps      []AutomationStepBody    `json:"steps"`
}

type AutomationDefinitionBody struct {
	Name       string                   `json:"name"`
	Enabled    bool                     `json:"enabled"`
	Triggers   []AutomationTriggerBody  `json:"triggers"`
	Conditions *AutomationConditionBody `json:"conditions"`
	Steps      []AutomationStepBody     `json:"steps"`
}

type AutomationBody struct {
	ID         string                   `json:"id"`
	Revision   int64                    `json:"revision"`
	CreatedAt  time.Time                `json:"created_at"`
	UpdatedAt  time.Time                `json:"updated_at"`
	Definition AutomationDefinitionBody `json:"definition"`
}

type AutomationCollectionBody struct {
	Items      []AutomationBody `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

type AutomationStepAttemptBody struct {
	Position          int        `json:"position"`
	StepID            string     `json:"step_id"`
	Status            string     `json:"status"`
	VerifiedCommandID *string    `json:"verified_command_id"`
	FailureCode       *string    `json:"failure_code"`
	StartedAt         *time.Time `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
}

type AutomationConditionNodeResultBody struct {
	ID                string          `json:"id"`
	Kind              string          `json:"kind"`
	Result            string          `json:"result"`
	UnknownReason     *string         `json:"unknown_reason"`
	SelectedValue     json.RawMessage `json:"selected_value"`
	ObservationID     *string         `json:"observation_id"`
	ObservedAt        *time.Time      `json:"observed_at"`
	MatchedTriggerIDs *[]string       `json:"matched_trigger_ids"`
}

type AutomationConditionEvaluationBody struct {
	EvaluatedAt time.Time                           `json:"evaluated_at"`
	Result      string                              `json:"result"`
	Nodes       []AutomationConditionNodeResultBody `json:"nodes"`
}

type AutomationConditionDecisionBody struct {
	Mode            string                             `json:"mode"`
	BypassRequested bool                               `json:"bypass_requested"`
	Snapshot        *AutomationConditionBody           `json:"snapshot"`
	Evaluation      *AutomationConditionEvaluationBody `json:"evaluation"`
}

type AutomationBranchConditionEvaluationBody struct {
	BranchID   *string                           `json:"branch_id"`
	Evaluation AutomationConditionEvaluationBody `json:"evaluation"`
}

type AutomationBranchDecisionBody struct {
	Position         int                                       `json:"position"`
	StepID           string                                    `json:"step_id"`
	Kind             string                                    `json:"kind"`
	EvaluatedAt      time.Time                                 `json:"evaluated_at"`
	Outcome          string                                    `json:"outcome"`
	SelectedBranchID *string                                   `json:"selected_branch_id"`
	Evaluations      []AutomationBranchConditionEvaluationBody `json:"evaluations"`
	FailureCode      *string                                   `json:"failure_code"`
}

type AutomationRunBody struct {
	ID                string                            `json:"id"`
	AutomationID      string                            `json:"automation_id"`
	AutomationName    string                            `json:"automation_name"`
	Revision          int64                             `json:"revision"`
	Cause             automationsapi.AdmissionCauseBody `json:"cause"`
	MatchedTriggerIDs []string                          `json:"matched_trigger_ids"`
	Status            string                            `json:"status"`
	FailureCode       *string                           `json:"failure_code"`
	StartedAt         time.Time                         `json:"started_at"`
	CompletedAt       *time.Time                        `json:"completed_at"`
	Snapshot          AutomationDefinitionBody          `json:"snapshot"`
	ConditionDecision AutomationConditionDecisionBody   `json:"condition_decision"`
	Steps             []AutomationStepAttemptBody       `json:"steps"`
	BranchDecisions   []AutomationBranchDecisionBody    `json:"branch_decisions"`
}

type AutomationSkipBody struct {
	ID                string                            `json:"id"`
	AutomationID      string                            `json:"automation_id"`
	AutomationName    string                            `json:"automation_name"`
	Revision          int64                             `json:"revision"`
	Cause             automationsapi.AdmissionCauseBody `json:"cause"`
	MatchedTriggers   []AutomationTriggerBody           `json:"matched_triggers"`
	Reason            string                            `json:"reason"`
	ConditionDecision AutomationConditionDecisionBody   `json:"condition_decision"`
	SkippedAt         time.Time                         `json:"skipped_at"`
}

type AutomationHistorySummaryBody struct {
	ID              string                            `json:"id"`
	Kind            string                            `json:"kind"`
	AutomationID    string                            `json:"automation_id"`
	AutomationName  string                            `json:"automation_name"`
	Revision        int64                             `json:"revision"`
	RecordedAt      time.Time                         `json:"recorded_at"`
	Status          string                            `json:"status"`
	Reason          string                            `json:"reason"`
	Cause           automationsapi.AdmissionCauseBody `json:"cause"`
	ConditionMode   string                            `json:"condition_mode"`
	ConditionResult *string                           `json:"condition_result"`
	BypassRequested bool                              `json:"bypass_requested"`
}

type AutomationHistoryCollectionBody struct {
	Items      []AutomationHistorySummaryBody `json:"items"`
	NextCursor *string                        `json:"next_cursor"`
}

type AutomationHistoryEntryBody struct {
	Kind string              `json:"kind"`
	Run  *AutomationRunBody  `json:"run"`
	Skip *AutomationSkipBody `json:"skip"`
}
