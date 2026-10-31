//nolint:goconst // Literal wire fields and statuses keep projections and contracts readable.
package mcpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerGetAutomationHistoryEntry(server *mcptools.Server, handler *handler) {
	mcptools.Register(server, mcptools.Tool[getAutomationHistoryEntryToolInput, HistoryDetail]{
		Name:        "get_automation_history_entry",
		Description: "Inspect one direct Run or Skip history detail with its immutable snapshot and canonical evidence",
		Handler:     handler.getAutomationHistoryEntry,
	})
}

type getAutomationHistoryEntryToolInput struct {
	AutomationID automationID `json:"automation_id" doc:"Canonical Hearth Automation ID"`
	EntryID      entryID      `json:"entry_id"      doc:"Canonical Hearth Automation Run or Skip ID"`
}

// getAutomationHistoryEntry reads exactly one retained Run or Skip. The domain
// reports one sentinel for a missing entry and for a missing or mismatched
// Automation, so the tool publishes the entry code for both rather than
// pretending it can tell them apart.
func (handler *handler) getAutomationHistoryEntry(
	ctx context.Context,
	input getAutomationHistoryEntryToolInput,
) (HistoryDetail, error) {
	entry, err := handler.automations.GetHistoryEntry(
		ctx,
		automations.AutomationID(input.AutomationID),
		string(input.EntryID),
	)
	if err != nil {
		return HistoryDetail{}, automationFailure(err, failureHistoryEntryNotFound)
	}
	result, err := historyDetail(entry)
	if err != nil {
		return HistoryDetail{}, mcptools.InternalToolError(err)
	}
	return result, nil
}

// Schema returns the canonical success contract.
func (HistoryDetail) Schema(_ huma.Registry) *huma.Schema {
	schema := historyDetailSchema()
	schema.Extensions = map[string]any{
		canonicalDefinitionsKeyword: canonicalResultDefinitions(automations.AutomationDefinitionSchema()),
	}
	return schema
}

// HistoryDetail encodes one direct discriminated Run or Skip, never an HTTP envelope.
type HistoryDetail struct{ value historyVariant }

//sumtype:decl
type historyVariant interface{ isHistoryDetail() }

func (RunDetail) isHistoryDetail() {}

func (SkipDetail) isHistoryDetail() {}

func (value HistoryDetail) MarshalJSON() ([]byte, error) { return json.Marshal(value.value) }

type HistoryFields struct {
	EntryID           string                 `json:"entry_id"`
	AutomationID      string                 `json:"automation_id"`
	AutomationName    string                 `json:"automation_name"`
	Kind              string                 `json:"kind"`
	Revision          int64                  `json:"revision"`
	RecordedAt        time.Time              `json:"recorded_at"`
	Cause             AdmissionCauseEvidence `json:"cause"`
	ConditionDecision json.RawMessage        `json:"condition_decision"`
}

type RunDetail struct {
	HistoryFields

	Status              string            `json:"status"`
	StartedAt           time.Time         `json:"started_at"`
	CompletedAt         *time.Time        `json:"completed_at,omitempty"`
	FailureCode         string            `json:"failure_code,omitempty"`
	Snapshot            json.RawMessage   `json:"snapshot"`
	MatchedTriggerIDs   []string          `json:"matched_trigger_ids"`
	Executions          []json.RawMessage `json:"executions"`
	DataEvidence        string            `json:"data_evidence"`
	ValueInitialization json.RawMessage   `json:"value_initialization"`
}

type SkipDetail struct {
	HistoryFields

	Reason          string          `json:"reason"`
	SkippedAt       time.Time       `json:"skipped_at"`
	MatchedTriggers json.RawMessage `json:"matched_triggers"`
}

func historyDetail(entry automations.HistoryEntry) (HistoryDetail, error) {
	switch entry := entry.(type) {
	case automations.Run:
		return runDetail(entry)
	case automations.Skip:
		return skipDetail(entry)
	default:
		return HistoryDetail{}, fmt.Errorf("invalid retained history entry %T", entry)
	}
}
func runDetail(entry automations.Run) (HistoryDetail, error) {
	decision, err := automations.EncodeConditionDecision(entry.ConditionDecision)
	if err != nil {
		return HistoryDetail{}, err
	}
	snapshot, err := automations.EncodeDefinition(entry.Snapshot)
	if err != nil {
		return HistoryDetail{}, err
	}
	receipt := runReceipt(entry)
	//nolint:modernize // Explicit embedded field initialization is accepted by the current compiler.
	result := RunDetail{
		HistoryFields: HistoryFields{
			EntryID:           string(entry.ID),
			AutomationID:      string(entry.AutomationID),
			AutomationName:    entry.AutomationName,
			Kind:              string(automations.HistoryRun),
			Revision:          entry.Revision,
			RecordedAt:        entry.StartedAt,
			Cause:             admissionCauseEvidence(entry.Cause),
			ConditionDecision: decision,
		},
		Status:      receipt.Status,
		StartedAt:   entry.StartedAt,
		CompletedAt: receipt.CompletedAt,
		FailureCode: receipt.FailureCode,
		Snapshot:    snapshot,
		MatchedTriggerIDs: make(
			[]string,
			len(entry.MatchedTriggerIDs),
		),
		Executions:   make([]json.RawMessage, len(entry.Executions)),
		DataEvidence: entry.DataEvidence,
	}
	if entry.ValueInitialization != nil {
		result.ValueInitialization, err = automations.EncodeRunValueInitialization(*entry.ValueInitialization)
		if err != nil {
			return HistoryDetail{}, err
		}
	}
	for i, id := range entry.MatchedTriggerIDs {
		result.MatchedTriggerIDs[i] = string(id)
	}
	for i, execution := range entry.Executions {
		result.Executions[i], err = automations.EncodePublicStepExecution(execution)
		if err != nil {
			return HistoryDetail{}, err
		}
	}
	return HistoryDetail{value: result}, nil
}
func skipDetail(entry automations.Skip) (HistoryDetail, error) {
	decision, err := automations.EncodeConditionDecision(entry.ConditionDecision)
	if err != nil {
		return HistoryDetail{}, err
	}
	triggers, err := automations.EncodeMatchedTriggers(entry.MatchedTriggers)
	if err != nil {
		return HistoryDetail{}, err
	}
	return HistoryDetail{value: SkipDetail{
		HistoryFields: HistoryFields{
			EntryID:           string(entry.ID),
			AutomationID:      string(entry.AutomationID),
			AutomationName:    entry.AutomationName,
			Kind:              string(automations.HistorySkip),
			Revision:          entry.Revision,
			RecordedAt:        entry.SkippedAt,
			Cause:             admissionCauseEvidence(entry.Cause),
			ConditionDecision: decision,
		},
		Reason:          string(entry.Reason),
		SkippedAt:       entry.SkippedAt,
		MatchedTriggers: triggers,
	}}, nil
}

type HeldStateEvidence struct {
	TriggerID string    `json:"trigger_id"`
	StartedAt time.Time `json:"started_at"`
	DueAt     time.Time `json:"due_at"`
}

func heldStateEvidence(value automations.HeldStateEvidence) HeldStateEvidence {
	return HeldStateEvidence{TriggerID: string(value.TriggerID), StartedAt: value.StartedAt, DueAt: value.DueAt}
}

func (value AdmissionCauseEvidence) MarshalJSON() ([]byte, error) {
	switch value.Variant.(type) {
	case ManualCauseEvidence, ScheduleCauseEvidence, DeviceFactCauseEvidence, HeldStateCauseEvidence:
		return json.Marshal(value.Variant)
	default:
		return nil, fmt.Errorf("invalid retained cause %T", value.Variant)
	}
}

func (value DeviceFactEvidence) MarshalJSON() ([]byte, error) {
	switch value.Variant.(type) {
	case ObservationFactEvidence, EntityEventFactEvidence:
		return json.Marshal(value.Variant)
	default:
		return nil, fmt.Errorf("invalid retained fact %T", value.Variant)
	}
}

// AdmissionCauseEvidence is one explicit public provenance variant.
type AdmissionCauseEvidence struct{ Variant AdmissionCauseVariant }

// AdmissionCauseVariant is a concrete public provenance DTO.
//
//sumtype:decl
type AdmissionCauseVariant interface{ isAdmissionCauseEvidence() }

type ManualCauseEvidence struct {
	Kind string `json:"kind" enum:"manual"`
}

type ScheduleCauseEvidence struct {
	Kind string `json:"kind" enum:"schedule"`
}

type DeviceFactCauseEvidence struct {
	Kind string             `json:"kind" enum:"device_fact"`
	Fact DeviceFactEvidence `json:"fact"`
}

type HeldStateCauseEvidence struct {
	Kind     string            `json:"kind"     enum:"held_state"`
	Evidence HeldStateEvidence `json:"evidence"`
}

func (ManualCauseEvidence) isAdmissionCauseEvidence() {}

func (ScheduleCauseEvidence) isAdmissionCauseEvidence() {}

func (DeviceFactCauseEvidence) isAdmissionCauseEvidence() {}

func (HeldStateCauseEvidence) isAdmissionCauseEvidence() {}

// DeviceFactEvidence retains the family-specific identity and complete evidence.
type DeviceFactEvidence struct{ Variant DeviceFactVariant }

// DeviceFactVariant is a concrete public Fact DTO.
//
//sumtype:decl
type DeviceFactVariant interface{ isDeviceFactEvidence() }

type ObservationFactEvidence struct {
	FactID        string          `json:"fact_id"`
	Family        string          `json:"family"                   enum:"observation"`
	EntityID      string          `json:"entity_id"`
	EmittedAt     time.Time       `json:"emitted_at"`
	ObservationID string          `json:"observation_id"`
	Disposition   string          `json:"disposition"`
	Value         json.RawMessage `json:"value"`
	PreviousValue json.RawMessage `json:"previous_value,omitempty"`
}

type EntityEventFactEvidence struct {
	FactID    string    `json:"fact_id"`
	Family    string    `json:"family"     enum:"entity_event"`
	EntityID  string    `json:"entity_id"`
	EmittedAt time.Time `json:"emitted_at"`
	EventID   string    `json:"event_id"`
	Name      string    `json:"name"`
}

func (ObservationFactEvidence) isDeviceFactEvidence() {}

func (EntityEventFactEvidence) isDeviceFactEvidence() {}

func admissionCauseEvidence(cause automations.AdmissionCause) AdmissionCauseEvidence {
	switch cause := cause.(type) {
	case automations.ManualCause:
		return AdmissionCauseEvidence{Variant: ManualCauseEvidence{Kind: "manual"}}
	case automations.ScheduleCause:
		return AdmissionCauseEvidence{Variant: ScheduleCauseEvidence{Kind: "schedule"}}
	case automations.DeviceFactCause:
		return AdmissionCauseEvidence{
			Variant: DeviceFactCauseEvidence{Kind: "device_fact", Fact: deviceFactEvidence(cause.Fact)},
		}
	case automations.HeldStateCause:
		return AdmissionCauseEvidence{
			Variant: HeldStateCauseEvidence{Kind: "held_state", Evidence: heldStateEvidence(cause.Evidence)},
		}
	default:
		panic("invalid retained admission cause")
	}
}

func deviceFactEvidence(fact automations.DeviceFact) DeviceFactEvidence {
	switch fact := fact.(type) {
	case automations.ObservationFact:
		return DeviceFactEvidence{Variant: ObservationFactEvidence{
			FactID: string(
				fact.FactID,
			),
			Family:        "observation",
			EntityID:      string(fact.EntityID),
			EmittedAt:     fact.EmittedAt,
			ObservationID: string(fact.ObservationID),
			Disposition:   string(fact.Disposition),
			Value: append(
				json.RawMessage(nil),
				fact.Value...),
			PreviousValue: append(json.RawMessage(nil), fact.PreviousValue...),
		}}
	case automations.EntityEventFact:
		return DeviceFactEvidence{Variant: EntityEventFactEvidence{
			FactID: string(
				fact.FactID,
			),
			Family:    "entity_event",
			EntityID:  string(fact.EntityID),
			EmittedAt: fact.EmittedAt,
			EventID:   string(fact.EventID),
			Name:      string(fact.Name),
		}}
	default:
		panic("invalid retained Device Fact")
	}
}

//nolint:mnd // Canonical evidence limits stay beside each array.
func historyDetailSchema() *huma.Schema {
	const maximumMatchedTriggers = 32

	return &huma.Schema{Type: "object", OneOf: []*huma.Schema{
		{
			Type: "object", AdditionalProperties: false,
			Properties: map[string]*huma.Schema{
				"entry_id":            {Type: "string", MinLength: new(1)},
				automationIDField:     {Type: "string", MinLength: new(1)},
				"automation_name":     {Type: "string", MinLength: new(1)},
				"kind":                {Enum: []any{"run"}},
				"revision":            {Type: "integer", Minimum: new(float64(1))},
				"recorded_at":         {Type: "string", Format: "date-time"},
				"cause":               {Ref: "#/$defs/evidence/$defs/cause"},
				"condition_decision":  {Ref: "#/$defs/evidence/$defs/conditionDecision"},
				"status":              {Enum: []any{"running", "succeeded", "failed", "interrupted"}},
				"started_at":          {Type: "string", Format: "date-time"},
				"completed_at":        {Type: "string", Format: "date-time"},
				"failure_code":        {Type: "string", MinLength: new(1)},
				"snapshot":            {Ref: "#/$defs/definition"},
				"matched_trigger_ids": {Ref: "#/$defs/evidence/$defs/triggerIDs"},
				"data_evidence":       {Enum: []any{"recorded", "legacy_unavailable"}},
				"value_initialization": {
					OneOf: []*huma.Schema{{Ref: "#/$defs/evidence/$defs/valueInitialization"}, {Type: "null"}},
				},
				"executions": {
					Type:     "array",
					MaxItems: new(64),
					Items:    &huma.Schema{Ref: "#/$defs/evidence/$defs/execution"},
				},
			},
			Required: []string{
				"entry_id",
				automationIDField,
				"automation_name",
				"kind",
				"revision",
				"recorded_at",
				"cause",
				"condition_decision",
				"status",
				"started_at",
				"snapshot",
				"matched_trigger_ids",
				"executions",
				"data_evidence",
				"value_initialization",
			},
			OneOf: runLifecycleSchema(),
		},
		{
			Type: "object", AdditionalProperties: false,
			Properties: map[string]*huma.Schema{
				"entry_id":           {Type: "string", MinLength: new(1)},
				automationIDField:    {Type: "string", MinLength: new(1)},
				"automation_name":    {Type: "string", MinLength: new(1)},
				"kind":               {Enum: []any{"skip"}},
				"revision":           {Type: "integer", Minimum: new(float64(1))},
				"recorded_at":        {Type: "string", Format: "date-time"},
				"cause":              {Ref: "#/$defs/evidence/$defs/cause"},
				"condition_decision": {Ref: "#/$defs/evidence/$defs/conditionDecision"},
				"reason": {
					Enum: []any{"automation_busy", "stale_fact", "conditions_false", "conditions_unknown"},
				},
				"skipped_at": {Type: "string", Format: "date-time"},
				"matched_triggers": {
					Type: "array", MaxItems: new(maximumMatchedTriggers),
					Items: &huma.Schema{Ref: "#/$defs/definition/properties/triggers/items"},
				},
			},
			Required: []string{
				"entry_id",
				automationIDField,
				"automation_name",
				"kind",
				"revision",
				"recorded_at",
				"cause",
				"condition_decision",
				"reason",
				"skipped_at",
				"matched_triggers",
			},
		},
	}}
}

// entryID is a canonical Hearth Run or Skip ID argument.
type entryID string

func (id *entryID) UnmarshalJSON(data []byte) error {
	value, err := argumentString(data, "invalid_entry_id", "entry_id")
	if err != nil {
		return err
	}
	if !validEntryID(value) {
		return &mcptools.ToolError{Code: "invalid_entry_id", Message: "entry_id is not a canonical history ID"}
	}
	*id = entryID(value)
	return nil
}

func validEntryID(value string) bool {
	if _, err := automations.ParseRunID(value); err == nil {
		return true
	}
	_, err := automations.ParseSkipID(value)
	return err == nil
}
