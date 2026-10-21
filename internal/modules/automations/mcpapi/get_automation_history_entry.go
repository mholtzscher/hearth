//nolint:gochecknoglobals // Fixed contracts are immutable process-lifetime preparation.
package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

var prepareGetAutomationHistoryEntryOutput = sync.OnceValues(func() (*mcptools.OutputContract[HistoryDetail], error) {
	return mcptools.PrepareOutput[HistoryDetail](resultSchema("historyDetail"))
})

func registerGetAutomationHistoryEntry(server *mcptools.Server, handler *handler) {
	mcptools.Register(server, mcptools.Tool[getAutomationHistoryEntryToolInput, HistoryDetail]{
		Output:      mustContract(prepareGetAutomationHistoryEntryOutput()),
		Name:        "get_automation_history_entry",
		Description: "Inspect one direct Run or Skip history detail with its immutable snapshot and canonical evidence",
		Handler:     handler.getAutomationHistoryEntry,
	})
}

type getAutomationHistoryEntryToolInput struct {
	AutomationID automationID `json:"automation_id" jsonschema:"Canonical Hearth Automation ID"`
	EntryID      entryID      `json:"entry_id"      jsonschema:"Canonical Hearth Automation Run or Skip ID"`
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

	Status            string                                    `json:"status"`
	StartedAt         time.Time                                 `json:"started_at"`
	CompletedAt       *time.Time                                `json:"completed_at,omitempty"`
	FailureCode       string                                    `json:"failure_code,omitempty"`
	Snapshot          json.RawMessage                           `json:"snapshot"`
	MatchedTriggerIDs []string                                  `json:"matched_trigger_ids"`
	Steps             []AutomationStepAttemptEvidence           `json:"steps"`
	BranchDecisions   []json.RawMessage                         `json:"branch_decisions"`
	Delays            []AutomationDelayExecutionEvidence        `json:"delays"`
	Notifications     []AutomationNotificationExecutionEvidence `json:"notifications"`
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
		Steps:           make([]AutomationStepAttemptEvidence, len(entry.Steps)),
		BranchDecisions: make([]json.RawMessage, len(entry.BranchDecisions)),
		Delays:          make([]AutomationDelayExecutionEvidence, len(entry.Delays)),
		Notifications:   make([]AutomationNotificationExecutionEvidence, len(entry.Notifications)),
	}
	for i, id := range entry.MatchedTriggerIDs {
		result.MatchedTriggerIDs[i] = string(id)
	}
	for i, step := range entry.Steps {
		result.Steps[i] = automationStepAttemptEvidence(step)
	}
	for i, branch := range entry.BranchDecisions {
		result.BranchDecisions[i], err = automations.EncodeBranchDecision(branch)
		if err != nil {
			return HistoryDetail{}, err
		}
	}
	for i, delay := range entry.Delays {
		result.Delays[i] = delayExecutionEvidence(delay)
	}
	for i, notification := range entry.Notifications {
		result.Notifications[i] = notificationExecutionEvidence(notification)
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

// AutomationStepAttemptEvidence exposes only ownership-verified Command evidence.
type AutomationStepAttemptEvidence struct{ Variant AutomationStepAttemptVariant }

//sumtype:decl
type AutomationStepAttemptVariant interface{ isAutomationStepAttemptEvidence() }

type AutomationStepAttemptFields struct {
	Position int    `json:"position"`
	StepID   string `json:"step_id"`
}

type NotAttemptedStepEvidence struct {
	AutomationStepAttemptFields

	Status string `json:"status" enum:"not_attempted"`
}

type RunningStepEvidence struct {
	AutomationStepAttemptFields

	Status    string    `json:"status"     enum:"running"`
	StartedAt time.Time `json:"started_at"`
}

type VerifiedStepEvidence struct {
	AutomationStepAttemptFields

	Status            string    `json:"status"              enum:"satisfied,dispatched"`
	StartedAt         time.Time `json:"started_at"`
	CompletedAt       time.Time `json:"completed_at"`
	VerifiedCommandID string    `json:"verified_command_id"`
}

type UnsuccessfulStepEvidence struct {
	AutomationStepAttemptFields

	Status            string    `json:"status"                        enum:"failed,interrupted"`
	StartedAt         time.Time `json:"started_at"`
	CompletedAt       time.Time `json:"completed_at"`
	FailureCode       string    `json:"failure_code"`
	VerifiedCommandID *string   `json:"verified_command_id,omitempty"`
}

func (NotAttemptedStepEvidence) isAutomationStepAttemptEvidence() {}

func (RunningStepEvidence) isAutomationStepAttemptEvidence() {}

func (VerifiedStepEvidence) isAutomationStepAttemptEvidence() {}

func (UnsuccessfulStepEvidence) isAutomationStepAttemptEvidence() {}

func (evidence AutomationStepAttemptEvidence) MarshalJSON() ([]byte, error) {
	switch evidence.Variant.(type) {
	case NotAttemptedStepEvidence, RunningStepEvidence, VerifiedStepEvidence, UnsuccessfulStepEvidence:
		return json.Marshal(evidence.Variant)
	default:
		return nil, fmt.Errorf("invalid Step attempt DTO %T", evidence.Variant)
	}
}

func automationStepAttemptEvidence(step automations.StepAttempt) AutomationStepAttemptEvidence {
	fields := AutomationStepAttemptFields{Position: step.Position, StepID: string(step.StepID)}
	switch state := step.State.(type) {
	case automations.NotAttemptedStep:
		return AutomationStepAttemptEvidence{
			Variant: NotAttemptedStepEvidence{AutomationStepAttemptFields: fields, Status: "not_attempted"},
		}
	case automations.RunningStep:
		return AutomationStepAttemptEvidence{Variant: RunningStepEvidence{
			AutomationStepAttemptFields: fields, Status: "running", StartedAt: state.StartedAt,
		}}
	case automations.CompletedStep:
		return completedStepEvidence(fields, state)
	default:
		panic("invalid retained Step state")
	}
}

func completedStepEvidence(
	fields AutomationStepAttemptFields,
	state automations.CompletedStep,
) AutomationStepAttemptEvidence {
	switch outcome := state.Outcome.(type) {
	case automations.SatisfiedStep:
		return AutomationStepAttemptEvidence{Variant: VerifiedStepEvidence{
			AutomationStepAttemptFields: fields, Status: "satisfied", StartedAt: state.StartedAt,
			CompletedAt: state.CompletedAt, VerifiedCommandID: string(outcome.VerifiedCommandID),
		}}
	case automations.DispatchedStep:
		return AutomationStepAttemptEvidence{Variant: VerifiedStepEvidence{
			AutomationStepAttemptFields: fields, Status: "dispatched", StartedAt: state.StartedAt,
			CompletedAt: state.CompletedAt, VerifiedCommandID: string(outcome.VerifiedCommandID),
		}}
	case automations.FailedStep:
		return AutomationStepAttemptEvidence{Variant: UnsuccessfulStepEvidence{
			AutomationStepAttemptFields: fields, Status: "failed", StartedAt: state.StartedAt,
			CompletedAt: state.CompletedAt, FailureCode: outcome.FailureCode,
			VerifiedCommandID: publicVerifiedCommandID(outcome.VerifiedCommandID),
		}}
	case automations.InterruptedStep:
		return AutomationStepAttemptEvidence{Variant: UnsuccessfulStepEvidence{
			AutomationStepAttemptFields: fields, Status: "interrupted", StartedAt: state.StartedAt,
			CompletedAt: state.CompletedAt, FailureCode: outcome.FailureCode,
			VerifiedCommandID: publicVerifiedCommandID(outcome.VerifiedCommandID),
		}}
	default:
		panic("invalid retained Step outcome")
	}
}

func publicVerifiedCommandID(id *devices.CommandID) *string {
	if id == nil {
		return nil
	}
	value := string(*id)
	return &value
}

// AutomationDelayExecutionEvidence exposes reached wait evidence and diagnostic due time.
type AutomationDelayExecutionEvidence struct {
	StepID      string     `json:"step_id"`
	Position    int        `json:"position"               minimum:"0" maximum:"63"`
	DurationMS  int64      `json:"duration_ms"            minimum:"1" maximum:"86400000"`
	Status      string     `json:"status"                                                enum:"running,completed,interrupted"`
	StartedAt   time.Time  `json:"started_at"`
	DueAt       time.Time  `json:"due_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	FailureCode *string    `json:"failure_code,omitempty"`
}

func delayExecutionEvidence(delay automations.DelayExecution) AutomationDelayExecutionEvidence {
	return AutomationDelayExecutionEvidence{
		StepID: string(delay.StepID), Position: delay.Position, DurationMS: delay.DurationMS,
		Status: string(delay.Status), StartedAt: delay.StartedAt, DueAt: delay.DueAt,
		CompletedAt: delay.CompletedAt, FailureCode: delay.FailureCode,
	}
}

// AutomationNotificationExecutionEvidence retains Step outcome independently of
// notification attempt retention. A reserved link need not have admitted a send.
type AutomationNotificationExecutionEvidence struct {
	StepID      string     `json:"step_id"`
	Position    int        `json:"position"              minimum:"0" maximum:"31"`
	AttemptID   string     `json:"attempt_id"`
	Status      string     `json:"status"                                         enum:"running,accepted,failed,interrupted"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Outcome     *string    `json:"outcome,omitempty"`
	ReasonCode  *string    `json:"reason_code,omitempty"`
}

func notificationExecutionEvidence(
	execution automations.NotificationExecution,
) AutomationNotificationExecutionEvidence {
	evidence := AutomationNotificationExecutionEvidence{
		StepID: string(execution.StepID), Position: execution.Position,
		AttemptID: string(execution.AttemptID), Status: string(execution.Status),
		StartedAt: execution.StartedAt, CompletedAt: execution.CompletedAt,
		ReasonCode: execution.ReasonCode,
	}
	if execution.Outcome != nil {
		outcome := string(*execution.Outcome)
		evidence.Outcome = &outcome
	}
	return evidence
}

//nolint:goconst // Wire field names stay literal beside this operation's result contract.
func historyDetailSchema() map[string]any {
	common := func() map[string]any {
		return map[string]any{
			"entry_id":           stringSchema(),
			automationIDField:    stringSchema(),
			"automation_name":    stringSchema(),
			"kind":               enumSchema("run", "skip"),
			"revision":           revisionSchema(),
			"recorded_at":        timeSchema(),
			"cause":              refSchema("#/$defs/evidence/$defs/cause"),
			"condition_decision": refSchema("#/$defs/evidence/$defs/conditionDecision"),
		}
	}
	required := []string{
		"entry_id",
		automationIDField,
		"automation_name",
		"kind",
		"revision",
		"recorded_at",
		"cause",
		"condition_decision",
	}
	run := common()
	run["kind"] = enumSchema("run")
	run["status"] = enumSchema("running", "succeeded", "failed", "interrupted")
	run["started_at"], run["completed_at"], run["failure_code"] = timeSchema(), timeSchema(), stringSchema()
	run["snapshot"] = refSchema("#/$defs/definition")
	run["matched_trigger_ids"] = refSchema("#/$defs/evidence/$defs/triggerIDs")
	for field, item := range map[string]struct {
		canonical string
		maximum   int
	}{
		"steps":            {"stepAttempt", 32},
		"branch_decisions": {"branchDecision", 64},
		"delays":           {"delayExecution", 64},
		"notifications":    {"notificationExecution", 32},
	} {
		array := arraySchema(refSchema("#/$defs/evidence/$defs/" + item.canonical))
		array["maxItems"] = item.maximum
		run[field] = array
	}
	runRoot := objectSchema(
		run,
		append(
			append([]string{}, required...),
			"status",
			"started_at",
			"snapshot",
			"matched_trigger_ids",
			"steps",
			"branch_decisions",
			"delays",
			"notifications",
		)...)
	runRoot["oneOf"] = runLifecycleSchema()
	skip := common()
	skip["kind"], skip["reason"], skip["skipped_at"] = enumSchema(
		"skip",
	), enumSchema(
		"automation_busy",
		"stale_fact",
		"conditions_false",
		"conditions_unknown",
	), timeSchema()
	matchedTriggers := arraySchema(refSchema("#/$defs/definition/properties/triggers/items"))
	matchedTriggers["maxItems"] = 32
	skip["matched_triggers"] = matchedTriggers
	skipRoot := objectSchema(
		skip,
		append(append([]string{}, required...), "reason", "skipped_at", "matched_triggers")...)
	return map[string]any{"type": "object", "oneOf": []any{runRoot, skipRoot}}
}

// entryID is a canonical Hearth Run or Skip ID argument.
type entryID string

func (id *entryID) UnmarshalJSON(data []byte) error {
	value, err := argumentString(data, "invalid_entry_id", "entry_id")
	if err != nil {
		return err
	}
	if !validEntryID(value) {
		return errors.New("invalid_entry_id: entry_id is not a canonical history ID")
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
