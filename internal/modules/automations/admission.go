package automations

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationFactMaximumAge is the fixed freshness limit measured from emitted_at.
// Older matching Facts record stale_fact Skips instead of starting Runs.
const AutomationFactMaximumAge = 30 * time.Second

// StartManualRun admits one Run from the current definition snapshot, even when
// the Automation is disabled. Conditions are evaluated unless
// [ManualRunInput.BypassConditions] requests an explicit bypass. A committed
// Condition Skip returns [ErrAutomationConditionsBlocked] after the transaction,
// so the Skip's history is never rolled back. A running Run returns
// [ErrAutomationBusy] with no new Skip and a closed gate returns
// [ErrAdmissionUnavailable].
func (service *Service) StartManualRun(ctx context.Context, input ManualRunInput) (AutomationRun, error) {
	reservation, admitted := service.admission.TryAcquire()
	if !admitted {
		return AutomationRun{}, ErrAdmissionUnavailable
	}
	// Track admission until the committed Run has a worker; release on errors too.
	defer reservation.Release()
	// The device gate is checked separately: automation admission closes first on
	// shutdown, and the cross-module gates never claim an atomic check-and-admit.
	if service.devices == nil || !service.devices.CommandAdmissionOpen() {
		return AutomationRun{}, ErrAdmissionUnavailable
	}
	// The reservation spans the definition pre-read, State read, and transaction.
	result, err := service.admitManualRun(ctx, input)
	if err != nil {
		// Release before diagnostics so a blocked log sink cannot hold Drain.
		reservation.Release()
		service.logConditionStateCorrupt(ctx, err)
		return AutomationRun{}, err
	}
	if result.Skip != nil {
		skip := *result.Skip
		// Release before logging so a blocked sink cannot hold Drain, then turn
		// the committed Skip into the typed blocked error outside the transaction.
		reservation.Release()
		service.logSkipped(ctx, AdmissionSkip{
			SkipID:       skip.ID,
			AutomationID: skip.AutomationID,
			Revision:     skip.Revision,
			Source:       skip.Source,
			Reason:       skip.Reason,
		})
		return AutomationRun{}, &AutomationConditionsBlockedError{
			AutomationID: skip.AutomationID,
			SkipID:       skip.ID,
			Reason:       skip.Reason,
		}
	}
	run := *result.Run
	// Caller cancellation must not cancel an admitted Run.
	workerContext := context.WithoutCancel(ctx)
	reservation.Go(func() { service.executeRun(workerContext, run) })
	// Release before logging so a blocked sink cannot hold Drain.
	reservation.Release()
	service.logRunStarted(ctx, run)
	return run, nil
}

// ReceiveDeviceFact admits one Device Fact against current enabled definitions
// and starts Run workers only after the admission transaction
// commits.
func (service *Service) ReceiveDeviceFact(
	ctx context.Context,
	fact DeviceFact,
) (AdmissionOutcome, error) {
	reservation, admitted := service.admission.TryAcquire()
	if !admitted {
		return AdmissionOutcome{}, ErrAdmissionUnavailable
	}
	defer reservation.Release()
	// The reservation spans the definition pre-read, State read, and transaction;
	// only a committed outcome registers workers.
	result, err := service.admitAutomaticFact(ctx, fact)
	if err != nil {
		// Release before diagnostics so a blocked log sink cannot hold Drain.
		reservation.Release()
		service.logConditionStateCorrupt(ctx, err)
		return AdmissionOutcome{}, err
	}
	// Start all committed Runs before logging can block, then release admission.
	workerContext := context.WithoutCancel(ctx)
	for _, run := range result.StartedRuns {
		reservation.Go(func() { service.executeRun(workerContext, run) })
	}
	reservation.Release()
	for _, run := range result.StartedRuns {
		service.logRunStarted(ctx, run)
	}
	for _, skip := range result.Skips {
		service.logSkipped(ctx, skip)
	}
	return result.Outcome, nil
}

// logRunStarted logs committed Run identity and Fact provenance, never definition
// JSON or Command parameters.
func (service *Service) logRunStarted(ctx context.Context, run AutomationRun) {
	attributes := []slog.Attr{
		slog.String("event", "automation.run_started"),
		slog.String("automation_id", string(run.AutomationID)),
		slog.String("run_id", string(run.ID)),
		slog.Int64("revision", run.Revision),
		slog.String("source", string(run.Source)),
	}
	if run.Fact != nil {
		attributes = append(attributes,
			slog.String("family", string(run.Fact.Family)),
			slog.String("variant", run.Fact.Variant),
		)
	}
	service.dependencies.Logger.LogAttrs(ctx, slog.LevelInfo, "automation run started", attributes...)
}

// logSkipped logs committed Skip identity, admission source, and reason without
// payload values. A manual Skip has no Fact identity, so it logs no fabricated
// empty Fact fields.
func (service *Service) logSkipped(ctx context.Context, skip AdmissionSkip) {
	attributes := []slog.Attr{
		slog.String("event", "automation.skipped"),
		slog.String("automation_id", string(skip.AutomationID)),
		slog.String("skip_id", string(skip.SkipID)),
		slog.Int64("revision", skip.Revision),
		slog.String("source", string(skip.Source)),
		slog.String("reason", string(skip.Reason)),
	}
	if skip.FactID != nil {
		attributes = append(attributes,
			slog.String("fact_id", string(*skip.FactID)),
			slog.String("family", string(skip.Family)),
			slog.String("variant", skip.Variant),
		)
	}
	service.dependencies.Logger.LogAttrs(ctx, slog.LevelInfo, "automation run skipped", attributes...)
}

// NewDeviceFactSummary copies one Device Fact into immutable history evidence so
// a retained Run or Skip stays explainable after Fact and Observation history
// are pruned. Persistence calls it on the validated inbound Fact before opening
// its admission transaction; it performs no reads or writes of its own.
func NewDeviceFactSummary(fact DeviceFact) DeviceFactSummary {
	summary := DeviceFactSummary{Family: fact.Family}
	switch fact.Family {
	case DeviceFactObservation:
		summary.FactID = fact.Observation.FactID
		summary.EntityID = fact.Observation.EntityID
		summary.Variant = string(fact.Observation.Disposition)
		summary.CausationID = string(fact.Observation.ObservationID)
		summary.ObservationValue = append(devices.Value(nil), fact.Observation.Value...)
		summary.EmittedAt = fact.Observation.EmittedAt
	case DeviceFactEntityEvent:
		summary.FactID = fact.EntityEvent.FactID
		summary.EntityID = fact.EntityEvent.EntityID
		summary.Variant = string(fact.EntityEvent.Name)
		summary.CausationID = string(fact.EntityEvent.EventID)
		summary.EmittedAt = fact.EntityEvent.EmittedAt
	}
	return summary
}

// MatchAutomationTriggers returns the IDs of every Trigger in one definition the
// Fact matches, in definition order. Triggers combine with OR and one Fact
// creates at most one outcome per Automation. Persistence calls it with the
// definitions it loaded inside its admission transaction; it performs no reads
// or writes of its own.
func MatchAutomationTriggers(fact DeviceFact, definition AutomationDefinition) ([]TriggerID, error) {
	var matched []TriggerID
	for _, trigger := range definition.Triggers {
		matches, err := matchAutomationTrigger(fact, trigger)
		if err != nil {
			return nil, err
		}
		if matches {
			matched = append(matched, trigger.ID)
		}
	}
	return matched, nil
}

// MatchedTriggerSnapshots selects Triggers in the supplied match order so a
// retained Skip can preserve the definition that matched. MatchAutomationTriggers
// supplies IDs in definition order. Nested values remain shared with definition;
// callers must not mutate them before persistence encodes the snapshots.
func MatchedTriggerSnapshots(
	definition AutomationDefinition,
	matched []TriggerID,
) ([]AutomationTrigger, error) {
	byID := make(map[TriggerID]AutomationTrigger, len(definition.Triggers))
	for _, trigger := range definition.Triggers {
		byID[trigger.ID] = trigger
	}
	snapshots := make([]AutomationTrigger, 0, len(matched))
	for _, id := range matched {
		trigger, found := byID[id]
		if !found {
			return nil, fmt.Errorf("%w: matched trigger %q is not in the definition", ErrInvalidAutomation, id)
		}
		snapshots = append(snapshots, trigger)
	}
	return snapshots, nil
}

func matchAutomationTrigger(fact DeviceFact, trigger AutomationTrigger) (bool, error) {
	switch trigger.Kind {
	case TriggerKindObservation:
		if fact.Family != DeviceFactObservation || fact.Observation == nil || trigger.Observation == nil {
			return false, nil
		}
		return matchObservationTrigger(fact.Observation, trigger.Observation)
	case TriggerKindEntityEvent:
		if fact.Family != DeviceFactEntityEvent || fact.EntityEvent == nil || trigger.EntityEvent == nil {
			return false, nil
		}
		return fact.EntityEvent.EntityID == trigger.EntityEvent.EntityID &&
			fact.EntityEvent.Name == trigger.EntityEvent.EventName, nil
	default:
		return false, fmt.Errorf("%w: trigger %q has unknown kind %q", ErrInvalidAutomation, trigger.ID, trigger.Kind)
	}
}

func matchObservationTrigger(fact *ObservationFact, trigger *ObservationTrigger) (bool, error) {
	if fact.EntityID != trigger.EntityID {
		return false, nil
	}
	if !slices.Contains(trigger.Dispositions, fact.Disposition) {
		return false, nil
	}
	for _, comparison := range trigger.Comparisons {
		matches, err := MatchObservationComparison(comparison, fact.Value)
		if err != nil {
			return false, err
		}
		if !matches {
			return false, nil
		}
	}
	return true, nil
}
