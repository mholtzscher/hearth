package api

import (
	"encoding/json"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// CreateAutomationInput leaves strict definition decoding to the canonical
// embedded schema, so unknown fields and typed-family violations are rejected.
type CreateAutomationInput struct {
	Body json.RawMessage
}

// ReplaceAutomationInput replaces one definition at a required current revision.
type ReplaceAutomationInput struct {
	AutomationID string          `path:"automation_id" doc:"Canonical Hearth Automation ID"`
	Body         json.RawMessage `                     doc:"expected_revision envelope and definition"`
}

// GetAutomationInput addresses one current definition.
type GetAutomationInput struct {
	AutomationID string `path:"automation_id" doc:"Canonical Hearth Automation ID"`
}

// DeleteAutomationInput requires optimistic revision concurrency.
type DeleteAutomationInput struct {
	AutomationID     string `path:"automation_id" doc:"Canonical Hearth Automation ID"`
	ExpectedRevision int64  `                                                          query:"expected_revision" required:"true" minimum:"1"`
}

// ListAutomationsInput uses an opaque ascending-ID continuation.
type ListAutomationsInput struct {
	Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}

// StartAutomationRunInput never accepts caller-supplied Command identities; an
// omitted body applies Conditions.
type StartAutomationRunInput struct {
	AutomationID string                  `path:"automation_id" doc:"Canonical Hearth Automation ID"`
	Body         *StartAutomationRunBody `                     doc:"Optional Condition bypass request"`
}

// ListHistoryInput pages newest-first history for one Automation.
type ListHistoryInput struct {
	AutomationID string `path:"automation_id" doc:"Canonical Hearth Automation ID"`
	Limit        int    `                                                          query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor       string `                                                          query:"cursor"`
}

// GetHistoryEntryInput addresses one retained Run or Skip.
type GetHistoryEntryInput struct {
	AutomationID string `path:"automation_id" doc:"Canonical Hearth Automation ID"`
	EntryID      string `path:"entry_id"      doc:"Canonical Hearth Automation Run or Skip ID"`
}

// AutomationComparisonBody is one typed Observation comparison.
type AutomationComparisonBody struct {
	Pointer  string          `json:"pointer"`
	Operator string          `json:"operator" enum:"eq,ne,lt,lte,gt,gte"`
	Operand  json.RawMessage `json:"operand"`
}

// AutomationTriggerBody mirrors the strict persisted Trigger shape with a
// discriminator and family-specific fields, never nullable placeholders.
type AutomationTriggerBody struct {
	ID           string                     `json:"id"`
	Kind         string                     `json:"kind"                   enum:"observation,entity_event"`
	EntityID     string                     `json:"entity_id"`
	Dispositions []string                   `json:"dispositions,omitempty"`
	Comparisons  []AutomationComparisonBody `json:"comparisons,omitempty"`
	EventName    string                     `json:"event_name,omitempty"`
}

// AutomationStepBody is one ordered Command with static parameters.
type AutomationStepBody struct {
	ID         string          `json:"id"`
	EntityID   string          `json:"entity_id"`
	Operation  string          `json:"operation"`
	Parameters json.RawMessage `json:"parameters"`
}

// AutomationDefinitionBody is the strict definition representation; an absent
// Conditions field preserves unconditional-after-Trigger behavior.
type AutomationDefinitionBody struct {
	Name       string                   `json:"name"`
	Enabled    bool                     `json:"enabled"`
	Triggers   []AutomationTriggerBody  `json:"triggers"`
	Conditions *AutomationConditionBody `json:"conditions,omitempty"`
	Steps      []AutomationStepBody     `json:"steps"`
}

// AutomationBody is one current definition with its revision and timestamps.
type AutomationBody struct {
	ID         string                   `json:"id"`
	Revision   int64                    `json:"revision"`
	CreatedAt  time.Time                `json:"created_at"`
	UpdatedAt  time.Time                `json:"updated_at"`
	Definition AutomationDefinitionBody `json:"definition"`
}

// AutomationOutput carries a creation Location or a normal definition read.
type AutomationOutput struct {
	Location string `header:"Location"`
	Body     AutomationBody
}

// AutomationCollectionBody always returns an array, including empty pages.
type AutomationCollectionBody struct {
	Items      []AutomationBody `json:"items"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}

// AutomationCollectionOutput is one ID-ascending keyset page.
type AutomationCollectionOutput struct {
	Body AutomationCollectionBody
}

// DeviceFactSummaryBody is the immutable Fact evidence retained in history.
type DeviceFactSummaryBody struct {
	FactID           string          `json:"fact_id"`
	Family           string          `json:"family"                      enum:"observation,entity_event"`
	EntityID         string          `json:"entity_id"`
	Variant          string          `json:"variant"`
	CausationID      string          `json:"causation_id"`
	ObservationValue json.RawMessage `json:"observation_value,omitempty"`
	EmittedAt        time.Time       `json:"emitted_at"`
}

// AutomationStepAttemptBody exposes only ownership-verified Command evidence.
type AutomationStepAttemptBody struct {
	Position          int        `json:"position"`
	StepID            string     `json:"step_id"`
	Status            string     `json:"status"                        enum:"not_attempted,running,satisfied,dispatched,failed,interrupted"`
	VerifiedCommandID *string    `json:"verified_command_id,omitempty"`
	FailureCode       *string    `json:"failure_code,omitempty"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

// AutomationRunBody exposes an immutable definition snapshot and current Step attempts.
type AutomationRunBody struct {
	ID                string                          `json:"id"`
	AutomationID      string                          `json:"automation_id"`
	AutomationName    string                          `json:"automation_name"`
	Revision          int64                           `json:"revision"`
	Source            string                          `json:"source"                 enum:"device_fact,manual"`
	Fact              *DeviceFactSummaryBody          `json:"fact,omitempty"`
	MatchedTriggerIDs []string                        `json:"matched_trigger_ids"`
	Status            string                          `json:"status"                 enum:"running,succeeded,failed,interrupted"`
	FailureCode       *string                         `json:"failure_code,omitempty"`
	StartedAt         time.Time                       `json:"started_at"`
	CompletedAt       *time.Time                      `json:"completed_at,omitempty"`
	Snapshot          AutomationDefinitionBody        `json:"snapshot"`
	ConditionDecision AutomationConditionDecisionBody `json:"condition_decision"`
	Steps             []AutomationStepAttemptBody     `json:"steps"`
}

// AutomationRunOutput uses 202 for admission with a history Location.
type AutomationRunOutput struct {
	Location string `header:"Location"`
	Body     AutomationRunBody
}

// AutomationSkipBody is one retained Skip with immutable matched Triggers;
// Source discriminates the manual and device-fact families.
type AutomationSkipBody struct {
	ID                string                          `json:"id"`
	AutomationID      string                          `json:"automation_id"`
	AutomationName    string                          `json:"automation_name"`
	Revision          int64                           `json:"revision"`
	Source            string                          `json:"source"             enum:"device_fact,manual"`
	Fact              *DeviceFactSummaryBody          `json:"fact,omitempty"`
	MatchedTriggers   []AutomationTriggerBody         `json:"matched_triggers"`
	Reason            string                          `json:"reason"             enum:"automation_busy,stale_fact,conditions_false,conditions_unknown"`
	ConditionDecision AutomationConditionDecisionBody `json:"condition_decision"`
	SkippedAt         time.Time                       `json:"skipped_at"`
}

// AutomationHistorySummaryBody is the lightweight history listing projection with
// admission provenance and the Condition decision summary.
type AutomationHistorySummaryBody struct {
	ID              string                 `json:"id"`
	Kind            string                 `json:"kind"                       enum:"run,skip"`
	AutomationID    string                 `json:"automation_id"`
	AutomationName  string                 `json:"automation_name"`
	Revision        int64                  `json:"revision"`
	RecordedAt      time.Time              `json:"recorded_at"`
	Status          string                 `json:"status,omitempty"`
	Reason          string                 `json:"reason,omitempty"`
	Source          string                 `json:"source"                     enum:"device_fact,manual"`
	ConditionMode   string                 `json:"condition_mode"             enum:"not_configured,not_evaluated,bypassed,evaluated"`
	ConditionResult *string                `json:"condition_result,omitempty" enum:"true,false,unknown"`
	BypassRequested bool                   `json:"bypass_requested"`
	Fact            *DeviceFactSummaryBody `json:"fact,omitempty"`
}

// AutomationHistoryCollectionBody is one newest-first history page.
type AutomationHistoryCollectionBody struct {
	Items      []AutomationHistorySummaryBody `json:"items"`
	NextCursor *string                        `json:"next_cursor,omitempty"`
}

// AutomationHistoryCollectionOutput is a history page.
type AutomationHistoryCollectionOutput struct {
	Body AutomationHistoryCollectionBody
}

// AutomationHistoryEntryBody is exactly one Run snapshot or Skip detail.
type AutomationHistoryEntryBody struct {
	Kind string              `json:"kind"           enum:"run,skip"`
	Run  *AutomationRunBody  `json:"run,omitempty"`
	Skip *AutomationSkipBody `json:"skip,omitempty"`
}

// AutomationHistoryEntryOutput is one history detail.
type AutomationHistoryEntryOutput struct {
	Body AutomationHistoryEntryBody
}

func automationDefinitionBody(definition automations.Definition) AutomationDefinitionBody {
	body := AutomationDefinitionBody{
		Name:       definition.Name,
		Enabled:    definition.Enabled,
		Triggers:   make([]AutomationTriggerBody, len(definition.Triggers)),
		Conditions: conditionBody(definition.Conditions),
		Steps:      make([]AutomationStepBody, len(definition.Steps)),
	}
	for index, trigger := range definition.Triggers {
		body.Triggers[index] = automationTriggerBody(trigger)
	}
	for index, step := range definition.Steps {
		body.Steps[index] = AutomationStepBody{
			ID:         string(step.ID),
			EntityID:   string(step.EntityID),
			Operation:  string(step.OperationName),
			Parameters: append(json.RawMessage(nil), step.Parameters...),
		}
	}
	return body
}

func automationTriggerBody(trigger automations.Trigger) AutomationTriggerBody {
	body := AutomationTriggerBody{ID: string(trigger.ID), Kind: string(trigger.Kind)}
	switch trigger.Kind {
	case automations.TriggerKindObservation:
		if trigger.Observation != nil {
			body.EntityID = string(trigger.Observation.EntityID)
			for _, disposition := range trigger.Observation.Dispositions {
				body.Dispositions = append(body.Dispositions, string(disposition))
			}
			for _, comparison := range trigger.Observation.Comparisons {
				body.Comparisons = append(body.Comparisons, AutomationComparisonBody{
					Pointer:  comparison.Pointer,
					Operator: string(comparison.Operator),
					Operand:  append(json.RawMessage(nil), comparison.Operand...),
				})
			}
		}
	case automations.TriggerKindEntityEvent:
		if trigger.EntityEvent != nil {
			body.EntityID = string(trigger.EntityEvent.EntityID)
			body.EventName = string(trigger.EntityEvent.EventName)
		}
	}
	return body
}

func automationBody(record automations.Record) AutomationBody {
	return AutomationBody{
		ID:         string(record.ID),
		Revision:   record.Revision,
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  record.UpdatedAt,
		Definition: automationDefinitionBody(record.Definition),
	}
}

func automationRunBody(run automations.Run) AutomationRunBody {
	body := AutomationRunBody{
		ID:                string(run.ID),
		AutomationID:      string(run.AutomationID),
		AutomationName:    run.AutomationName,
		Revision:          run.Revision,
		Source:            string(run.Source),
		MatchedTriggerIDs: make([]string, len(run.MatchedTriggerIDs)),
		Status:            string(run.Status),
		FailureCode:       run.FailureCode,
		StartedAt:         run.StartedAt,
		CompletedAt:       run.CompletedAt,
		Snapshot:          automationDefinitionBody(run.Snapshot),
		ConditionDecision: conditionDecisionBody(run.ConditionDecision),
		Steps:             make([]AutomationStepAttemptBody, len(run.Steps)),
	}
	for index, triggerID := range run.MatchedTriggerIDs {
		body.MatchedTriggerIDs[index] = string(triggerID)
	}
	if run.Fact != nil {
		fact := deviceFactSummaryBody(*run.Fact)
		body.Fact = &fact
	}
	for index, step := range run.Steps {
		body.Steps[index] = automationStepAttemptBody(step)
	}
	return body
}

func automationStepAttemptBody(step automations.StepAttempt) AutomationStepAttemptBody {
	body := AutomationStepAttemptBody{
		Position:    step.Position,
		StepID:      string(step.StepID),
		Status:      string(step.Status),
		FailureCode: step.FailureCode,
		StartedAt:   step.StartedAt,
		CompletedAt: step.CompletedAt,
	}
	if step.VerifiedCommandID != nil {
		verified := string(*step.VerifiedCommandID)
		body.VerifiedCommandID = &verified
	}
	return body
}

func automationSkipBody(skip automations.Skip) AutomationSkipBody {
	body := AutomationSkipBody{
		ID:                string(skip.ID),
		AutomationID:      string(skip.AutomationID),
		AutomationName:    skip.AutomationName,
		Revision:          skip.Revision,
		Source:            string(skip.Source),
		MatchedTriggers:   make([]AutomationTriggerBody, len(skip.MatchedTriggers)),
		Reason:            string(skip.Reason),
		ConditionDecision: conditionDecisionBody(skip.ConditionDecision),
		SkippedAt:         skip.SkippedAt,
	}
	if skip.Fact != nil {
		fact := deviceFactSummaryBody(*skip.Fact)
		body.Fact = &fact
	}
	for index, trigger := range skip.MatchedTriggers {
		body.MatchedTriggers[index] = automationTriggerBody(trigger)
	}
	return body
}

func deviceFactSummaryBody(summary automations.DeviceFactSummary) DeviceFactSummaryBody {
	body := DeviceFactSummaryBody{
		FactID:      string(summary.FactID),
		Family:      string(summary.Family),
		EntityID:    string(summary.EntityID),
		Variant:     summary.Variant,
		CausationID: summary.CausationID,
		EmittedAt:   summary.EmittedAt,
	}
	if summary.ObservationValue != nil {
		body.ObservationValue = append(json.RawMessage(nil), summary.ObservationValue...)
	}
	return body
}

func historySummaryBody(summary automations.HistorySummary) AutomationHistorySummaryBody {
	body := AutomationHistorySummaryBody{
		ID:              summary.ID,
		Kind:            string(summary.Kind),
		AutomationID:    string(summary.AutomationID),
		AutomationName:  summary.AutomationName,
		Revision:        summary.Revision,
		RecordedAt:      summary.RecordedAt,
		Source:          string(summary.Source),
		ConditionMode:   string(summary.ConditionMode),
		BypassRequested: summary.BypassRequested,
	}
	if summary.ConditionResult != nil {
		result := string(*summary.ConditionResult)
		body.ConditionResult = &result
	}
	if summary.Kind == automations.HistoryRun {
		body.Status = string(summary.Status)
	} else {
		body.Reason = string(summary.Reason)
	}
	if summary.Fact != nil {
		fact := deviceFactSummaryBody(*summary.Fact)
		body.Fact = &fact
	}
	return body
}

func historyEntryBody(entry automations.HistoryEntry) AutomationHistoryEntryBody {
	body := AutomationHistoryEntryBody{Kind: string(entry.Kind)}
	if entry.Run != nil {
		run := automationRunBody(*entry.Run)
		body.Run = &run
	}
	if entry.Skip != nil {
		skip := automationSkipBody(*entry.Skip)
		body.Skip = &skip
	}
	return body
}
