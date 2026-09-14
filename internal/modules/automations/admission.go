package automations

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationFactMaximumAge is the fixed semantic freshness bound for one Device
// Fact. A matching Fact whose envelope `emitted_at` is older than this records
// stale_fact skips instead of executing delayed Commands. It is deliberately not
// operator configuration.
const AutomationFactMaximumAge = 30 * time.Second

// StartManualRun admits one Run from the current definition snapshot, even when
// the Automation is disabled. Each accepted call creates a distinct Run; a
// running Run returns [ErrAutomationBusy] and a closed gate returns
// [ErrAdmissionUnavailable].
func (service *Service) StartManualRun(ctx context.Context, id AutomationID) (AutomationRun, error) {
	if !service.beginAdmission() {
		return AutomationRun{}, ErrAdmissionUnavailable
	}
	// The device gate is checked separately: automation admission closes first on
	// shutdown, and the cross-module gates never claim an atomic check-and-admit.
	if service.devices == nil || !service.devices.CommandAdmissionOpen() {
		service.abandonAdmission()
		return AutomationRun{}, ErrAdmissionUnavailable
	}
	run, err := service.repository.AdmitManualRun(ctx, id, service.dependencies.Now())
	if err != nil {
		service.abandonAdmission()
		return AutomationRun{}, err
	}
	service.registerAdmittedRuns(run)
	service.startRun(ctx, run)
	service.logRunStarted(ctx, run)
	return run, nil
}

// ReceiveDeviceFact admits one Device Fact against current enabled definitions
// and registers every started Run worker only after the admission transaction
// commits.
func (service *Service) ReceiveDeviceFact(
	ctx context.Context,
	fact DeviceFact,
) (AdmissionOutcome, error) {
	if !service.beginAdmission() {
		return AdmissionOutcome{}, ErrAdmissionUnavailable
	}
	result, err := service.repository.AdmitDeviceFact(ctx, fact, service.dependencies.Now())
	if err != nil {
		service.abandonAdmission()
		return AdmissionOutcome{}, err
	}
	service.registerAdmittedRuns(result.StartedRuns...)
	for index := range result.StartedRuns {
		run := result.StartedRuns[index]
		service.startRun(ctx, run)
		service.logRunStarted(ctx, run)
	}
	for _, skip := range result.Skips {
		service.logSkipped(ctx, skip)
	}
	return result.Outcome, nil
}

// logRunStarted records one committed Run with only safe structured attributes,
// never the definition JSON or a Command parameter. It covers manual and
// Device-Fact Runs; an automatic Run also carries the matching Fact's family and
// disposition or event name.
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

// logSkipped records one committed Skip with its exact fixed reason and the
// Fact's safe identity. It never logs a Fact value, a definition snapshot, or a
// Command parameter.
func (service *Service) logSkipped(ctx context.Context, skip AdmissionSkip) {
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation run skipped",
		slog.String("event", "automation.skipped"),
		slog.String("automation_id", string(skip.AutomationID)),
		slog.String("skip_id", string(skip.SkipID)),
		slog.Int64("revision", skip.Revision),
		slog.String("reason", string(skip.Reason)),
		slog.String("fact_id", string(skip.FactID)),
		slog.String("family", string(skip.Family)),
		slog.String("variant", skip.Variant),
	)
}

// startRun launches one committed Run's worker detached from the caller's
// cancellation. [Service.registerAdmittedRuns] already registered and tracks the
// worker before this call, so drain joins it before dependencies are torn down.
func (service *Service) startRun(ctx context.Context, run AutomationRun) {
	workerContext := context.WithoutCancel(ctx)
	go service.executeRun(workerContext, run)
}

// factSummary copies one Device Fact into immutable history evidence so a
// retained Run or Skip stays explainable after Fact and Observation history are
// pruned.
func factSummary(fact DeviceFact) DeviceFactSummary {
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

// matchAutomationTriggers returns the IDs of every Trigger in one definition the
// Fact matches, in definition order. Triggers combine with OR and one Fact
// creates at most one outcome per Automation.
func matchAutomationTriggers(fact DeviceFact, definition AutomationDefinition) ([]TriggerID, error) {
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
