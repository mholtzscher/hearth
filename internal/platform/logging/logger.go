// Package logging builds the process loggers shared by every Hearth executable.
package logging

import (
	"errors"
	"io"
	"log/slog"
	"os"
)

// LogOptions controls process log output; the zero value selects info level text output.
type LogOptions struct {
	// Level selects the minimum log level: debug, info, warn, or error.
	Level string
	// Format selects the log format: text or json.
	Format string
}

// jsonLogFormat selects newline-delimited JSON records from NewApplicationLogger.
const jsonLogFormat = "json"

// NewApplicationLogger creates a logger writing one record per line to output
// without changing the global default logger. The caller owns output; logger
// writes never close it. The logger carries the exact binary name as app and
// the current process id as pid. Option errors carry a fixed explanation that
// lists the allowed values without echoing the supplied value.
func NewApplicationLogger(output io.Writer, app string, options LogOptions) (*slog.Logger, error) {
	if output == nil {
		return nil, errors.New("logging: output writer is required")
	}
	level, err := parseLogLevel(options.Level)
	if err != nil {
		return nil, err
	}
	format, err := parseLogFormat(options.Format)
	if err != nil {
		return nil, err
	}
	handlerOptions := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if format == jsonLogFormat {
		handler = slog.NewJSONHandler(output, handlerOptions)
	} else {
		handler = slog.NewTextHandler(output, handlerOptions)
	}
	return slog.New(handler).With("app", app, "pid", os.Getpid()), nil
}

func parseLogLevel(level string) (slog.Level, error) {
	switch level {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, errors.New("logging: invalid log level: must be one of debug, info, warn, error")
	}
}

func parseLogFormat(format string) (string, error) {
	switch format {
	case "", "text":
		return "text", nil
	case jsonLogFormat:
		return jsonLogFormat, nil
	default:
		return "", errors.New("logging: invalid log format: must be one of text, json")
	}
}
