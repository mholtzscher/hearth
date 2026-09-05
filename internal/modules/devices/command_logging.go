package devices

import (
	"context"
	"log/slog"
	"time"
)

// Structured field keys shared by command log emission sites. Event values
// stay whole literals at each site so searching an event name finds its
// implementation.
const (
	commandEventKey     = "event"
	commandErrorCodeKey = "error_code"
)

// logCommandCreated emits the durable creation record after CreateCommand
// commits. The caller invokes it with the operation context that created the
// command, including for immediately terminal pre-dispatch records.
func (service *Service) logCommandCreated(ctx context.Context, command CommandRecord) {
	logger := service.commandLogger()
	attributes := []any{
		"command_id", string(command.ID),
		"correlation_id", string(command.CorrelationID),
		"entity_id", string(command.EntityID),
		"adapter_id", command.AdapterID,
		"operation", string(command.OperationName),
	}
	if command.RuntimeID != nil {
		attributes = append(attributes, "runtime_id", string(*command.RuntimeID))
	}
	logger.InfoContext(ctx, "command created", append([]any{commandEventKey, "command.created"}, attributes...)...)
}

// logCommandOutcome emits the single terminal summary for one in-memory
// command lifecycle. A known durable status produces command.completed
// (satisfied at Info, expected terminal failures at Warn); an established
// internal_failure or an empty status with no durable outcome produces
// command.execution_failed at Error. Raw error text is never logged.
func (service *Service) logCommandOutcome(
	ctx context.Context,
	command CommandRecord,
	outcome commandOutcome,
	startedAt time.Time,
) {
	logger := service.commandLogger()
	attributes := []any{
		"command_id", string(command.ID),
		"correlation_id", string(command.CorrelationID),
		"entity_id", string(command.EntityID),
		"adapter_id", command.AdapterID,
		"operation", string(command.OperationName),
	}
	if command.RuntimeID != nil {
		attributes = append(attributes, "runtime_id", string(*command.RuntimeID))
	}
	if !startedAt.IsZero() {
		elapsed := max(time.Since(startedAt), 0)
		attributes = append(attributes, "duration_ms", int64(elapsed/time.Millisecond))
	}
	if outcome.status == "" {
		fields := []any{commandEventKey, "command.execution_failed", commandErrorCodeKey, "outcome_unknown"}
		logger.ErrorContext(ctx, "command execution failed", append(fields, attributes...)...)
		return
	}
	attributes = append(attributes, "status", string(outcome.status))
	if outcome.failureCode != "" {
		attributes = append(attributes, "failure_code", string(outcome.failureCode))
	}
	if outcome.status == CommandStatusInternalFailure {
		logger.ErrorContext(ctx, "command execution failed",
			append([]any{commandEventKey, "command.execution_failed"}, attributes...)...)
		return
	}
	if outcome.status == CommandStatusSatisfied {
		if outcome.result.ObservationID != "" {
			attributes = append(attributes, "observation_id", string(outcome.result.ObservationID))
		}
		logger.InfoContext(ctx, "command completed",
			append([]any{commandEventKey, "command.completed"}, attributes...)...)
		return
	}
	logger.WarnContext(ctx, "command completed",
		append([]any{commandEventKey, "command.completed"}, attributes...)...)
}

// commandLogger returns the injected devices logger, falling back to the
// process default with the devices component for non-injected callers.
func (service *Service) commandLogger() *slog.Logger {
	if service != nil && service.logger != nil {
		return service.logger
	}
	return slog.Default().With("component", "devices")
}
