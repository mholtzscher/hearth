package nats

import "log/slog"

func defaultLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default().With(slog.String("component", "nats"))
	}
	return logger
}

// Stable structured keys for Devices transport diagnostics. Event values stay
// whole literals at emission sites so an event-name search finds its owner.
const (
	transportEventKey     = "event"
	transportErrorCodeKey = "error_code"
)
