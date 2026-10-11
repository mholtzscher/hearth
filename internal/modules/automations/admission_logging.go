package automations

import (
	"context"
	"errors"
	"log/slog"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// logConditionStateCorrupt records the fixed, value-free diagnostic for unusable stored State.
func (service *Service) logConditionStateCorrupt(ctx context.Context, err error) {
	if !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
		return
	}
	service.dependencies.Logger.ErrorContext(
		ctx,
		"automation condition state is corrupt",
		slog.String("event", "automation.condition_state_corrupt"),
	)
}

// logRunStarted logs committed Run identity and Fact provenance.
func (service *Service) logRunStarted(ctx context.Context, run Run) {
	attributes := []slog.Attr{
		slog.String("event", "automation.run_started"),
		slog.String("automation_id", string(run.AutomationID)),
		slog.String("run_id", string(run.ID)),
		slog.Int64("revision", run.Revision),
		slog.String("source", string(CauseSource(run.Cause))),
	}
	attributes = append(attributes, causeFactLogAttributes(run.Cause, false)...)
	service.dependencies.Logger.LogAttrs(ctx, slog.LevelInfo, "automation run started", attributes...)
}

// logSkipped logs committed Skip identity, admission source, and reason.
func (service *Service) logSkipped(ctx context.Context, skip AdmissionSkip) {
	attributes := []slog.Attr{
		slog.String("event", "automation.skipped"),
		slog.String("automation_id", string(skip.AutomationID)),
		slog.String("skip_id", string(skip.SkipID)),
		slog.Int64("revision", skip.Revision),
		slog.String("source", string(CauseSource(skip.Cause))),
		slog.String("reason", string(skip.Reason)),
	}
	attributes = append(attributes, causeFactLogAttributes(skip.Cause, true)...)
	service.dependencies.Logger.LogAttrs(ctx, slog.LevelInfo, "automation run skipped", attributes...)
}

func causeFactLogAttributes(cause AdmissionCause, includeID bool) []slog.Attr {
	var fact DeviceFact
	switch cause := cause.(type) {
	case DeviceFactCause:
		fact = cause.Fact
	case ManualCause, HeldStateCause, ScheduleCause:
		return nil
	default:
		return nil
	}
	var variant string
	switch fact := fact.(type) {
	case ObservationFact:
		variant = string(fact.Disposition)
	case EntityEventFact:
		variant = string(fact.Name)
	default:
		return nil
	}
	attributes := []slog.Attr{
		slog.String("family", string(FactFamily(fact))),
		slog.String("variant", variant),
	}
	if includeID {
		attributes = append(attributes, slog.String("fact_id", string(FactID(fact))))
	}
	return attributes
}
