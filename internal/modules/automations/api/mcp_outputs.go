package api

import (
	"encoding/json"
	"time"
)

// MCP output DTOs mirror the Huma read bodies below one deliberate difference:
// every open JSON leaf is typed any instead of json.RawMessage.
//
// The SDK derives a tool's output schema by reflection and validates the
// marshaled body against it. A json.RawMessage leaf derives the schema for
// []byte (an array of byte-sized integers) and would reject the very payloads
// these tools return, so a leaf is typed any, which derives an unconstrained
// schema while json.Marshal still emits the exact original bytes.
//
// A Condition tree is recursive, and the schema deriver rejects a recursive Go
// type outright, so the definition and decision fields that hold one carry the
// existing Huma Condition body behind any. The published JSON shape is
// unchanged; only the derived schema stops descending there.
//
// The SDK's typed output path also re-marshals every object-rooted body through
// map[string]any before it reaches a client, which turns each number into a
// float64. That limit is an SDK property, not an omission here: a definition
// number above 2^53 is preserved exactly on the way in (see
// mcpDefinitionArgument), through persistence, and out of a hearth:// resource
// read, but a typed tool result rounds it before the client sees it.

type mcpAutomationComparisonBody struct {
	ValuePointer string `json:"value_pointer"`
	Operator     string `json:"operator"`
	Operand      any    `json:"operand"`
}

type mcpAutomationTriggerBody struct {
	ID           string                        `json:"id"`
	Kind         string                        `json:"kind"`
	EntityID     string                        `json:"entity_id"`
	Dispositions []string                      `json:"dispositions,omitempty"`
	Comparisons  []mcpAutomationComparisonBody `json:"comparisons,omitempty"`
	EventName    string                        `json:"event_name,omitempty"`
}

type mcpAutomationStepBody struct {
	ID         string `json:"id"`
	EntityID   string `json:"entity_id"`
	Operation  string `json:"operation"`
	Parameters any    `json:"parameters"`
}

type mcpAutomationDefinitionBody struct {
	Name       string                     `json:"name"`
	Enabled    bool                       `json:"enabled"`
	Triggers   []mcpAutomationTriggerBody `json:"triggers"`
	Conditions any                        `json:"conditions,omitempty"`
	Steps      []mcpAutomationStepBody    `json:"steps"`
}

type mcpAutomationBody struct {
	ID         string                      `json:"id"`
	Revision   int64                       `json:"revision"`
	CreatedAt  time.Time                   `json:"created_at"`
	UpdatedAt  time.Time                   `json:"updated_at"`
	Definition mcpAutomationDefinitionBody `json:"definition"`
}

type mcpAutomationCollectionBody struct {
	Items      []mcpAutomationBody `json:"items"`
	NextCursor *string             `json:"next_cursor,omitempty"`
}

type mcpDeviceFactSummaryBody struct {
	FactID           string    `json:"fact_id"`
	Family           string    `json:"family"`
	EntityID         string    `json:"entity_id"`
	Variant          string    `json:"variant"`
	CausationID      string    `json:"causation_id"`
	ObservationValue any       `json:"observation_value,omitempty"`
	EmittedAt        time.Time `json:"emitted_at"`
}

type mcpAutomationStepAttemptBody struct {
	Position          int        `json:"position"`
	StepID            string     `json:"step_id"`
	Status            string     `json:"status"`
	VerifiedCommandID *string    `json:"verified_command_id,omitempty"`
	FailureCode       *string    `json:"failure_code,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

type mcpAutomationConditionNodeResultBody struct {
	ID            string     `json:"id"`
	Result        string     `json:"result"`
	UnknownReason *string    `json:"unknown_reason,omitempty"`
	SelectedValue any        `json:"selected_value,omitempty"`
	ObservationID *string    `json:"observation_id,omitempty"`
	ObservedAt    *time.Time `json:"observed_at,omitempty"`
}

type mcpAutomationConditionEvaluationBody struct {
	EvaluatedAt time.Time                              `json:"evaluated_at"`
	Result      string                                 `json:"result"`
	Nodes       []mcpAutomationConditionNodeResultBody `json:"nodes"`
}

type mcpAutomationConditionDecisionBody struct {
	Mode            string                                `json:"mode"`
	BypassRequested bool                                  `json:"bypass_requested"`
	Snapshot        any                                   `json:"snapshot,omitempty"`
	Evaluation      *mcpAutomationConditionEvaluationBody `json:"evaluation,omitempty"`
}

type mcpAutomationRunBody struct {
	ID                string                             `json:"id"`
	AutomationID      string                             `json:"automation_id"`
	AutomationName    string                             `json:"automation_name"`
	Revision          int64                              `json:"revision"`
	Source            string                             `json:"source"`
	Fact              *mcpDeviceFactSummaryBody          `json:"fact,omitempty"`
	MatchedTriggerIDs []string                           `json:"matched_trigger_ids"`
	Status            string                             `json:"status"`
	FailureCode       *string                            `json:"failure_code,omitempty"`
	StartedAt         time.Time                          `json:"started_at"`
	CompletedAt       *time.Time                         `json:"completed_at,omitempty"`
	Snapshot          mcpAutomationDefinitionBody        `json:"snapshot"`
	ConditionDecision mcpAutomationConditionDecisionBody `json:"condition_decision"`
	Steps             []mcpAutomationStepAttemptBody     `json:"steps"`
}

type mcpAutomationSkipBody struct {
	ID                string                             `json:"id"`
	AutomationID      string                             `json:"automation_id"`
	AutomationName    string                             `json:"automation_name"`
	Revision          int64                              `json:"revision"`
	Source            string                             `json:"source"`
	Fact              *mcpDeviceFactSummaryBody          `json:"fact,omitempty"`
	MatchedTriggers   []mcpAutomationTriggerBody         `json:"matched_triggers"`
	Reason            string                             `json:"reason"`
	ConditionDecision mcpAutomationConditionDecisionBody `json:"condition_decision"`
	SkippedAt         time.Time                          `json:"skipped_at"`
}

type mcpAutomationHistorySummaryBody struct {
	ID              string                    `json:"id"`
	Kind            string                    `json:"kind"`
	AutomationID    string                    `json:"automation_id"`
	AutomationName  string                    `json:"automation_name"`
	Revision        int64                     `json:"revision"`
	RecordedAt      time.Time                 `json:"recorded_at"`
	Status          string                    `json:"status,omitempty"`
	Reason          string                    `json:"reason,omitempty"`
	Source          string                    `json:"source"`
	ConditionMode   string                    `json:"condition_mode"`
	ConditionResult *string                   `json:"condition_result,omitempty"`
	BypassRequested bool                      `json:"bypass_requested"`
	Fact            *mcpDeviceFactSummaryBody `json:"fact,omitempty"`
}

type mcpAutomationHistoryCollectionBody struct {
	Items      []mcpAutomationHistorySummaryBody `json:"items"`
	NextCursor *string                           `json:"next_cursor,omitempty"`
}

type mcpAutomationHistoryEntryBody struct {
	Kind string                 `json:"kind"`
	Run  *mcpAutomationRunBody  `json:"run,omitempty"`
	Skip *mcpAutomationSkipBody `json:"skip,omitempty"`
}

// mcpExactJSON returns one open JSON leaf as the value an MCP output marshals.
// The returned [json.RawMessage] marshals verbatim, and a nil value keeps an
// optional member absent, so a selected JSON null stays distinct from an absent
// member exactly as the Huma body does.
func mcpExactJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// mcpConditionTree returns one recursive Huma Condition subtree behind any, so
// the derived output schema stops descending instead of rejecting the recursion.
func mcpConditionTree(condition *AutomationConditionBody) any {
	if condition == nil {
		return nil
	}
	return condition
}

func mcpComparisonOutput(body AutomationComparisonBody) mcpAutomationComparisonBody {
	return mcpAutomationComparisonBody{
		ValuePointer: body.Pointer, Operator: body.Operator, Operand: mcpExactJSON(body.Operand),
	}
}

func mcpTriggerOutput(body AutomationTriggerBody) mcpAutomationTriggerBody {
	output := mcpAutomationTriggerBody{
		ID: body.ID, Kind: body.Kind, EntityID: body.EntityID,
		Dispositions: body.Dispositions, EventName: body.EventName,
	}
	if len(body.Comparisons) > 0 {
		output.Comparisons = make([]mcpAutomationComparisonBody, len(body.Comparisons))
		for index, comparison := range body.Comparisons {
			output.Comparisons[index] = mcpComparisonOutput(comparison)
		}
	}
	return output
}

func mcpStepOutput(body AutomationStepBody) mcpAutomationStepBody {
	return mcpAutomationStepBody{
		ID: body.ID, EntityID: body.EntityID, Operation: body.Operation,
		Parameters: mcpExactJSON(body.Parameters),
	}
}

func mcpDefinitionOutput(body AutomationDefinitionBody) mcpAutomationDefinitionBody {
	output := mcpAutomationDefinitionBody{
		Name: body.Name, Enabled: body.Enabled, Conditions: mcpConditionTree(body.Conditions),
		Triggers: make([]mcpAutomationTriggerBody, len(body.Triggers)),
		Steps:    make([]mcpAutomationStepBody, len(body.Steps)),
	}
	for index, trigger := range body.Triggers {
		output.Triggers[index] = mcpTriggerOutput(trigger)
	}
	for index, step := range body.Steps {
		output.Steps[index] = mcpStepOutput(step)
	}
	return output
}

func mcpAutomationOutput(body AutomationBody) mcpAutomationBody {
	return mcpAutomationBody{
		ID: body.ID, Revision: body.Revision, CreatedAt: body.CreatedAt,
		UpdatedAt: body.UpdatedAt, Definition: mcpDefinitionOutput(body.Definition),
	}
}

func mcpCollectionOutput(body AutomationCollectionBody) mcpAutomationCollectionBody {
	output := mcpAutomationCollectionBody{
		Items: make([]mcpAutomationBody, len(body.Items)), NextCursor: body.NextCursor,
	}
	for index, item := range body.Items {
		output.Items[index] = mcpAutomationOutput(item)
	}
	return output
}

func mcpFactOutput(body DeviceFactSummaryBody) mcpDeviceFactSummaryBody {
	return mcpDeviceFactSummaryBody{
		FactID: body.FactID, Family: body.Family, EntityID: body.EntityID,
		Variant: body.Variant, CausationID: body.CausationID,
		ObservationValue: mcpExactJSON(body.ObservationValue), EmittedAt: body.EmittedAt,
	}
}

func mcpStepAttemptOutput(body AutomationStepAttemptBody) mcpAutomationStepAttemptBody {
	// The two shapes have identical fields; only the Huma body's extra enum tags
	// differ, and conversion ignores struct tags.
	return mcpAutomationStepAttemptBody(body)
}

func mcpNodeResultOutput(
	body AutomationConditionNodeResultBody,
) mcpAutomationConditionNodeResultBody {
	return mcpAutomationConditionNodeResultBody{
		ID: body.ID, Result: body.Result, UnknownReason: body.UnknownReason,
		SelectedValue: mcpExactJSON(body.SelectedValue), ObservationID: body.ObservationID,
		ObservedAt: body.ObservedAt,
	}
}

func mcpEvaluationOutput(
	body AutomationConditionEvaluationBody,
) mcpAutomationConditionEvaluationBody {
	output := mcpAutomationConditionEvaluationBody{
		EvaluatedAt: body.EvaluatedAt, Result: body.Result,
		Nodes: make([]mcpAutomationConditionNodeResultBody, len(body.Nodes)),
	}
	for index, node := range body.Nodes {
		output.Nodes[index] = mcpNodeResultOutput(node)
	}
	return output
}

func mcpConditionDecisionOutput(
	body AutomationConditionDecisionBody,
) mcpAutomationConditionDecisionBody {
	output := mcpAutomationConditionDecisionBody{
		Mode: body.Mode, BypassRequested: body.BypassRequested,
		Snapshot: mcpConditionTree(body.Snapshot),
	}
	if body.Evaluation != nil {
		evaluation := mcpEvaluationOutput(*body.Evaluation)
		output.Evaluation = &evaluation
	}
	return output
}

func mcpRunOutput(body AutomationRunBody) mcpAutomationRunBody {
	output := mcpAutomationRunBody{
		ID: body.ID, AutomationID: body.AutomationID, AutomationName: body.AutomationName,
		Revision: body.Revision, Source: body.Source, MatchedTriggerIDs: body.MatchedTriggerIDs,
		Status: body.Status, FailureCode: body.FailureCode, StartedAt: body.StartedAt,
		CompletedAt: body.CompletedAt, Snapshot: mcpDefinitionOutput(body.Snapshot),
		ConditionDecision: mcpConditionDecisionOutput(body.ConditionDecision),
		Steps:             make([]mcpAutomationStepAttemptBody, len(body.Steps)),
	}
	if body.Fact != nil {
		fact := mcpFactOutput(*body.Fact)
		output.Fact = &fact
	}
	for index, step := range body.Steps {
		output.Steps[index] = mcpStepAttemptOutput(step)
	}
	return output
}

func mcpSkipOutput(body AutomationSkipBody) mcpAutomationSkipBody {
	output := mcpAutomationSkipBody{
		ID: body.ID, AutomationID: body.AutomationID, AutomationName: body.AutomationName,
		Revision: body.Revision, Source: body.Source, Reason: body.Reason,
		ConditionDecision: mcpConditionDecisionOutput(body.ConditionDecision),
		SkippedAt:         body.SkippedAt,
		MatchedTriggers:   make([]mcpAutomationTriggerBody, len(body.MatchedTriggers)),
	}
	if body.Fact != nil {
		fact := mcpFactOutput(*body.Fact)
		output.Fact = &fact
	}
	for index, trigger := range body.MatchedTriggers {
		output.MatchedTriggers[index] = mcpTriggerOutput(trigger)
	}
	return output
}

func mcpHistorySummaryOutput(
	body AutomationHistorySummaryBody,
) mcpAutomationHistorySummaryBody {
	output := mcpAutomationHistorySummaryBody{
		ID: body.ID, Kind: body.Kind, AutomationID: body.AutomationID,
		AutomationName: body.AutomationName, Revision: body.Revision, RecordedAt: body.RecordedAt,
		Status: body.Status, Reason: body.Reason, Source: body.Source,
		ConditionMode: body.ConditionMode, ConditionResult: body.ConditionResult,
		BypassRequested: body.BypassRequested,
	}
	if body.Fact != nil {
		fact := mcpFactOutput(*body.Fact)
		output.Fact = &fact
	}
	return output
}

func mcpHistoryCollectionOutput(
	body AutomationHistoryCollectionBody,
) mcpAutomationHistoryCollectionBody {
	output := mcpAutomationHistoryCollectionBody{
		Items: make([]mcpAutomationHistorySummaryBody, len(body.Items)), NextCursor: body.NextCursor,
	}
	for index, item := range body.Items {
		output.Items[index] = mcpHistorySummaryOutput(item)
	}
	return output
}

func mcpHistoryEntryOutput(body AutomationHistoryEntryBody) mcpAutomationHistoryEntryBody {
	output := mcpAutomationHistoryEntryBody{Kind: body.Kind}
	if body.Run != nil {
		run := mcpRunOutput(*body.Run)
		output.Run = &run
	}
	if body.Skip != nil {
		skip := mcpSkipOutput(*body.Skip)
		output.Skip = &skip
	}
	return output
}
