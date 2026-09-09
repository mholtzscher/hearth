package api

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// CreateAutomationInput leaves definition validation to the canonical codec.
type CreateAutomationInput struct{ Body json.RawMessage }

// UpdateAutomationInput replaces the definition at a required positive revision.
type UpdateAutomationInput struct {
	AutomationID     string `path:"automation_id"`
	ExpectedRevision int64  `                     query:"expected_revision" required:"true" minimum:"1"`
	Body             json.RawMessage
}

// GetAutomationInput addresses a live definition.
type GetAutomationInput struct {
	AutomationID string `path:"automation_id"`
}

// DeleteAutomationInput requires optimistic revision concurrency.
type DeleteAutomationInput struct {
	AutomationID     string `path:"automation_id"`
	ExpectedRevision int64  `                     query:"expected_revision" required:"true" minimum:"1"`
}

// ListAutomationsInput uses an opaque ascending ID continuation.
type ListAutomationsInput struct {
	Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}

// StartAutomationRunInput never accepts caller command identities or parameters.
type StartAutomationRunInput struct {
	AutomationID   string `path:"automation_id"`
	IdempotencyKey string `                     header:"Idempotency-Key" required:"true"`
}

// GetAutomationRunInput addresses retained history.
type GetAutomationRunInput struct {
	RunID string `path:"run_id"`
}

// ListAutomationRunsInput binds continuation to its optional historical filter.
type ListAutomationRunsInput struct {
	AutomationID string `query:"automation_id"`
	Limit        int    `query:"limit"         default:"50" minimum:"1" maximum:"200"`
	Cursor       string `query:"cursor"`
}

// AutomationTriggerBody preserves author identity and order.
type AutomationTriggerBody struct {
	ID         automations.AutomationTriggerID `json:"id"`
	Kind       string                          `json:"kind"       enum:"cron"`
	Expression string                          `json:"expression"`
}

// AutomationStepBody contains only public operation inputs.
type AutomationStepBody struct {
	EntityID      devices.EntityID      `json:"entity_id"`
	OperationName devices.OperationName `json:"operation_name"`
	Parameters    json.RawMessage       `json:"parameters"`
}

// AutomationDefinitionBody represents the canonical DSL without transport metadata.
type AutomationDefinitionBody struct {
	Name     string                  `json:"name"`
	Enabled  bool                    `json:"enabled"`
	Triggers []AutomationTriggerBody `json:"triggers"`
	Steps    []AutomationStepBody    `json:"steps"`
}

// AutomationBody adds revision and timestamps to a normalized definition.
type AutomationBody struct {
	AutomationDefinitionBody

	ID        automations.AutomationID `json:"id"`
	Revision  int64                    `json:"revision"`
	CreatedAt time.Time                `json:"created_at"`
	UpdatedAt time.Time                `json:"updated_at"`
}

// AutomationOutput carries a creation Location or a normal definition read.
type AutomationOutput struct {
	Location string `header:"Location"`
	Body     AutomationBody
}

// AutomationListOutput always returns an array, including empty pages.
type AutomationListOutput struct {
	Body struct {
		Items      []AutomationBody `json:"items"`
		NextCursor string           `json:"next_cursor,omitempty"`
	}
}

// AutomationRunSnapshotBody retains the complete admitted definition and timezone.
type AutomationRunSnapshotBody struct {
	AutomationID automations.AutomationID `json:"automation_id"`
	Revision     int64                    `json:"revision"`
	Definition   AutomationDefinitionBody `json:"definition"`
	Timezone     string                   `json:"timezone"`
}

// AutomationRunStepBody excludes the internal reserved correlation marker.
type AutomationRunStepBody struct {
	Index             int                              `json:"index"`
	Definition        AutomationStepBody               `json:"definition"`
	Status            automations.AutomationStepStatus `json:"status"                        enum:"pending,running,satisfied,dispatched,failed,not_attempted,interrupted"`
	ReservedCommandID *devices.CommandID               `json:"reserved_command_id,omitempty"`
	CommandID         *devices.CommandID               `json:"command_id,omitempty"`
	CommandStatus     *devices.CommandStatus           `json:"command_status,omitempty"`
	Outcome           *devices.OutcomeKind             `json:"outcome,omitempty"`
	FailureCode       *string                          `json:"failure_code,omitempty"`
	StartedAt         *time.Time                       `json:"started_at,omitempty"`
	CompletedAt       *time.Time                       `json:"completed_at,omitempty"`
}

// AutomationRunBody exposes immutable inputs and ownership-verified evidence only.
type AutomationRunBody struct {
	ID                automations.AutomationRunID       `json:"id"`
	Snapshot          AutomationRunSnapshotBody         `json:"snapshot"`
	Source            automations.AutomationRunSource   `json:"source"                 enum:"manual,scheduled"`
	ScheduledAt       *time.Time                        `json:"scheduled_at,omitempty"`
	MatchedTriggerIDs []automations.AutomationTriggerID `json:"matched_trigger_ids"`
	Status            automations.AutomationRunStatus   `json:"status"                 enum:"running,succeeded,failed,interrupted"`
	StartedAt         time.Time                         `json:"started_at"`
	CompletedAt       *time.Time                        `json:"completed_at,omitempty"`
	FailureCode       *string                           `json:"failure_code,omitempty"`
	Steps             []AutomationRunStepBody           `json:"steps"`
}

// AutomationRunSummaryBody deliberately omits all step parameters and keys.
type AutomationRunSummaryBody struct {
	ID                automations.AutomationRunID       `json:"id"`
	AutomationID      automations.AutomationID          `json:"automation_id"`
	Name              string                            `json:"name"`
	Revision          int64                             `json:"revision"`
	Source            automations.AutomationRunSource   `json:"source"                 enum:"manual,scheduled"`
	ScheduledAt       *time.Time                        `json:"scheduled_at,omitempty"`
	MatchedTriggerIDs []automations.AutomationTriggerID `json:"matched_trigger_ids"`
	Status            automations.AutomationRunStatus   `json:"status"                 enum:"running,succeeded,failed,interrupted"`
	StartedAt         time.Time                         `json:"started_at"`
	CompletedAt       *time.Time                        `json:"completed_at,omitempty"`
	FailureCode       *string                           `json:"failure_code,omitempty"`
}

// AutomationRunOutput uses 202 for admission and 200 for retained-key reuse.
type AutomationRunOutput struct {
	Status   int
	Location string `header:"Location"`
	Body     AutomationRunBody
}

// AutomationRunListOutput is a newest-first summary page.
type AutomationRunListOutput struct {
	Body struct {
		Items      []AutomationRunSummaryBody `json:"items"`
		NextCursor string                     `json:"next_cursor,omitempty"`
	}
}

func automationDefinitionBody(definition automations.AutomationDefinition) AutomationDefinitionBody {
	body := AutomationDefinitionBody{
		Name:    definition.Name,
		Enabled: definition.Enabled,
		Triggers: make(
			[]AutomationTriggerBody,
			len(definition.Triggers),
		),
		Steps: make([]AutomationStepBody, len(definition.Steps)),
	}
	for i, trigger := range definition.Triggers {
		body.Triggers[i] = AutomationTriggerBody(trigger)
	}
	for i, step := range definition.Steps {
		body.Steps[i] = automationStepBody(step)
	}
	return body
}
func automationStepBody(step automations.AutomationStep) AutomationStepBody {
	return AutomationStepBody{
		EntityID:      step.EntityID,
		OperationName: step.OperationName,
		Parameters:    bytes.Clone(step.Parameters),
	}
}
func automationBody(record automations.AutomationRecord) AutomationBody {
	return AutomationBody{AutomationDefinitionBody: automationDefinitionBody(record.Definition), ID: record.ID,
		Revision: record.Revision, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}
func automationRunBody(run automations.AutomationRunRecord) AutomationRunBody {
	body := AutomationRunBody{
		ID: run.ID,
		Snapshot: AutomationRunSnapshotBody{
			AutomationID: run.Snapshot.AutomationID, Revision: run.Snapshot.Revision,
			Definition: automationDefinitionBody(run.Snapshot.Definition), Timezone: run.Snapshot.Timezone},
		Source:            run.Source,
		ScheduledAt:       run.ScheduledAt,
		MatchedTriggerIDs: append([]automations.AutomationTriggerID{}, run.MatchedTriggerIDs...),
		Status:            run.Status,
		StartedAt:         run.StartedAt,
		CompletedAt:       run.CompletedAt,
		FailureCode:       run.FailureCode,
		Steps:             make([]AutomationRunStepBody, len(run.Steps)),
	}
	for i, step := range run.Steps {
		body.Steps[i] = AutomationRunStepBody{
			Index:             step.Index,
			Definition:        automationStepBody(step.Definition),
			Status:            step.Status,
			ReservedCommandID: step.ReservedCommandID,
			CommandID:         step.CommandID,
			CommandStatus:     step.CommandStatus,
			Outcome:           step.Outcome,
			FailureCode:       step.FailureCode,
			StartedAt:         step.StartedAt,
			CompletedAt:       step.CompletedAt,
		}
	}
	return body
}
func automationRunSummaryBody(run automations.AutomationRunRecord) AutomationRunSummaryBody {
	return AutomationRunSummaryBody{
		ID:                run.ID,
		AutomationID:      run.Snapshot.AutomationID,
		Name:              run.Snapshot.Definition.Name,
		Revision:          run.Snapshot.Revision,
		Source:            run.Source,
		ScheduledAt:       run.ScheduledAt,
		MatchedTriggerIDs: append([]automations.AutomationTriggerID{}, run.MatchedTriggerIDs...),
		Status:            run.Status,
		StartedAt:         run.StartedAt,
		CompletedAt:       run.CompletedAt,
		FailureCode:       run.FailureCode,
	}
}
