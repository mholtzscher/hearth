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

// logCommandExecutionFailed diagnoses unexpected execution or persistence
// failures without asserting a durable status. Command history owns outcomes;
// this diagnostic stays useful even when the HTTP caller has disconnected.
func (service *Service) logCommandExecutionFailed(ctx context.Context, command CommandRecord) {
	logger := service.commandLogger()
	attributes := []any{
		commandEventKey, "command.execution_failed",
		commandErrorCodeKey, "internal_error",
		"command_id", string(command.ID),
		"correlation_id", string(command.CorrelationID),
		"entity_id", string(command.EntityID),
		"adapter_id", command.AdapterID,
		"operation", string(command.OperationName),
	}
	if command.RuntimeID != nil {
		attributes = append(attributes, "runtime_id", string(*command.RuntimeID))
	}
	logger.ErrorContext(ctx, "command execution failed", attributes...)
}

// commandLogger returns the injected devices logger, falling back to the
// process default with the devices component for non-injected callers.
func (service *Service) commandLogger() *slog.Logger {
	if service != nil && service.logger != nil {
		return service.logger
	}
	return slog.Default().With("component", "devices")
}
