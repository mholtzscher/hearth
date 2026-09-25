package automations

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// FactMaximumAge is the fixed freshness limit measured from emitted_at.
// Older matching Facts record stale_fact Skips instead of starting Runs.
const FactMaximumAge = 30 * time.Second

// StartManualRun admits one Run from the current definition snapshot, even when
// the Automation is disabled. Conditions are evaluated unless
// [ManualRunInput.BypassConditions] requests an explicit bypass; a committed
// Condition Skip returns [ErrAutomationConditionsBlocked] after the transaction.
func (service *Service) StartManualRun(ctx context.Context, input ManualRunInput) (Run, error) {
	reservation, admitted := service.admission.TryAcquire()
	if !admitted {
		return Run{}, ErrAdmissionUnavailable
	}
	defer reservation.Release()
	// The device gate is checked separately: automation admission closes first on
	// shutdown, and the cross-module gates never claim an atomic check-and-admit.
	if service.devices == nil || !service.devices.CommandAdmissionOpen() {
		return Run{}, ErrAdmissionUnavailable
	}
	result, err := service.admitManualRun(ctx, input)
	if err != nil {
		// Release before diagnostics so a blocked log sink cannot hold Drain.
		reservation.Release()
		service.logConditionStateCorrupt(ctx, err)
		return Run{}, err
	}
	if result.Skip != nil {
		skip := *result.Skip
		reservation.Release()
		service.logSkipped(ctx, AdmissionSkip{
			SkipID:       skip.ID,
			AutomationID: skip.AutomationID,
			Revision:     skip.Revision,
			Source:       skip.Source,
			Reason:       skip.Reason,
		})
		return Run{}, &ConditionsBlockedError{
			AutomationID: skip.AutomationID,
			SkipID:       skip.ID,
			Reason:       skip.Reason,
		}
	}
	run := *result.Run
	// Caller cancellation must not cancel an admitted Run.
	workerContext := context.WithoutCancel(ctx)
	reservation.Go(func() { service.executeRun(workerContext, run) })
	reservation.Release()
	service.logRunStarted(ctx, run)
	return run, nil
}

// ReceiveDeviceFact admits one Device Fact against current enabled definitions.
// Run workers start only after the admission transaction commits.
func (service *Service) ReceiveDeviceFact(
	ctx context.Context,
	fact DeviceFact,
) (AdmissionOutcome, error) {
	reservation, admitted := service.admission.TryAcquire()
	if !admitted {
		return AdmissionOutcome{}, ErrAdmissionUnavailable
	}
	defer reservation.Release()
	result, err := service.admitAutomaticFact(ctx, fact)
	if err != nil {
		reservation.Release()
		service.logConditionStateCorrupt(ctx, err)
		return AdmissionOutcome{}, err
	}
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

// logRunStarted logs committed Run identity and Fact provenance.
func (service *Service) logRunStarted(ctx context.Context, run Run) {
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

// logSkipped logs committed Skip identity, admission source, and reason.
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
// a retained Run or Skip stays explainable after Fact history is pruned.
func NewDeviceFactSummary(fact DeviceFact) DeviceFactSummary {
	summary := DeviceFactSummary{Family: fact.Family}
	switch fact.Family {
	case DeviceFactObservation:
		summary.FactID = fact.Observation.FactID
		summary.EntityID = fact.Observation.EntityID
		summary.Variant = string(fact.Observation.Disposition)
		summary.CausationID = string(fact.Observation.ObservationID)
		summary.ObservationValue = append(devices.Value(nil), fact.Observation.Value...)
		summary.PreviousStateValue = append(devices.Value(nil), fact.Observation.PreviousValue...)
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

// MatchTriggers returns the IDs of every Trigger in one definition the Fact
// matches, in definition order.
func MatchTriggers(fact DeviceFact, definition Definition) ([]TriggerID, error) {
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
// retained Skip can preserve the definition that matched. Nested values remain
// shared with definition; callers must not mutate them before persistence encodes.
func MatchedTriggerSnapshots(
	definition Definition,
	matched []TriggerID,
) ([]Trigger, error) {
	byID := make(map[TriggerID]Trigger, len(definition.Triggers))
	for _, trigger := range definition.Triggers {
		byID[trigger.ID] = trigger
	}
	snapshots := make([]Trigger, 0, len(matched))
	for _, id := range matched {
		trigger, found := byID[id]
		if !found {
			return nil, fmt.Errorf("%w: matched trigger %q is not in the definition", ErrInvalidAutomation, id)
		}
		snapshots = append(snapshots, trigger)
	}
	return snapshots, nil
}

func matchAutomationTrigger(fact DeviceFact, trigger Trigger) (bool, error) {
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
	case TriggerKindHeldState:
		// Held-state Triggers are evaluated by the deadline worker, never by
		// immediate Device Fact admission.
		return false, nil
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
	if len(trigger.PreviousComparisons) > 0 && fact.PreviousValue == nil {
		return false, nil
	}
	for _, comparison := range trigger.PreviousComparisons {
		matches, err := MatchObservationComparison(comparison, fact.PreviousValue)
		if err != nil {
			return false, err
		}
		if !matches {
			return false, nil
		}
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
