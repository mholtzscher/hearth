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
	ID   automations.AutomationTriggerID `json:"id"`
	Kind string                          `json:"kind" enum:"cron"`
	// Expression is one five-field household cron rule, interpreted in the
	// response household_timezone. Syntactically valid but impossible
	// schedules such as 0 0 31 2 * are stored and never match, so
	// validation does not imply eventual execution.
	Expression string `json:"expression" doc:"Cron expression; impossible dates such as 0 0 31 2 * never match."`
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

// AutomationBody adds revision, timestamps, and the read-only household
// timezone used to interpret every cron expression to a normalized definition.
type AutomationBody struct {
	AutomationDefinitionBody

	ID        automations.AutomationID `json:"id"`
	Revision  int64                    `json:"revision"`
	CreatedAt time.Time                `json:"created_at"`
	UpdatedAt time.Time                `json:"updated_at"`
	// HouseholdTimezone is read-only process configuration, not an editable
	// per-Automation field. Run and Occurrence snapshots retain the timezone
	// used at admission; this value reflects the currently running process.
	HouseholdTimezone string `json:"household_timezone" doc:"Household timezone. Read-only process configuration."`
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
func automationBody(record automations.AutomationRecord, householdTimezone string) AutomationBody {
	return AutomationBody{AutomationDefinitionBody: automationDefinitionBody(record.Definition), ID: record.ID,
		Revision: record.Revision, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		HouseholdTimezone: householdTimezone}
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

// ListAutomationOccurrencesInput pages started and skipped schedule matches
// newest-first, optionally filtered to one Automation's history.
type ListAutomationOccurrencesInput struct {
	AutomationID string `query:"automation_id"`
	Limit        int    `query:"limit"         default:"50" minimum:"1" maximum:"200"`
	Cursor       string `query:"cursor"`
}

// ListAutomationScheduleGapsInput pages unevaluated intervals newest-first.
type ListAutomationScheduleGapsInput struct {
	Limit  int    `query:"limit"  default:"50" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}

// AutomationOccurrenceBody exposes one Automation's collected Trigger matches
// at one UTC minute. MatchedTriggers retains the complete matching Trigger
// snapshots in definition array order; it is never reconstructed from the
// current definition, so it survives expression edits, reorder, and deletion.
type AutomationOccurrenceBody struct {
	AutomationID automations.AutomationID `json:"automation_id"`
	Revision     int64                    `json:"revision"`
	// Name is the retained diagnostic name, even after definition deletion.
	Name            string                  `json:"name"`
	MatchedTriggers []AutomationTriggerBody `json:"matched_triggers"`
	// Timezone is the household zone used at admission, not the current process zone.
	Timezone    string                                 `json:"timezone"`
	ScheduledAt time.Time                              `json:"scheduled_at"`
	EvaluatedAt time.Time                              `json:"evaluated_at"`
	Status      automations.AutomationOccurrenceStatus `json:"status"       enum:"started,skipped"`
	// RunID is present only when the match was admitted; skipped matches
	// carry SkipReason instead and never both.
	RunID      *automations.AutomationRunID `json:"run_id,omitempty"`
	SkipReason *string                      `json:"skip_reason,omitempty"`
}

// AutomationScheduleGapBody explains one unevaluated UTC minute interval.
// Gaps never claim the number or identity of missed Automations.
type AutomationScheduleGapBody struct {
	ID               string    `json:"id"`
	FromExclusive    time.Time `json:"from_exclusive"`
	ThroughInclusive time.Time `json:"through_inclusive"`
	RecordedAt       time.Time `json:"recorded_at"`
	Reason           string    `json:"reason"            enum:"core_restart,clock_or_processing_gap"`
}

// AutomationOccurrenceListOutput is a newest-first occurrence page.
type AutomationOccurrenceListOutput struct {
	Body struct {
		Items      []AutomationOccurrenceBody `json:"items"`
		NextCursor string                     `json:"next_cursor,omitempty"`
	}
}

// AutomationScheduleGapListOutput is a newest-first gap page.
type AutomationScheduleGapListOutput struct {
	Body struct {
		Items      []AutomationScheduleGapBody `json:"items"`
		NextCursor string                      `json:"next_cursor,omitempty"`
	}
}

func automationOccurrenceBody(occurrence automations.AutomationOccurrence) AutomationOccurrenceBody {
	body := AutomationOccurrenceBody{
		AutomationID: occurrence.AutomationID,
		Revision:     occurrence.Revision,
		Name:         occurrence.Name,
		MatchedTriggers: make(
			[]AutomationTriggerBody,
			len(occurrence.MatchedTriggers),
		),
		Timezone:    occurrence.Timezone,
		ScheduledAt: occurrence.ScheduledAt,
		EvaluatedAt: occurrence.EvaluatedAt,
		Status:      occurrence.Status,
		RunID:       occurrence.RunID,
		SkipReason:  occurrence.SkipReason,
	}
	for i, trigger := range occurrence.MatchedTriggers {
		body.MatchedTriggers[i] = AutomationTriggerBody(trigger)
	}
	return body
}
func automationScheduleGapBody(gap automations.AutomationScheduleGap) AutomationScheduleGapBody {
	return AutomationScheduleGapBody{
		ID:               gap.ID,
		FromExclusive:    gap.FromExclusive,
		ThroughInclusive: gap.ThroughInclusive,
		RecordedAt:       gap.RecordedAt,
		Reason:           gap.Reason,
	}
}
