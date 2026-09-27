package automations

import (
	"context"
	"log/slog"
)

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
