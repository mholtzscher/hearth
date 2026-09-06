package devices

import (
	"context"
	"log/slog"
)

// Structured field keys shared by command log emission sites. Event values
// stay whole literals at each site so searching an event name finds its
// implementation.
const (
	commandEventKey     = "event"
	commandErrorCodeKey = "error_code"
)

// commandScopedLogger scopes stable command identity once for creation,
// dispatch, and failure diagnostics. Runtime identity is included only when
// the command already carries it; unknown fields are omitted.
func (service *Service) commandScopedLogger(command CommandRecord) *slog.Logger {
	logger := service.commandLogger().With(
		slog.String("command_id", string(command.ID)),
		slog.String("correlation_id", string(command.CorrelationID)),
		slog.String("entity_id", string(command.EntityID)),
		slog.String("adapter_id", command.AdapterID),
		slog.String("operation", string(command.OperationName)),
	)
	if command.RuntimeID != nil {
		return logger.With(slog.String("runtime_id", string(*command.RuntimeID)))
	}
	return logger
}

// logCommandCreated emits the durable creation record after CreateCommand
// commits. The caller invokes it with the operation context that created the
// command, including for immediately terminal pre-dispatch records.
func (service *Service) logCommandCreated(ctx context.Context, command CommandRecord) {
	service.commandScopedLogger(command).InfoContext(
		ctx,
		"command created",
		slog.String(commandEventKey, "command.created"),
	)
}

// logCommandExecutionFailed diagnoses unexpected execution or persistence
// failures without asserting a durable status. Command history owns outcomes;
// this diagnostic stays useful even when the HTTP caller has disconnected.
func (service *Service) logCommandExecutionFailed(ctx context.Context, command CommandRecord) {
	service.commandScopedLogger(command).ErrorContext(
		ctx,
		"command execution failed",
		slog.String(commandEventKey, "command.execution_failed"),
		slog.String(commandErrorCodeKey, "internal_error"),
	)
}

// commandLogger returns the injected devices logger, falling back to the
// process default with the devices component for non-injected callers.
func (service *Service) commandLogger() *slog.Logger {
	if service != nil && service.logger != nil {
		return service.logger
	}
	return slog.Default().With(slog.String("component", "devices"))
}
