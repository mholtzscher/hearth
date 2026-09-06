package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/platform/logging"
)

// This test protects the log level contract (debug off by default, each level
// filters below-threshold records) and fails if a level string maps to the
// wrong threshold.
func TestNewApplicationLoggerLevelThresholds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		level     string
		wantDebug bool
		wantInfo  bool
		wantWarn  bool
		wantError bool
	}{
		{name: "zero value selects info", level: "", wantInfo: true, wantWarn: true, wantError: true},
		{name: "debug", level: "debug", wantDebug: true, wantInfo: true, wantWarn: true, wantError: true},
		{name: "info", level: "info", wantInfo: true, wantWarn: true, wantError: true},
		{name: "warn", level: "warn", wantWarn: true, wantError: true},
		{name: "error", level: "error", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			logger, err := logging.NewApplicationLogger(
				&bytes.Buffer{}, "hearthd", logging.LogOptions{Level: test.level},
			)
			if err != nil {
				t.Fatalf("NewApplicationLogger returned error: %v", err)
			}
			ctx := context.Background()
			if got := logger.Enabled(ctx, slog.LevelDebug); got != test.wantDebug {
				t.Errorf("debug enabled = %v, want %v", got, test.wantDebug)
			}
			if got := logger.Enabled(ctx, slog.LevelInfo); got != test.wantInfo {
				t.Errorf("info enabled = %v, want %v", got, test.wantInfo)
			}
			if got := logger.Enabled(ctx, slog.LevelWarn); got != test.wantWarn {
				t.Errorf("warn enabled = %v, want %v", got, test.wantWarn)
			}
			if got := logger.Enabled(ctx, slog.LevelError); got != test.wantError {
				t.Errorf("error enabled = %v, want %v", got, test.wantError)
			}
		})
	}
}

// This test protects the default-hides-debug behavior and fails if info output
// leaks debug records.
func TestNewApplicationLoggerDefaultHidesDebug(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger, err := logging.NewApplicationLogger(&output, "hearthd", logging.LogOptions{})
	if err != nil {
		t.Fatalf("NewApplicationLogger returned error: %v", err)
	}
	logger.Debug("routine progress")
	logger.Info("lifecycle milestone")
	if strings.Contains(output.String(), "routine progress") {
		t.Error("default logger emitted a debug record")
	}
	if !strings.Contains(output.String(), "lifecycle milestone") {
		t.Error("default logger dropped an info record")
	}
}

// This test protects the text/json format contract and fails if a format
// option silently produces the other encoding.
func TestNewApplicationLoggerFormats(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"", "text", "json"} {
		t.Run("format="+format, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			logger, err := logging.NewApplicationLogger(&output, "hearthd", logging.LogOptions{Format: format})
			if err != nil {
				t.Fatalf("NewApplicationLogger returned error: %v", err)
			}
			logger.Info("hello")
			line := strings.TrimSpace(output.String())
			if format == "json" {
				var record map[string]any
				if unmarshalErr := json.Unmarshal([]byte(line), &record); unmarshalErr != nil {
					t.Fatalf("json output does not parse per line: %v", unmarshalErr)
				}
				if record["msg"] != "hello" {
					t.Errorf("json msg = %v, want hello", record["msg"])
				}
			} else if strings.HasPrefix(line, "{") {
				t.Errorf("text output looks like json: %q", line)
			}
		})
	}
}

// This test protects JSON executable record identity (exact app name, current
// pid, each key once) and fails if the factory drops or duplicates fields.
func TestNewApplicationLoggerJSONRecordStructure(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger, err := logging.NewApplicationLogger(&output, "hearthd", logging.LogOptions{Format: "json"})
	if err != nil {
		t.Fatalf("NewApplicationLogger returned error: %v", err)
	}
	logger.Info("started")
	line := strings.TrimSpace(output.String())
	var record map[string]any
	if unmarshalErr := json.Unmarshal([]byte(line), &record); unmarshalErr != nil {
		t.Fatalf("record does not parse as json: %v", unmarshalErr)
	}
	if record["app"] != "hearthd" {
		t.Errorf("app = %v, want hearthd", record["app"])
	}
	pid, ok := record["pid"].(float64)
	if !ok || int(pid) != os.Getpid() {
		t.Errorf("pid = %v, want %d", record["pid"], os.Getpid())
	}
	if count := strings.Count(line, `"app":`); count != 1 {
		t.Errorf("app key occurs %d times, want exactly once", count)
	}
	if count := strings.Count(line, `"pid":`); count != 1 {
		t.Errorf("pid key occurs %d times, want exactly once", count)
	}
}

// This test protects text executable record identity and fails if the factory
// drops or duplicates the app and pid fields.
func TestNewApplicationLoggerTextRecordStructure(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger, err := logging.NewApplicationLogger(&output, "hearthd", logging.LogOptions{Format: "text"})
	if err != nil {
		t.Fatalf("NewApplicationLogger returned error: %v", err)
	}
	logger.Info("started")
	line := strings.TrimSpace(output.String())
	if !strings.Contains(line, "app=hearthd") {
		t.Errorf("text record missing app=hearthd: %q", line)
	}
	if count := strings.Count(line, "app="); count != 1 {
		t.Errorf("app key occurs %d times, want exactly once: %q", count, line)
	}
	if count := strings.Count(line, "pid="); count != 1 {
		t.Errorf("pid key occurs %d times, want exactly once: %q", count, line)
	}
}

// This test protects safe bootstrap diagnostics and fails if an invalid option
// error echoes the supplied value instead of the fixed allowed-values text.
// Values are case-sensitive, so uppercase variants must also fail safely.
func TestNewApplicationLoggerInvalidOptionsAreSafe(t *testing.T) {
	t.Parallel()
	sentinel := "tok-secret-" + strings.Repeat("x", 8)
	tests := []struct {
		name    string
		options logging.LogOptions
		allowed string
	}{
		{name: "level", options: logging.LogOptions{Level: sentinel}, allowed: "debug, info, warn, error"},
		{name: "format", options: logging.LogOptions{Format: sentinel}, allowed: "text, json"},
		{name: "uppercase level", options: logging.LogOptions{Level: "INFO"}, allowed: "debug, info, warn, error"},
		{name: "uppercase format", options: logging.LogOptions{Format: "JSON"}, allowed: "text, json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			logger, err := logging.NewApplicationLogger(&bytes.Buffer{}, "hearthd", test.options)
			if err == nil {
				t.Fatal("NewApplicationLogger accepted an invalid option")
			}
			if logger != nil {
				t.Error("NewApplicationLogger returned a logger alongside the error")
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Errorf("error echoes the supplied value: %q", err)
			}
			if !strings.Contains(err.Error(), test.allowed) {
				t.Errorf("error %q does not list allowed values %q", err, test.allowed)
			}
		})
	}
}

// This test protects the required-writer contract and fails if a nil writer is
// accepted and panics later on first write.
func TestNewApplicationLoggerRequiresOutput(t *testing.T) {
	t.Parallel()
	logger, err := logging.NewApplicationLogger(nil, "hearthd", logging.LogOptions{})
	if err == nil {
		t.Fatal("NewApplicationLogger accepted a nil writer")
	}
	if logger != nil {
		t.Error("NewApplicationLogger returned a logger alongside the error")
	}
}

// This test protects callers from global logger mutation and fails if the
// factory changes [slog.Default].
func TestNewApplicationLoggerLeavesGlobalDefault(t *testing.T) {
	t.Parallel()
	before := slog.Default()
	if _, err := logging.NewApplicationLogger(&bytes.Buffer{}, "hearthd", logging.LogOptions{}); err != nil {
		t.Fatalf("NewApplicationLogger returned error: %v", err)
	}
	if slog.Default() != before {
		t.Error("NewApplicationLogger changed the global default logger")
	}
}
