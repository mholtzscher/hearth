//go:build integration || e2e

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"encoding/json"
	"time"
)

// conditionsNode is the recursive Condition tree shape the transport returns.
type conditionsNode struct {
	ID       string           `json:"id"`
	Kind     string           `json:"kind"`
	Children []conditionsNode `json:"children"`
	Child    *conditionsNode  `json:"child"`
}

// conditionsDecision mirrors the transport Condition decision. Evaluation is
// present only for the evaluated mode.
type conditionsDecision struct {
	Mode            string              `json:"mode"`
	BypassRequested bool                `json:"bypass_requested"`
	Snapshot        *conditionsNode     `json:"snapshot"`
	Evaluation      *conditionsEvalBody `json:"evaluation"`
}

type conditionsEvalBody struct {
	EvaluatedAt time.Time              `json:"evaluated_at"`
	Result      string                 `json:"result"`
	Nodes       []conditionsNodeResult `json:"nodes"`
}

type conditionsNodeResult struct {
	ID            string          `json:"id"`
	Result        string          `json:"result"`
	UnknownReason *string         `json:"unknown_reason"`
	SelectedValue json.RawMessage `json:"selected_value"`
	ObservationID *string         `json:"observation_id"`
	ObservedAt    *string         `json:"observed_at"`
}

// conditionsRun mirrors the history detail Run body this test inspects.
type conditionsRun struct {
	ID                string             `json:"id"`
	Cause             conditionsCause    `json:"cause"`
	Status            string             `json:"status"`
	MatchedTriggerIDs []string           `json:"matched_trigger_ids"`
	ConditionDecision conditionsDecision `json:"condition_decision"`
	Snapshot          struct {
		Conditions *conditionsNode `json:"conditions"`
	} `json:"snapshot"`
	Executions []branchingExecution `json:"executions"`
}

type conditionsCause struct {
	Kind     string           `json:"kind"`
	Fact     *json.RawMessage `json:"fact"`
	Evidence *struct {
		TriggerID string    `json:"trigger_id"`
		StartedAt time.Time `json:"started_at"`
		DueAt     time.Time `json:"due_at"`
	} `json:"evidence"`
}
