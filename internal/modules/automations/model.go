package automations

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationID is the durable identity of one Automation definition: a
// canonical aut_-prefixed UUIDv7.
type AutomationID string

// AutomationRunID is the durable identity of one Automation Run: a canonical
// arn_-prefixed UUIDv7. The arn_ prefix deliberately differs from the adapter
// runtime identity, which uses run_.
type AutomationRunID string

// AutomationSkipID is the durable identity of one recorded Skip: a canonical
// ask_-prefixed UUIDv7.
type AutomationSkipID string

// TriggerID is an author-supplied subject-safe slug identifying one Trigger
// within its own definition.
type TriggerID string

// StepID is an author-supplied subject-safe slug identifying one Step within
// its own definition.
type StepID string

// TriggerKind is the closed discriminated family of one Automation Trigger.
type TriggerKind string

const (
	// TriggerKindObservation matches one accepted Observation Fact.
	TriggerKindObservation TriggerKind = "observation"
	// TriggerKindEntityEvent matches one accepted Entity Event Fact.
	TriggerKindEntityEvent TriggerKind = "entity_event"
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

// ObservationComparison is one typed comparison against an Observation Fact's
// data.value. Pointer is an RFC 6901 JSON Pointer relative to data.value; the
// empty pointer selects the whole value. Operand is exactly one normalized JSON
// value.
type ObservationComparison struct {
	Pointer  string
	Operator ComparisonOperator
	Operand  json.RawMessage
}

// ObservationTrigger matches one Observation Fact by exact Entity ID, a
// non-empty disposition set, and zero to eight comparisons that must all match.
type ObservationTrigger struct {
	EntityID     devices.EntityID
	Dispositions []devices.ObservationDisposition
	Comparisons  []ObservationComparison
}

// EntityEventTrigger matches one Entity Event Fact by exact Entity ID and exact
// event name.
type EntityEventTrigger struct {
	EntityID  devices.EntityID
	EventName devices.EntityEventName
}

// AutomationTrigger is one identified typed Trigger. Exactly one family payload
// is set: Observation iff Kind is TriggerKindObservation, EntityEvent iff Kind
// is TriggerKindEntityEvent.
type AutomationTrigger struct {
	ID          TriggerID
	Kind        TriggerKind
	Observation *ObservationTrigger
	EntityEvent *EntityEventTrigger
}

// AutomationStep is one identified, execution-ordered Command: exact Entity,
// Operation, and normalized static JSON parameters.
type AutomationStep struct {
	ID            StepID
	EntityID      devices.EntityID
	OperationName devices.OperationName
	Parameters    devices.CommandParameters
}

// AutomationDefinition is one complete, normalized Automation document.
type AutomationDefinition struct {
	Name     string // 1–200 runes, trimmed; not unique
	Enabled  bool
	Triggers []AutomationTrigger // 1–32, IDs unique
	Steps    []AutomationStep    // 1–32, IDs unique and execution ordered
}

// AutomationRecord is one live definition at its current revision. Revision
// starts at 1 and increments by one on replacement.
type AutomationRecord struct {
	ID         AutomationID
	Revision   int64
	Definition AutomationDefinition
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// DeviceFactFamily is the closed family of Device Fact evidence an automation
// admits. The tokens match the durable history vocabulary.
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

// DeviceFactSummary is the immutable evidence retained in history so a Run or
// Skip stays explainable after Device Fact and Observation history are pruned.
// ObservationValue is non-nil only for Observation facts.
type DeviceFactSummary struct {
	FactID           devices.DeviceFactID
	Family           DeviceFactFamily
	EntityID         devices.EntityID
	Variant          string // observation disposition or Entity Event name
	CausationID      string // obs_ or evt_
	ObservationValue devices.Value
	EmittedAt        time.Time
}

// AdmissionOutcome reports what one Device Fact admission decided.
type AdmissionOutcome struct {
	MatchedAutomations int
	StartedRuns        int
	RecordedSkips      int
	DuplicateOutcomes  int
}

// AdmissionResult contains committed admission outcomes. Register StartedRuns
// workers and log Skips only after the transaction commits.
type AdmissionResult struct {
	Outcome     AdmissionOutcome
	StartedRuns []AutomationRun
	Skips       []AdmissionSkip
}

// AdmissionSkip carries committed Skip identity and reason for logging,
// without Fact values or definition snapshots.
type AdmissionSkip struct {
	SkipID       AutomationSkipID
	AutomationID AutomationID
	Revision     int64
	Reason       AutomationSkipReason
	FactID       devices.DeviceFactID
	Family       DeviceFactFamily
	Variant      string
}

// RunSource distinguishes how a Run was admitted.
type RunSource string

const (
	// RunSourceDeviceFact marks a Run admitted by a matching Device Fact.
	RunSourceDeviceFact RunSource = "device_fact"
	// RunSourceManual marks a Run admitted by an operator request.
	RunSourceManual RunSource = "manual"
)

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

// AutomationStepAttempt records an ordered Step's execution state.
// Reserved identities are private; only verified Command links are exposed.
type AutomationStepAttempt struct {
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

// AutomationRun tracks execution of an immutable definition snapshot,
// retaining admission provenance and ordered Step attempts.
type AutomationRun struct {
	ID                AutomationRunID
	AutomationID      AutomationID
	AutomationName    string
	Revision          int64
	Snapshot          AutomationDefinition
	Source            RunSource
	Fact              *DeviceFactSummary // non-nil iff Source is RunSourceDeviceFact
	MatchedTriggerIDs []TriggerID        // empty iff Source is RunSourceManual
	Status            RunStatus
	FailureCode       *string
	StartedAt         time.Time
	CompletedAt       *time.Time
	Steps             []AutomationStepAttempt
}

// AutomationSkipReason identifies why a matching Fact did not start a Run.
type AutomationSkipReason string

const (
	// AutomationSkipBusy marks a matching Automation that already had a running Run.
	AutomationSkipBusy AutomationSkipReason = "automation_busy"
	// AutomationSkipStaleFact marks a matching Fact older than the freshness bound.
	AutomationSkipStaleFact AutomationSkipReason = "stale_fact"
)

// AutomationSkip is one recorded non-Run outcome with immutable matching
// Trigger snapshots.
type AutomationSkip struct {
	ID              AutomationSkipID
	AutomationID    AutomationID
	AutomationName  string
	Revision        int64
	Fact            DeviceFactSummary
	MatchedTriggers []AutomationTrigger
	Reason          AutomationSkipReason
	SkippedAt       time.Time
}

// AutomationHistoryKind discriminates a retained Run from a retained Skip.
type AutomationHistoryKind string

const (
	// AutomationHistoryRun identifies a retained Run.
	AutomationHistoryRun AutomationHistoryKind = "run"
	// AutomationHistorySkip identifies a retained Skip.
	AutomationHistorySkip AutomationHistoryKind = "skip"
)

// AutomationHistoryEntry is exactly one retained history record.
type AutomationHistoryEntry struct {
	Kind AutomationHistoryKind
	Run  *AutomationRun
	Skip *AutomationSkip
}

// AutomationHistorySummary is the lightweight listing projection of one
// retained history record.
type AutomationHistorySummary struct {
	ID             string
	Kind           AutomationHistoryKind
	AutomationID   AutomationID
	AutomationName string
	Revision       int64
	RecordedAt     time.Time
	Status         RunStatus            // set iff Kind is AutomationHistoryRun
	Reason         AutomationSkipReason // set iff Kind is AutomationHistorySkip
	Fact           *DeviceFactSummary   // nil iff a manual Run
}

// ListAutomationsParams is an ascending-ID keyset position.
type ListAutomationsParams struct {
	AfterID *AutomationID
	Limit   int
}

// ListHistoryParams is a descending (recorded_at, id) keyset position scoped to
// one Automation, including one that has been hard-deleted.
type ListHistoryParams struct {
	AutomationID     AutomationID
	BeforeRecordedAt *time.Time
	BeforeID         *string
	Limit            int
}

// AutomationPage is one keyset page of automation-owned records.
type AutomationPage[T any] struct {
	Items   []T
	HasMore bool
}

const (
	// automationDefaultPageLimit is the page size used when a caller omits one.
	automationDefaultPageLimit = 50
	// automationMaximumPageLimit bounds one definition or history page.
	automationMaximumPageLimit = 200
)

// AutomationPageLimit resolves one requested page limit to the effective page
// size: an omitted limit becomes automationDefaultPageLimit, and anything
// outside 1 through automationMaximumPageLimit is an [ErrInvalidAutomation].
// Persistence applies it so a direct repository caller gets the same bounded
// page as one that arrived through the HTTP request schema's matching bounds.
func AutomationPageLimit(limit int) (int, error) {
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
	RunID         AutomationRunID
	Position      int
	CommandID     devices.CommandID
	CorrelationID devices.CorrelationID
}

// StepCompletion records one Step's established terminal outcome.
type StepCompletion struct {
	RunID             AutomationRunID
	Position          int
	Status            StepStatus
	VerifiedCommandID *devices.CommandID
	FailureCode       *string
}

// RunCompletion records one Run's established terminal state.
type RunCompletion struct {
	RunID       AutomationRunID
	Status      RunStatus
	FailureCode *string
}

// NewAutomationRunSnapshot builds one Run from a persisted definition record and
// an identity the caller already minted. It takes ownership of record.Definition
// and fact, which callers must not mutate afterward, and copies matchedTriggerIDs.
// Every Step starts at not_attempted in definition order. Persistence mints the
// Run identity inside its admission transaction and then writes this snapshot;
// construction performs no reads or writes of its own.
func NewAutomationRunSnapshot(
	record AutomationRecord,
	runID AutomationRunID,
	source RunSource,
	fact *DeviceFactSummary,
	matchedTriggerIDs []TriggerID,
	admittedAt time.Time,
) AutomationRun {
	steps := make([]AutomationStepAttempt, len(record.Definition.Steps))
	for position, step := range record.Definition.Steps {
		steps[position] = AutomationStepAttempt{
			Position: position,
			StepID:   step.ID,
			Status:   StepNotAttempted,
		}
	}
	return AutomationRun{
		ID:                runID,
		AutomationID:      record.ID,
		AutomationName:    record.Definition.Name,
		Revision:          record.Revision,
		Snapshot:          record.Definition,
		Source:            source,
		Fact:              fact,
		MatchedTriggerIDs: append([]TriggerID(nil), matchedTriggerIDs...),
		Status:            RunRunning,
		StartedAt:         admittedAt.UTC(),
		Steps:             steps,
	}
}

// EntityID reports the single Entity this Trigger constrains.
func (trigger AutomationTrigger) EntityID() devices.EntityID {
	switch trigger.Kind {
	case TriggerKindObservation:
		if trigger.Observation != nil {
			return trigger.Observation.EntityID
		}
	case TriggerKindEntityEvent:
		if trigger.EntityEvent != nil {
			return trigger.EntityEvent.EntityID
		}
	}
	return ""
}

// ValidateDeviceFact rejects a Device Fact whose family payload is missing,
// contradictory, malformed, or unidentifiable.
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

// ValidateDeviceFactSummary rejects a retained Fact summary whose family,
// identity, variant, causation, or timestamp is impossible.
func ValidateDeviceFactSummary(summary DeviceFactSummary) error {
	if _, err := devices.ParseDeviceFactID(string(summary.FactID)); err != nil {
		return invalidSummary(err)
	}
	if _, err := devices.ParseEntityID(string(summary.EntityID)); err != nil {
		return invalidSummary(err)
	}
	if summary.EmittedAt.IsZero() {
		return invalidSummary(errors.New("emit time is required"))
	}
	switch summary.Family {
	case DeviceFactObservation:
		if summary.ObservationValue == nil {
			return invalidSummary(errors.New("observation value is required"))
		}
		if summary.Variant != string(devices.DispositionApplied) &&
			summary.Variant != string(devices.DispositionUnchanged) {
			return invalidSummary(fmt.Errorf("observation variant %q is not applied or unchanged", summary.Variant))
		}
		if _, err := devices.ParseObservationID(summary.CausationID); err != nil {
			return invalidSummary(err)
		}
	case DeviceFactEntityEvent:
		if summary.ObservationValue != nil {
			return invalidSummary(errors.New("entity event summary carries an observation value"))
		}
		if !subjectSlugPattern.MatchString(summary.Variant) {
			return invalidSummary(errors.New("entity event name is not a subject-safe slug"))
		}
		if _, err := devices.ParseEntityEventID(summary.CausationID); err != nil {
			return invalidSummary(err)
		}
	default:
		return invalidSummary(errors.New("unknown family"))
	}
	return nil
}

// ValidateAutomationTrigger rejects a Trigger whose identity, family payload,
// or typed fields are impossible.
func ValidateAutomationTrigger(trigger AutomationTrigger) error {
	if _, err := ParseTriggerID(string(trigger.ID)); err != nil {
		return err
	}
	switch trigger.Kind {
	case TriggerKindObservation:
		if trigger.Observation == nil || trigger.EntityEvent != nil {
			return invalidTrigger(trigger.ID, "observation family payload mismatch")
		}
		return validateObservationTrigger(*trigger.Observation)
	case TriggerKindEntityEvent:
		if trigger.EntityEvent == nil || trigger.Observation != nil {
			return invalidTrigger(trigger.ID, "entity event family payload mismatch")
		}
		return validateEntityEventTrigger(*trigger.EntityEvent)
	default:
		return invalidTrigger(trigger.ID, fmt.Sprintf("unknown kind %q", trigger.Kind))
	}
}

// ValidateAutomationRun rejects a Run whose identity, provenance, snapshot,
// status, timestamps, or ordered Steps are impossible.
func ValidateAutomationRun(run AutomationRun) error {
	if _, err := ParseAutomationRunID(string(run.ID)); err != nil {
		return err
	}
	if _, err := ParseAutomationID(string(run.AutomationID)); err != nil {
		return err
	}
	if run.Revision < 1 {
		return invalidRun(run.ID, "revision must be at least 1")
	}
	if _, err := NormalizeAutomationDefinition(run.Snapshot); err != nil {
		return invalidRun(run.ID, "definition snapshot is invalid")
	}
	if err := validateRunProvenance(run); err != nil {
		return err
	}
	if run.StartedAt.IsZero() {
		return invalidRun(run.ID, "start time is required")
	}
	if err := validateRunStatus(run); err != nil {
		return err
	}
	if len(run.Steps) != len(run.Snapshot.Steps) {
		return invalidRun(run.ID, "step count does not match the definition snapshot")
	}
	for position, step := range run.Steps {
		if err := validateStepAttempt(run, position, step); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAutomationSkip rejects a Skip whose identity, Fact evidence, matched
// Trigger snapshots, reason, or timestamp is impossible.
func ValidateAutomationSkip(skip AutomationSkip) error {
	if _, err := ParseAutomationSkipID(string(skip.ID)); err != nil {
		return err
	}
	if _, err := ParseAutomationID(string(skip.AutomationID)); err != nil {
		return err
	}
	if skip.Revision < 1 {
		return invalidSkip(skip.ID, "revision must be at least 1")
	}
	if err := ValidateDeviceFactSummary(skip.Fact); err != nil {
		return err
	}
	if len(skip.MatchedTriggers) == 0 {
		return invalidSkip(skip.ID, "matched triggers are required")
	}
	seen := make(map[TriggerID]bool, len(skip.MatchedTriggers))
	for _, trigger := range skip.MatchedTriggers {
		if err := ValidateAutomationTrigger(trigger); err != nil {
			return err
		}
		if seen[trigger.ID] {
			return invalidSkip(skip.ID, "matched trigger IDs must be unique")
		}
		seen[trigger.ID] = true
	}
	switch skip.Reason {
	case AutomationSkipBusy, AutomationSkipStaleFact:
	default:
		return invalidSkip(skip.ID, fmt.Sprintf("unknown reason %q", skip.Reason))
	}
	if skip.SkippedAt.IsZero() {
		return invalidSkip(skip.ID, "skip time is required")
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

func validateRunProvenance(run AutomationRun) error {
	switch run.Source {
	case RunSourceDeviceFact:
		if run.Fact == nil {
			return invalidRun(run.ID, "device fact Run requires Fact evidence")
		}
		if err := ValidateDeviceFactSummary(*run.Fact); err != nil {
			return err
		}
	case RunSourceManual:
		if run.Fact != nil {
			return invalidRun(run.ID, "manual Run carries Fact evidence")
		}
	default:
		return invalidRun(run.ID, fmt.Sprintf("unknown source %q", run.Source))
	}
	if (run.Source == RunSourceManual) != (len(run.MatchedTriggerIDs) == 0) {
		return invalidRun(run.ID, "matched trigger IDs must be empty iff the Run is manual")
	}
	return validateMatchedTriggerIDs(run)
}

func validateMatchedTriggerIDs(run AutomationRun) error {
	seen := make(map[TriggerID]bool, len(run.MatchedTriggerIDs))
	next := 0
	for _, trigger := range run.Snapshot.Triggers {
		if seen[trigger.ID] {
			return invalidRun(run.ID, "snapshot trigger IDs must be unique")
		}
		seen[trigger.ID] = true
		if next < len(run.MatchedTriggerIDs) && trigger.ID == run.MatchedTriggerIDs[next] {
			next++
		}
	}
	if next != len(run.MatchedTriggerIDs) {
		return invalidRun(run.ID, "matched trigger IDs must be a subset of the snapshot in definition order")
	}
	return nil
}

func validateRunStatus(run AutomationRun) error {
	switch run.Status {
	case RunRunning, RunSucceeded, RunFailed, RunInterrupted:
	default:
		return invalidRun(run.ID, fmt.Sprintf("unknown status %q", run.Status))
	}
	if run.Status == RunRunning && run.CompletedAt != nil {
		return invalidRun(run.ID, "running Run carries a completion time")
	}
	if run.Status != RunRunning && run.CompletedAt == nil {
		return invalidRun(run.ID, "terminal Run requires a completion time")
	}
	if (run.Status == RunRunning || run.Status == RunSucceeded) && run.FailureCode != nil {
		return invalidRun(run.ID, "non-failing Run carries a failure code")
	}
	if (run.Status == RunFailed || run.Status == RunInterrupted) && run.FailureCode == nil {
		return invalidRun(run.ID, "failed or interrupted Run requires a failure code")
	}
	return nil
}

func validateStepAttempt(run AutomationRun, position int, step AutomationStepAttempt) error {
	if step.Position != position {
		return invalidRun(run.ID, "step positions must be contiguous and zero-based")
	}
	if step.StepID != run.Snapshot.Steps[position].ID {
		return invalidRun(run.ID, "step identity does not match the definition snapshot")
	}
	switch step.Status {
	case StepNotAttempted:
		if step.ReservedCommandID != nil || step.ReservedCorrelationID != nil ||
			step.VerifiedCommandID != nil || step.StartedAt != nil || step.CompletedAt != nil {
			return invalidRun(run.ID, "not attempted step carries attempt evidence")
		}
	case StepRunning:
		if step.ReservedCommandID == nil || step.ReservedCorrelationID == nil ||
			step.StartedAt == nil || step.CompletedAt != nil || step.VerifiedCommandID != nil {
			return invalidRun(run.ID, "running step requires reserved identities and no verification")
		}
	case StepSatisfied, StepDispatched:
		if step.ReservedCommandID == nil || step.ReservedCorrelationID == nil ||
			step.StartedAt == nil || step.CompletedAt == nil || step.VerifiedCommandID == nil {
			return invalidRun(run.ID, "successful step requires verified Command evidence")
		}
	case StepFailed, StepInterrupted:
		if step.StartedAt == nil || step.CompletedAt == nil || step.FailureCode == nil {
			return invalidRun(run.ID, "terminal failure requires start, completion, and a failure code")
		}
	default:
		return invalidRun(run.ID, fmt.Sprintf("unknown step status %q", step.Status))
	}
	return nil
}

func invalidFact(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidDeviceFact, message)
}

// acceptedObservationDisposition reports whether one Observation disposition is
// durable Device Fact evidence. Rejected and duplicate Observations are recorded
// history but never facts, so they never match a Trigger.
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

func invalidSummary(err error) error {
	return fmt.Errorf("%w: fact summary: %w", ErrInvalidAutomation, err)
}

func invalidTrigger(id TriggerID, message string) error {
	return fmt.Errorf("%w: trigger %q: %s", ErrInvalidAutomation, id, message)
}

func invalidRun(id AutomationRunID, message string) error {
	return fmt.Errorf("%w: run %q: %s", ErrInvalidAutomation, id, message)
}

func invalidSkip(id AutomationSkipID, message string) error {
	return fmt.Errorf("%w: skip %q: %s", ErrInvalidAutomation, id, message)
}
