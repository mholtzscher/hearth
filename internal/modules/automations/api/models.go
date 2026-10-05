package api

import (
	"encoding/json"
	"fmt"
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

// AutomationDefinitionBody reuses the canonical concrete Step, Trigger, and
// Condition DTO mapping. Encoding validates recursive bounds before copying.
// HTTP and MCP publish the canonical schema rather than reflecting these bytes.
type AutomationDefinitionBody struct{ encoded json.RawMessage }

func (body AutomationDefinitionBody) MarshalJSON() ([]byte, error) {
	return body.encoded.MarshalJSON()
}

// AutomationMatchedTriggersBody uses the same concrete Trigger DTOs as definitions.
type AutomationMatchedTriggersBody struct{ encoded json.RawMessage }

func (body AutomationMatchedTriggersBody) MarshalJSON() ([]byte, error) {
	return body.encoded.MarshalJSON()
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

// HeldStateEvidenceBody records the Trigger and scheduled hold window.
type HeldStateEvidenceBody struct {
	TriggerID string    `json:"trigger_id"`
	StartedAt time.Time `json:"started_at"`
	DueAt     time.Time `json:"due_at"`
}

// AutomationRunFields are common to active and terminal Runs.
type AutomationRunFields struct {
	ID                string                          `json:"id"`
	AutomationID      string                          `json:"automation_id"`
	AutomationName    string                          `json:"automation_name"`
	Revision          int64                           `json:"revision"`
	Cause             AdmissionCauseBody              `json:"cause"`
	MatchedTriggerIDs []string                        `json:"matched_trigger_ids"`
	StartedAt         time.Time                       `json:"started_at"`
	Snapshot          AutomationDefinitionBody        `json:"snapshot"`
	ConditionDecision AutomationConditionDecisionBody `json:"condition_decision"`
	Steps             []AutomationStepAttemptBody     `json:"steps"`
	BranchDecisions   []AutomationBranchDecisionBody  `json:"branch_decisions"`
	Delays            []AutomationDelayExecutionBody  `json:"delays"`
}

// AutomationRunOutput uses 202 for admission with a history Location.
type AutomationRunOutput struct {
	Location string `header:"Location"`
	Body     AutomationRunBody
}

// AutomationSkipBody is one retained Skip with owned Cause and matched Triggers.
type AutomationSkipBody struct {
	ID             string `json:"id"`
	AutomationID   string `json:"automation_id"`
	AutomationName string `json:"automation_name"`
	Revision       int64  `json:"revision"`

	Cause           AdmissionCauseBody            `json:"cause"`
	MatchedTriggers AutomationMatchedTriggersBody `json:"matched_triggers"`
	// Reason identifies why a matching automation did not start a Run.
	Reason string `json:"reason" enum:"automation_busy,stale_fact,conditions_false,conditions_unknown"`
	// ConditionDecision records how admission conditions were evaluated.
	ConditionDecision AutomationConditionDecisionBody `json:"condition_decision"`
	SkippedAt         time.Time                       `json:"skipped_at"`
}

// AutomationHistorySummaryFields are the shared lightweight history projection.
type AutomationHistorySummaryFields struct {
	ID             string             `json:"id"`
	AutomationID   string             `json:"automation_id"`
	AutomationName string             `json:"automation_name"`
	Revision       int64              `json:"revision"`
	RecordedAt     time.Time          `json:"recorded_at"`
	Cause          AdmissionCauseBody `json:"cause"`
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

// AutomationHistoryEntryOutput is one history detail.
type AutomationHistoryEntryOutput struct {
	Body AutomationHistoryEntryBody
}

func automationDefinitionBody(definition automations.Definition) AutomationDefinitionBody {
	raw, err := automations.EncodeDefinition(definition)
	if err != nil {
		panic(fmt.Errorf("invalid retained definition: %w", err))
	}
	return AutomationDefinitionBody{encoded: raw}
}

func matchedTriggersBody(triggers []automations.Trigger) AutomationMatchedTriggersBody {
	raw, err := automations.EncodeMatchedTriggers(triggers)
	if err != nil {
		panic(fmt.Errorf("invalid retained matched Triggers: %w", err))
	}
	return AutomationMatchedTriggersBody{encoded: raw}
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
	body := AutomationRunFields{
		ID:                string(run.ID),
		AutomationID:      string(run.AutomationID),
		AutomationName:    run.AutomationName,
		Revision:          run.Revision,
		Cause:             admissionCauseBody(run.Cause),
		MatchedTriggerIDs: make([]string, len(run.MatchedTriggerIDs)),
		StartedAt:         run.StartedAt,
		Snapshot:          automationDefinitionBody(run.Snapshot),
		ConditionDecision: conditionDecisionBody(run.ConditionDecision),
		Steps:             make([]AutomationStepAttemptBody, len(run.Steps)),
		BranchDecisions:   make([]AutomationBranchDecisionBody, len(run.BranchDecisions)),
		Delays:            make([]AutomationDelayExecutionBody, len(run.Delays)),
	}
	for index, triggerID := range run.MatchedTriggerIDs {
		body.MatchedTriggerIDs[index] = string(triggerID)
	}
	for index, step := range run.Steps {
		body.Steps[index] = automationStepAttemptBody(step)
	}
	for index, decision := range run.BranchDecisions {
		body.BranchDecisions[index] = branchDecisionBody(decision)
	}
	for index, delay := range run.Delays {
		body.Delays[index] = delayExecutionBody(delay)
	}
	return runLifecycleBody(body, run.State)
}

func automationSkipBody(skip automations.Skip) AutomationSkipBody {
	body := AutomationSkipBody{
		ID:                string(skip.ID),
		AutomationID:      string(skip.AutomationID),
		AutomationName:    skip.AutomationName,
		Revision:          skip.Revision,
		Cause:             admissionCauseBody(skip.Cause),
		MatchedTriggers:   matchedTriggersBody(skip.MatchedTriggers),
		Reason:            string(skip.Reason),
		ConditionDecision: conditionDecisionBody(skip.ConditionDecision),
		SkippedAt:         skip.SkippedAt,
	}
	return body
}

func historySummaryBody(summary automations.HistorySummary) AutomationHistorySummaryBody {
	body := AutomationHistorySummaryFields{
		ID:             summary.ID,
		AutomationID:   string(summary.AutomationID),
		AutomationName: summary.AutomationName,
		Revision:       summary.Revision,
		RecordedAt:     summary.RecordedAt,
		Cause:          admissionCauseBody(summary.Cause),
	}
	return historySummaryVariantBody(body, summary.Body, summary.ConditionSummary)
}

func heldStateEvidenceBody(evidence automations.HeldStateEvidence) HeldStateEvidenceBody {
	return HeldStateEvidenceBody{
		TriggerID: string(evidence.TriggerID), StartedAt: evidence.StartedAt, DueAt: evidence.DueAt,
	}
}

func historyEntryBody(entry automations.HistoryEntry) AutomationHistoryEntryBody {
	switch entry := entry.(type) {
	case automations.Run:
		return AutomationHistoryEntryBody{
			Variant: RunHistoryEntryBody{Kind: string(automations.HistoryRun), Run: automationRunBody(entry)},
		}
	case automations.Skip:
		return AutomationHistoryEntryBody{
			Variant: SkipHistoryEntryBody{Kind: string(automations.HistorySkip), Skip: automationSkipBody(entry)},
		}
	default:
		panic("invalid retained history entry")
	}
}
