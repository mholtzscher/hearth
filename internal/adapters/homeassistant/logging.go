package homeassistant

import (
	"errors"
)

// adapterComponent is the only component value emitted from this package. It
// is attached once in New so every record carries a bounded subsystem label
// without repeating app or pid, which belong to the root application logger.
const adapterComponent = "homeassistant"

// eventKey is the shared slog attribute key carrying the stable dotted event
// name on every record emitted from this package. Event values stay whole
// literals at each emission site so searching a value finds its site.
const eventKey = "event"

// homeAssistantErrorCode maps a failure to a fixed diagnostic classification.
// The code never carries URLs, tokens, payloads, or upstream free-form text.
func homeAssistantErrorCode(err error) string {
	if _, ok := errors.AsType[*AuthenticationError](err); ok {
		return "authentication_failed"
	}
	if _, ok := errors.AsType[*requestRejectedError](err); ok {
		return "upstream_rejected"
	}
	if _, ok := errors.AsType[*sessionOperationError](err); ok {
		return "session_operation_failed"
	}
	return "upstream_connection_failed"
}
