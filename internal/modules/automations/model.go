package automations

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationID is the durable identity of one Automation definition (aut_ UUIDv7).
type AutomationID string

// RunID is the durable identity of one Automation Run (arn_ UUIDv7), distinct
// from the adapter run_ identity.
type RunID string

// SkipID is the durable identity of one recorded Skip (ask_ UUIDv7).
type SkipID string

// TriggerID is an author-supplied subject-safe slug identifying one Trigger within its own definition.
type TriggerID string

// StepID is an author-supplied subject-safe slug identifying one Step within its own definition.
type StepID string

// TriggerKind is the closed discriminated family of one Automation Trigger.
type TriggerKind string

const (
	// TriggerKindObservation matches one accepted Observation Fact.
	TriggerKindObservation TriggerKind = "observation"
	// TriggerKindEntityEvent matches one accepted Entity Event Fact.
	TriggerKindEntityEvent TriggerKind = "entity_event"
	// TriggerKindHeldState starts an Automation after State has matched for a duration.
	TriggerKindHeldState TriggerKind = "held_state"
)

// ComparisonOperator is the closed set of typed Observation comparisons.
type ComparisonOperator string

const (
	// ComparisonEqual requires equal JSON type and equal JSON value.
	ComparisonEqual ComparisonOperator = "eq"
	// ComparisonNotEqual requires equal JSON type and a different value.
	ComparisonNotEqual ComparisonOperator = "ne"
	// ComparisonLessThan requires two finite JSON numbers with left < right.
	ComparisonLessThan ComparisonOperator = "lt"
	// ComparisonLessThanOrEqual requires two finite JSON numbers with left <= right.
	ComparisonLessThanOrEqual ComparisonOperator = "lte"
	// ComparisonGreaterThan requires two finite JSON numbers with left > right.
	ComparisonGreaterThan ComparisonOperator = "gt"
	// ComparisonGreaterThanOrEqual requires two finite JSON numbers with left >= right.
	ComparisonGreaterThanOrEqual ComparisonOperator = "gte"
)

// ObservationComparison is one typed comparison against an Observation Fact
// value. Pointer is RFC 6901; the empty pointer selects the whole value, and
// Operand is exactly one normalized JSON value.
type ObservationComparison struct {
	Pointer  string
	Operator ComparisonOperator
	Operand  json.RawMessage
}

// ObservationTrigger matches an Observation Fact by Entity, disposition, and up
// to eight independent comparisons against each side of the transition.
type ObservationTrigger struct {
	EntityID            devices.EntityID
	Dispositions        []devices.ObservationDisposition
	PreviousComparisons []ObservationComparison
	Comparisons         []ObservationComparison
}

// EntityEventTrigger matches one Entity Event Fact by exact Entity ID and event name.
type EntityEventTrigger struct {
	EntityID  devices.EntityID
	EventName devices.EntityEventName
}

// HeldStateTrigger matches the current State value while it remains equal to
// every configured comparison for ForSeconds.
type HeldStateTrigger struct {
	EntityID    devices.EntityID
	Comparisons []ObservationComparison
	ForSeconds  int64
}

// Trigger is one identified typed Trigger; exactly one family payload is set
// matching Kind.
type Trigger struct {
	ID          TriggerID
	Kind        TriggerKind
	Observation *ObservationTrigger
	EntityEvent *EntityEventTrigger
	HeldState   *HeldStateTrigger
}

// Step is one identified, execution-ordered Command with exact Entity, Operation, and normalized static JSON parameters.
type Step struct {
	ID            StepID
	EntityID      devices.EntityID
	OperationName devices.OperationName
	Parameters    devices.CommandParameters
}

// Definition is one complete, normalized Automation document. Conditions is
// optional; explicit JSON null is invalid.
type Definition struct {
	Name       string // 1–200 runes, trimmed; not unique
	Enabled    bool
	Triggers   []Trigger // 1–32, IDs unique
	Conditions *Condition
	Steps      []Step // 1–32, IDs unique and execution ordered
}

// Record is one live definition at its current revision, which starts at 1 and increments on replacement.
type Record struct {
	ID         AutomationID
	Revision   int64
	Definition Definition
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// DeviceFactFamily is the closed family of Device Fact evidence an automation admits.
type DeviceFactFamily string

const (
	// DeviceFactObservation identifies an Observation Fact.
	DeviceFactObservation DeviceFactFamily = "observation"
	// DeviceFactEntityEvent identifies an Entity Event Fact.
	DeviceFactEntityEvent DeviceFactFamily = "entity_event"
)

// ObservationFact reports a committed, applied or unchanged Observation.
// EmittedAt is the Fact envelope time, not the Observation time.
type ObservationFact struct {
	FactID        devices.DeviceFactID
	ObservationID devices.ObservationID
	EntityID      devices.EntityID
	Disposition   devices.ObservationDisposition
	Value         devices.Value
	PreviousValue devices.Value // nil means absent; bytes containing null are a real predecessor.
	EmittedAt     time.Time
}

// EntityEventFact reports a committed Entity Event.
// EmittedAt is the Fact envelope time, not the Entity Event time.
type EntityEventFact struct {
	FactID    devices.DeviceFactID
	EventID   devices.EntityEventID
	EntityID  devices.EntityID
	Name      devices.EntityEventName
	EmittedAt time.Time
}

// DeviceFact is exactly one typed Device Fact family payload.
type DeviceFact struct {
	Family      DeviceFactFamily
	Observation *ObservationFact
	EntityEvent *EntityEventFact
}

// DeviceFactSummary is immutable history evidence; the two value fields are
// present only for Observation Facts and PreviousStateValue may be JSON null.
type DeviceFactSummary struct {
	FactID             devices.DeviceFactID
	Family             DeviceFactFamily
	EntityID           devices.EntityID
	Variant            string // observation disposition or Entity Event name
	CausationID        string // obs_ or evt_
	ObservationValue   devices.Value
	PreviousStateValue devices.Value
	EmittedAt          time.Time
}

// AdmissionOutcome reports what one Device Fact admission decided.
type AdmissionOutcome struct {
	MatchedAutomations int
	StartedRuns        int
	RecordedSkips      int
	DuplicateOutcomes  int
}

// AdmissionResult contains committed admission outcomes.
type AdmissionResult struct {
	Outcome     AdmissionOutcome
	StartedRuns []Run
	Skips       []AdmissionSkip
}

// AdmissionSkip carries committed Skip identity, admission source, reason, and
// nullable Fact identity for logging. FactID, Family, and Variant are set only
// for a device-fact Skip.
type AdmissionSkip struct {
	SkipID       SkipID
	AutomationID AutomationID
	Revision     int64
	Source       RunSource
	Reason       SkipReason
	FactID       *devices.DeviceFactID
	Family       DeviceFactFamily
	Variant      string
}

// RunSource distinguishes how a Run was admitted and records the same provenance on a Skip.
type RunSource string

const (
	// RunSourceDeviceFact marks a Run admitted by a matching Device Fact.
	RunSourceDeviceFact RunSource = "device_fact"
	// RunSourceManual marks a Run admitted by an operator request.
	RunSourceManual RunSource = "manual"
	// RunSourceHeldState marks a Run admitted when a State predicate elapsed.
	RunSourceHeldState RunSource = "held_state"
)

// HeldStateEvidence records the scheduled window for a held-state outcome.
type HeldStateEvidence struct {
	TriggerID TriggerID
	StartedAt time.Time
	DueAt     time.Time
}

// RunStatus is the durable state of one Automation Run.
type RunStatus string

const (
	// RunRunning marks an admitted Run whose Steps are still being executed.
	RunRunning RunStatus = "running"
	// RunSucceeded marks a Run whose every Step reached a successful outcome.
	RunSucceeded RunStatus = "succeeded"
	// RunFailed marks a Run stopped by the first failed Step.
	RunFailed RunStatus = "failed"
	// RunInterrupted marks a Run that stopped without establishing a Command
	// outcome, including core restart, drain, and executor faults.
	RunInterrupted RunStatus = "interrupted"
)

// StepStatus is the durable state of one ordered Run Step.
type StepStatus string

const (
	// StepNotAttempted marks a Step that never started.
	StepNotAttempted StepStatus = "not_attempted"
	// StepRunning marks a Step whose Command identity is reserved and recorded.
	StepRunning StepStatus = "running"
	// StepSatisfied marks a Step whose verified Command reached satisfied.
	StepSatisfied StepStatus = "satisfied"
	// StepDispatched marks a Step whose verified Command reached dispatched.
	StepDispatched StepStatus = "dispatched"
	// StepFailed marks a Step whose verified or confirmed Command failed.
	StepFailed StepStatus = "failed"
	// StepInterrupted marks a Step stopped without a durable Command outcome.
	StepInterrupted StepStatus = "interrupted"
)

// StepAttempt records an ordered Step's execution state.
// Reserved identities are private; only verified Command links are exposed.
type StepAttempt struct {
	Position              int
	StepID                StepID
	Status                StepStatus
	ReservedCommandID     *devices.CommandID
	ReservedCorrelationID *devices.CorrelationID
	VerifiedCommandID     *devices.CommandID
	FailureCode           *string
	StartedAt             *time.Time
	CompletedAt           *time.Time
}

// Run tracks execution of an immutable definition snapshot with admission provenance and ordered Step attempts.
type Run struct {
	ID                RunID
	AutomationID      AutomationID
	AutomationName    string
	Revision          int64
	Snapshot          Definition
	Source            RunSource
	Fact              *DeviceFactSummary // non-nil iff Source is RunSourceDeviceFact
	HeldState         *HeldStateEvidence // non-nil iff Source is RunSourceHeldState
	MatchedTriggerIDs []TriggerID        // empty iff Source is RunSourceManual
	ConditionDecision ConditionDecision
	Status            RunStatus
	FailureCode       *string
	StartedAt         time.Time
	CompletedAt       *time.Time
	Steps             []StepAttempt
}

// SkipReason identifies why a matching Fact did not start a Run.
type SkipReason string

const (
	// SkipBusy marks a matching Automation that already had a running Run.
	SkipBusy SkipReason = "automation_busy"
	// SkipStaleFact marks a matching Fact older than the freshness bound.
	SkipStaleFact SkipReason = "stale_fact"
	// SkipConditionsFalse marks a matching Automation whose Conditions
	// evaluated false.
	SkipConditionsFalse SkipReason = "conditions_false"
	// SkipConditionsUnknown marks a matching Automation whose Conditions
	// evaluated unknown.
	SkipConditionsUnknown SkipReason = "conditions_unknown"
)

// Skip is one recorded non-Run outcome with its admission provenance,
// immutable matching Trigger snapshots, and an admission Condition decision. A
// device-fact Skip carries complete Fact evidence; a held-state Skip carries
// hold evidence and one matched Trigger; a manual Skip carries neither.
type Skip struct {
	ID                SkipID
	AutomationID      AutomationID
	AutomationName    string
	Revision          int64
	Source            RunSource          // device_fact, manual, or held_state admission provenance
	Fact              *DeviceFactSummary // non-nil iff Source is RunSourceDeviceFact
	HeldState         *HeldStateEvidence // non-nil iff Source is RunSourceHeldState
	MatchedTriggers   []Trigger          // nonempty iff Source is RunSourceDeviceFact
	Reason            SkipReason
	ConditionDecision ConditionDecision
	SkippedAt         time.Time
}

// HistoryKind discriminates a retained Run from a retained Skip.
type HistoryKind string

const (
	// HistoryRun identifies a retained Run.
	HistoryRun HistoryKind = "run"
	// HistorySkip identifies a retained Skip.
	HistorySkip HistoryKind = "skip"
)

// HistoryEntry is exactly one retained history record.
type HistoryEntry struct {
	Kind HistoryKind
	Run  *Run
	Skip *Skip
}

// HistorySummary is the lightweight listing projection of one retained history record.
type HistorySummary struct {
	ID              string
	Kind            HistoryKind
	AutomationID    AutomationID
	AutomationName  string
	Revision        int64
	RecordedAt      time.Time
	Status          RunStatus          // set iff Kind is HistoryRun
	Reason          SkipReason         // set iff Kind is HistorySkip
	Source          RunSource          // admission provenance
	Fact            *DeviceFactSummary // nil for a manual Run or manual Skip
	HeldState       *HeldStateEvidence // set iff Source is RunSourceHeldState
	ConditionMode   ConditionDecisionMode
	ConditionResult *ConditionResult // set iff Conditions were evaluated
	BypassRequested bool
}

// ListAutomationsParams is an ascending-ID keyset position.
type ListAutomationsParams struct {
	AfterID *AutomationID
	Limit   int
}

// ListHistoryParams is a descending (recorded_at, id) keyset position scoped to one Automation.
type ListHistoryParams struct {
	AutomationID     AutomationID
	BeforeRecordedAt *time.Time
	BeforeID         *string
	Limit            int
}

// Page is one keyset page of automation-owned records.
type Page[T any] struct {
	Items   []T
	HasMore bool
}

const (
	// automationDefaultPageLimit is the page size used when a caller omits one.
	automationDefaultPageLimit = 50
	// automationMaximumPageLimit bounds one definition or history page.
	automationMaximumPageLimit = 200
)

// PageLimit resolves one requested page limit to the effective page size. An
// omitted limit becomes automationDefaultPageLimit; anything outside 1 through
// automationMaximumPageLimit is an [ErrInvalidAutomation].
func PageLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return automationDefaultPageLimit, nil
	case limit < 1 || limit > automationMaximumPageLimit:
		return 0, fmt.Errorf(
			"%w: page limit must be between 1 and %d",
			ErrInvalidAutomation, automationMaximumPageLimit,
		)
	default:
		return limit, nil
	}
}

// StepStart reserves and records one Step's Command identity before execution.
type StepStart struct {
	RunID         RunID
	Position      int
	CommandID     devices.CommandID
	CorrelationID devices.CorrelationID
}

// StepCompletion records one Step's established terminal outcome.
type StepCompletion struct {
	RunID             RunID
	Position          int
	Status            StepStatus
	VerifiedCommandID *devices.CommandID
	FailureCode       *string
}

// RunCompletion records one Run's established terminal state.
type RunCompletion struct {
	RunID       RunID
	Status      RunStatus
	FailureCode *string
}

// NewRunSnapshot builds one Run from a persisted definition record, an
// already-minted identity, and its committed admission Condition decision. It
// takes ownership of record.Definition and fact, copies matchedTriggerIDs, and
// starts every Step at not_attempted in definition order.
func NewRunSnapshot(
	record Record,
	runID RunID,
	source RunSource,
	fact *DeviceFactSummary,
	matchedTriggerIDs []TriggerID,
	conditionDecision ConditionDecision,
	admittedAt time.Time,
) Run {
	steps := make([]StepAttempt, len(record.Definition.Steps))
	for position, step := range record.Definition.Steps {
		steps[position] = StepAttempt{
			Position: position,
			StepID:   step.ID,
			Status:   StepNotAttempted,
		}
	}
	return Run{
		ID:                runID,
		AutomationID:      record.ID,
		AutomationName:    record.Definition.Name,
		Revision:          record.Revision,
		Snapshot:          record.Definition,
		Source:            source,
		Fact:              fact,
		MatchedTriggerIDs: append([]TriggerID(nil), matchedTriggerIDs...),
		ConditionDecision: conditionDecision,
		Status:            RunRunning,
		StartedAt:         admittedAt.UTC(),
		Steps:             steps,
	}
}

// EntityID reports the single Entity this Trigger constrains.
func (trigger Trigger) EntityID() devices.EntityID {
	switch trigger.Kind {
	case TriggerKindObservation:
		if trigger.Observation != nil {
			return trigger.Observation.EntityID
		}
	case TriggerKindEntityEvent:
		if trigger.EntityEvent != nil {
			return trigger.EntityEvent.EntityID
		}
	case TriggerKindHeldState:
		if trigger.HeldState != nil {
			return trigger.HeldState.EntityID
		}
	}
	return ""
}

// ValidateDeviceFact rejects a Device Fact whose family payload is missing, contradictory, malformed, or unidentifiable.
func ValidateDeviceFact(fact DeviceFact) error {
	switch fact.Family {
	case DeviceFactObservation:
		if fact.Observation == nil || fact.EntityEvent != nil {
			return invalidFact("observation family payload mismatch")
		}
		return validateObservationFact(*fact.Observation)
	case DeviceFactEntityEvent:
		if fact.EntityEvent == nil || fact.Observation != nil {
			return invalidFact("entity event family payload mismatch")
		}
		return validateEntityEventFact(*fact.EntityEvent)
	default:
		return invalidFact("unknown family")
	}
}

// ValidateDeviceFactSummary rejects an impossible retained Fact summary.
func ValidateDeviceFactSummary(summary DeviceFactSummary) error {
	if err := validateDeviceFactSummaryIdentity(summary); err != nil {
		return err
	}
	switch summary.Family {
	case DeviceFactObservation:
		return validateObservationFactSummary(summary)
	case DeviceFactEntityEvent:
		return validateEntityEventFactSummary(summary)
	default:
		return invalid("fact summary: unknown family")
	}
}

func validateDeviceFactSummaryIdentity(summary DeviceFactSummary) error {
	if _, err := devices.ParseDeviceFactID(string(summary.FactID)); err != nil {
		return invalid("fact summary: %s", err)
	}
	if _, err := devices.ParseEntityID(string(summary.EntityID)); err != nil {
		return invalid("fact summary: %s", err)
	}
	if summary.EmittedAt.IsZero() {
		return invalid("fact summary: emit time is required")
	}
	return nil
}

func validateObservationFactSummary(summary DeviceFactSummary) error {
	if summary.ObservationValue == nil {
		return invalid("fact summary: observation value is required")
	}
	if summary.PreviousStateValue != nil {
		if _, err := decodeJSONValue(json.RawMessage(summary.PreviousStateValue)); err != nil {
			return invalid("fact summary: previous state value must be exactly one JSON value")
		}
	}
	if summary.Variant != string(devices.DispositionApplied) &&
		summary.Variant != string(devices.DispositionUnchanged) {
		return invalid("fact summary: observation variant %q is not applied or unchanged", summary.Variant)
	}
	if _, err := devices.ParseObservationID(summary.CausationID); err != nil {
		return invalid("fact summary: %s", err)
	}
	return nil
}

func validateEntityEventFactSummary(summary DeviceFactSummary) error {
	if summary.ObservationValue != nil || summary.PreviousStateValue != nil {
		return invalid("fact summary: entity event summary carries observation values")
	}
	if !subjectSlugPattern.MatchString(summary.Variant) {
		return invalid("fact summary: entity event name is not a subject-safe slug")
	}
	if _, err := devices.ParseEntityEventID(summary.CausationID); err != nil {
		return invalid("fact summary: %s", err)
	}
	return nil
}

// ValidateTrigger rejects an impossible Trigger identity, family payload, or typed fields.
func ValidateTrigger(trigger Trigger) error {
	if _, err := ParseTriggerID(string(trigger.ID)); err != nil {
		return err
	}
	switch trigger.Kind {
	case TriggerKindObservation:
		if trigger.Observation == nil || trigger.EntityEvent != nil || trigger.HeldState != nil {
			return invalid("trigger %q: observation family payload mismatch", trigger.ID)
		}
		return validateObservationTrigger(*trigger.Observation)
	case TriggerKindEntityEvent:
		if trigger.EntityEvent == nil || trigger.Observation != nil || trigger.HeldState != nil {
			return invalid("trigger %q: entity event family payload mismatch", trigger.ID)
		}
		return validateEntityEventTrigger(*trigger.EntityEvent)
	case TriggerKindHeldState:
		if trigger.HeldState == nil || trigger.Observation != nil || trigger.EntityEvent != nil {
			return invalid("trigger %q: held state family payload mismatch", trigger.ID)
		}
		return validateHeldStateTrigger(*trigger.HeldState)
	default:
		return invalid("trigger %q: unknown kind %q", trigger.ID, trigger.Kind)
	}
}

// ValidateRun rejects an impossible Run identity, provenance, snapshot, status, timestamps, or Steps.
func ValidateRun(run Run) error {
	if _, err := ParseRunID(string(run.ID)); err != nil {
		return err
	}
	if _, err := ParseAutomationID(string(run.AutomationID)); err != nil {
		return err
	}
	if run.Revision < 1 {
		return invalid("run %q: revision must be at least 1", run.ID)
	}
	if _, err := NormalizeDefinition(run.Snapshot); err != nil {
		return invalid("run %q: definition snapshot is invalid", run.ID)
	}
	if err := validateRunProvenance(run); err != nil {
		return err
	}
	if err := ValidateRunConditionDecision(run); err != nil {
		return err
	}
	if run.StartedAt.IsZero() {
		return invalid("run %q: start time is required", run.ID)
	}
	if err := validateRunStatus(run); err != nil {
		return err
	}
	if len(run.Steps) != len(run.Snapshot.Steps) {
		return invalid("run %q: step count does not match the definition snapshot", run.ID)
	}
	for position, step := range run.Steps {
		if err := validateStepAttempt(run, position, step); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSkip rejects a Skip whose identity, provenance, evidence, reason,
// Condition decision, or timestamp is impossible.
func ValidateSkip(skip Skip) error {
	if _, err := ParseSkipID(string(skip.ID)); err != nil {
		return err
	}
	if _, err := ParseAutomationID(string(skip.AutomationID)); err != nil {
		return err
	}
	if skip.Revision < 1 {
		return invalid("skip %q: revision must be at least 1", skip.ID)
	}
	if err := validateSkipProvenance(skip); err != nil {
		return err
	}
	switch skip.Reason {
	case SkipBusy, SkipStaleFact, SkipConditionsFalse, SkipConditionsUnknown:
	default:
		return invalid("skip %q: unknown reason %q", skip.ID, skip.Reason)
	}
	if skip.SkippedAt.IsZero() {
		return invalid("skip %q: skip time is required", skip.ID)
	}
	return ValidateSkipConditionDecision(skip)
}

// validateSkipProvenance checks the Source-discriminated Skip family: complete
// Fact evidence with matched Triggers for a device-fact Skip, and neither for a
// manual Skip.
func validateSkipProvenance(skip Skip) error {
	switch skip.Source {
	case RunSourceDeviceFact:
		if err := validateDeviceFactSkipProvenance(skip); err != nil {
			return err
		}
	case RunSourceManual:
		if err := validateManualSkipProvenance(skip); err != nil {
			return err
		}
	case RunSourceHeldState:
		if err := validateHeldStateSkipProvenance(skip); err != nil {
			return err
		}
	default:
		return invalid("skip %q: unknown source %q", skip.ID, skip.Source)
	}
	seen := make(map[TriggerID]bool, len(skip.MatchedTriggers))
	for _, trigger := range skip.MatchedTriggers {
		if err := ValidateTrigger(trigger); err != nil {
			return err
		}
		if seen[trigger.ID] {
			return invalid("skip %q: matched trigger IDs must be unique", skip.ID)
		}
		seen[trigger.ID] = true
	}
	return nil
}

func validateDeviceFactSkipProvenance(skip Skip) error {
	if skip.Fact == nil || skip.HeldState != nil {
		return invalid("skip %q: device fact Skip requires Fact evidence", skip.ID)
	}
	if err := ValidateDeviceFactSummary(*skip.Fact); err != nil {
		return err
	}
	if len(skip.MatchedTriggers) == 0 {
		return invalid("skip %q: device fact Skip requires matched triggers", skip.ID)
	}
	return nil
}

func validateManualSkipProvenance(skip Skip) error {
	if skip.Fact != nil || skip.HeldState != nil {
		return invalid("skip %q: manual Skip carries admission evidence", skip.ID)
	}
	if len(skip.MatchedTriggers) != 0 {
		return invalid("skip %q: manual Skip carries matched triggers", skip.ID)
	}
	if skip.Reason != SkipConditionsFalse && skip.Reason != SkipConditionsUnknown {
		return invalid("skip %q: manual Skip reason must be a condition outcome", skip.ID)
	}
	return nil
}

func validateHeldStateSkipProvenance(skip Skip) error {
	if skip.Fact != nil || skip.HeldState == nil || len(skip.MatchedTriggers) != 1 {
		return invalid("skip %q: held-state Skip requires one matched Trigger and hold evidence, without Fact", skip.ID)
	}
	if err := validateHeldStateEvidence(*skip.HeldState); err != nil {
		return err
	}
	if skip.Reason == SkipStaleFact {
		return invalid("skip %q: held-state Skip cannot be stale_fact", skip.ID)
	}
	if skip.MatchedTriggers[0].ID != skip.HeldState.TriggerID || skip.MatchedTriggers[0].Kind != TriggerKindHeldState {
		return invalid("skip %q: held-state evidence does not match its Trigger", skip.ID)
	}
	return nil
}

func validateHeldStateEvidence(evidence HeldStateEvidence) error {
	if _, err := ParseTriggerID(string(evidence.TriggerID)); err != nil {
		return err
	}
	if evidence.StartedAt.IsZero() || evidence.DueAt.IsZero() || !evidence.DueAt.After(evidence.StartedAt) {
		return invalid("held-state evidence requires a due time after its start time")
	}
	return nil
}

func validateObservationFact(fact ObservationFact) error {
	if _, err := devices.ParseDeviceFactID(string(fact.FactID)); err != nil {
		return invalidFact(err.Error())
	}
	if _, err := devices.ParseObservationID(string(fact.ObservationID)); err != nil {
		return invalidFact(err.Error())
	}
	if _, err := devices.ParseEntityID(string(fact.EntityID)); err != nil {
		return invalidFact(err.Error())
	}
	if !acceptedObservationDisposition(fact.Disposition) {
		return invalidFact(fmt.Sprintf("disposition %q is not an accepted observation", fact.Disposition))
	}
	if len(fact.Value) == 0 {
		return invalidFact("observation value is required")
	}
	if fact.PreviousValue != nil {
		if _, err := decodeJSONValue(json.RawMessage(fact.PreviousValue)); err != nil {
			return invalidFact("previous observation value must be exactly one JSON value")
		}
	}
	if fact.EmittedAt.IsZero() {
		return invalidFact("emit time is required")
	}
	return nil
}

func validateEntityEventFact(fact EntityEventFact) error {
	if _, err := devices.ParseDeviceFactID(string(fact.FactID)); err != nil {
		return invalidFact(err.Error())
	}
	if _, err := devices.ParseEntityEventID(string(fact.EventID)); err != nil {
		return invalidFact(err.Error())
	}
	if _, err := devices.ParseEntityID(string(fact.EntityID)); err != nil {
		return invalidFact(err.Error())
	}
	if !subjectSlugPattern.MatchString(string(fact.Name)) {
		return invalidFact("event name is not a subject-safe slug")
	}
	if fact.EmittedAt.IsZero() {
		return invalidFact("emit time is required")
	}
	return nil
}

func validateObservationTrigger(trigger ObservationTrigger) error {
	if _, err := devices.ParseEntityID(string(trigger.EntityID)); err != nil {
		return fmt.Errorf("%w: observation trigger entity: %w", ErrInvalidAutomation, err)
	}
	if len(trigger.Dispositions) == 0 || len(trigger.Dispositions) > automationDispositionMaxCount {
		return fmt.Errorf(
			"%w: observation trigger needs 1 to %d dispositions",
			ErrInvalidAutomation, automationDispositionMaxCount,
		)
	}
	seen := make(map[devices.ObservationDisposition]bool, len(trigger.Dispositions))
	for _, disposition := range trigger.Dispositions {
		if !acceptedObservationDisposition(disposition) {
			return fmt.Errorf(
				"%w: observation trigger disposition %q is not applied or unchanged",
				ErrInvalidAutomation, disposition,
			)
		}
		if seen[disposition] {
			return fmt.Errorf("%w: observation trigger dispositions must be unique", ErrInvalidAutomation)
		}
		seen[disposition] = true
	}
	if len(trigger.Comparisons) > automationComparisonMaxCount {
		return fmt.Errorf(
			"%w: observation trigger has more than %d comparisons",
			ErrInvalidAutomation, automationComparisonMaxCount,
		)
	}
	if len(trigger.PreviousComparisons) > automationComparisonMaxCount {
		return fmt.Errorf(
			"%w: observation trigger has more than %d previous comparisons",
			ErrInvalidAutomation, automationComparisonMaxCount,
		)
	}
	for _, comparison := range trigger.PreviousComparisons {
		if err := ValidateObservationComparison(comparison); err != nil {
			return err
		}
	}
	for _, comparison := range trigger.Comparisons {
		if err := ValidateObservationComparison(comparison); err != nil {
			return err
		}
	}
	return nil
}

func validateEntityEventTrigger(trigger EntityEventTrigger) error {
	if _, err := devices.ParseEntityID(string(trigger.EntityID)); err != nil {
		return fmt.Errorf("%w: entity event trigger entity: %w", ErrInvalidAutomation, err)
	}
	if !subjectSlugPattern.MatchString(string(trigger.EventName)) {
		return fmt.Errorf("%w: entity event trigger name is not a subject-safe slug", ErrInvalidAutomation)
	}
	return nil
}

func validateHeldStateTrigger(trigger HeldStateTrigger) error {
	if _, err := devices.ParseEntityID(string(trigger.EntityID)); err != nil {
		return fmt.Errorf("%w: held state trigger entity: %w", ErrInvalidAutomation, err)
	}
	if len(trigger.Comparisons) < 1 || len(trigger.Comparisons) > automationComparisonMaxCount {
		return fmt.Errorf(
			"%w: held state trigger needs 1 to %d comparisons",
			ErrInvalidAutomation, automationComparisonMaxCount,
		)
	}
	if trigger.ForSeconds < 1 || trigger.ForSeconds > 2_592_000 {
		return fmt.Errorf("%w: held state duration must be between 1 and 2592000 seconds", ErrInvalidAutomation)
	}
	for _, comparison := range trigger.Comparisons {
		if err := ValidateObservationComparison(comparison); err != nil {
			return err
		}
	}
	return nil
}

func validateRunProvenance(run Run) error {
	switch run.Source {
	case RunSourceDeviceFact:
		if err := validateDeviceFactRunProvenance(run); err != nil {
			return err
		}
	case RunSourceManual:
		if err := validateManualRunProvenance(run); err != nil {
			return err
		}
	case RunSourceHeldState:
		if err := validateHeldStateRunProvenance(run); err != nil {
			return err
		}
	default:
		return invalid("run %q: unknown source %q", run.ID, run.Source)
	}
	if invalidMatchedTriggerCount(run) {
		return invalid("run %q: matched trigger IDs must be empty iff the Run is manual", run.ID)
	}
	if err := validateMatchedTriggerIDs(run); err != nil {
		return err
	}
	if run.Source == RunSourceHeldState {
		if run.MatchedTriggerIDs[0] != run.HeldState.TriggerID {
			return invalid("run %q: held-state evidence trigger does not match admission trigger", run.ID)
		}
		for _, trigger := range run.Snapshot.Triggers {
			if trigger.ID == run.HeldState.TriggerID && trigger.Kind == TriggerKindHeldState {
				return nil
			}
		}
		return invalid("run %q: held-state evidence trigger is absent from snapshot", run.ID)
	}
	return nil
}

func validateDeviceFactRunProvenance(run Run) error {
	if run.Fact == nil || run.HeldState != nil {
		return invalid("run %q: device fact Run requires Fact evidence", run.ID)
	}
	return ValidateDeviceFactSummary(*run.Fact)
}

func validateManualRunProvenance(run Run) error {
	if run.Fact != nil || run.HeldState != nil {
		return invalid("run %q: manual Run carries admission evidence", run.ID)
	}
	return nil
}

func validateHeldStateRunProvenance(run Run) error {
	if run.Fact != nil || run.HeldState == nil {
		return invalid("run %q: held-state Run requires hold evidence and no Fact", run.ID)
	}
	return validateHeldStateEvidence(*run.HeldState)
}

func invalidMatchedTriggerCount(run Run) bool {
	return run.Source == RunSourceManual && len(run.MatchedTriggerIDs) != 0 ||
		run.Source == RunSourceDeviceFact && len(run.MatchedTriggerIDs) == 0 ||
		run.Source == RunSourceHeldState && len(run.MatchedTriggerIDs) != 1
}

func validateMatchedTriggerIDs(run Run) error {
	seen := make(map[TriggerID]bool, len(run.MatchedTriggerIDs))
	next := 0
	for _, trigger := range run.Snapshot.Triggers {
		if seen[trigger.ID] {
			return invalid("run %q: snapshot trigger IDs must be unique", run.ID)
		}
		seen[trigger.ID] = true
		if next < len(run.MatchedTriggerIDs) && trigger.ID == run.MatchedTriggerIDs[next] {
			next++
		}
	}
	if next != len(run.MatchedTriggerIDs) {
		return invalid("run %q: matched trigger IDs must be a subset of the snapshot in definition order", run.ID)
	}
	return nil
}

func validateRunStatus(run Run) error {
	switch run.Status {
	case RunRunning, RunSucceeded, RunFailed, RunInterrupted:
	default:
		return invalid("run %q: unknown status %q", run.ID, run.Status)
	}
	if run.Status == RunRunning && run.CompletedAt != nil {
		return invalid("run %q: running Run carries a completion time", run.ID)
	}
	if run.Status != RunRunning && run.CompletedAt == nil {
		return invalid("run %q: terminal Run requires a completion time", run.ID)
	}
	if (run.Status == RunRunning || run.Status == RunSucceeded) && run.FailureCode != nil {
		return invalid("run %q: non-failing Run carries a failure code", run.ID)
	}
	if (run.Status == RunFailed || run.Status == RunInterrupted) && run.FailureCode == nil {
		return invalid("run %q: failed or interrupted Run requires a failure code", run.ID)
	}
	return nil
}

func validateStepAttempt(run Run, position int, step StepAttempt) error {
	if step.Position != position {
		return invalid("run %q: step positions must be contiguous and zero-based", run.ID)
	}
	if step.StepID != run.Snapshot.Steps[position].ID {
		return invalid("run %q: step identity does not match the definition snapshot", run.ID)
	}
	switch step.Status {
	case StepNotAttempted:
		if step.ReservedCommandID != nil || step.ReservedCorrelationID != nil ||
			step.VerifiedCommandID != nil || step.StartedAt != nil || step.CompletedAt != nil {
			return invalid("run %q: not attempted step carries attempt evidence", run.ID)
		}
	case StepRunning:
		if step.ReservedCommandID == nil || step.ReservedCorrelationID == nil ||
			step.StartedAt == nil || step.CompletedAt != nil || step.VerifiedCommandID != nil {
			return invalid("run %q: running step requires reserved identities and no verification", run.ID)
		}
	case StepSatisfied, StepDispatched:
		if step.ReservedCommandID == nil || step.ReservedCorrelationID == nil ||
			step.StartedAt == nil || step.CompletedAt == nil || step.VerifiedCommandID == nil {
			return invalid("run %q: successful step requires verified Command evidence", run.ID)
		}
	case StepFailed, StepInterrupted:
		if step.StartedAt == nil || step.CompletedAt == nil || step.FailureCode == nil {
			return invalid("run %q: terminal failure requires start, completion, and a failure code", run.ID)
		}
	default:
		return invalid("run %q: unknown step status %q", run.ID, step.Status)
	}
	return nil
}

func invalidFact(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidDeviceFact, message)
}

// acceptedObservationDisposition reports whether one Observation disposition is
// durable Device Fact evidence; rejected and duplicate Observations are recorded
// history but never facts.
func acceptedObservationDisposition(disposition devices.ObservationDisposition) bool {
	switch disposition {
	case devices.DispositionApplied, devices.DispositionUnchanged:
		return true
	case devices.DispositionRejected, devices.DispositionDuplicate:
		return false
	default:
		return false
	}
}

// invalid reports one invalid Automation value behind the shared class prefix.
func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidAutomation}, args...)...)
}
