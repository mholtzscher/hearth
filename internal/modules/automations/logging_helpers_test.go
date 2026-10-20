package automations_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// lockedAutomationLogWriter allows assertions while Run workers log concurrently.
type lockedAutomationLogWriter struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (writer *lockedAutomationLogWriter) Write(payload []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.Write(payload)
}

func (writer *lockedAutomationLogWriter) output() string {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.String()
}

func (writer *lockedAutomationLogWriter) records(t *testing.T) []map[string]any {
	t.Helper()
	var parsed []map[string]any
	for line := range strings.SplitSeq(writer.output(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("automation log line is not JSON: %v\n%s", err, line)
		}
		parsed = append(parsed, record)
	}
	return parsed
}

func newAutomationLogSink() (*lockedAutomationLogWriter, *slog.Logger) {
	writer := &lockedAutomationLogWriter{}
	return writer, slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func automationLogEvents(records []map[string]any, event string) []map[string]any {
	var matched []map[string]any
	for _, record := range records {
		if record["event"] == event {
			matched = append(matched, record)
		}
	}
	return matched
}

func requireAutomationLogField(t *testing.T, record map[string]any, field, want string) {
	t.Helper()
	got, ok := record[field].(string)
	if !ok || got != want {
		t.Fatalf("log field %q = %#v, want %q (record = %#v)", field, record[field], want, record)
	}
}

func requireAutomationLogInt(t *testing.T, record map[string]any, field string, want int64) {
	t.Helper()
	got, ok := record[field].(float64)
	if !ok || int64(got) != want {
		t.Fatalf("log field %q = %#v, want %d (record = %#v)", field, record[field], want, record)
	}
}

// fixedAutomationLogID builds a canonical UUIDv7 from a deterministic ordinal.
func fixedAutomationLogID(prefix string, ordinal int64) string {
	return fmt.Sprintf("%s_018f9c1e-0000-7000-8000-%012d", prefix, ordinal)
}

// fixedAutomationLogDependencies fixes time and identities for log assertions.
func fixedAutomationLogDependencies(logger *slog.Logger) automations.Dependencies {
	var automationOrdinal, runOrdinal, skipOrdinal, commandOrdinal, correlationOrdinal atomic.Int64
	return automations.Dependencies{
		Logger: logger,
		Now:    func() time.Time { return runtimeTestNow },
		NewAutomationID: func() (automations.AutomationID, error) {
			return automations.AutomationID(fixedAutomationLogID("aut", automationOrdinal.Add(1))), nil
		},
		NewRunID: func() (automations.RunID, error) {
			return automations.RunID(fixedAutomationLogID("arn", runOrdinal.Add(1))), nil
		},
		NewSkipID: func() (automations.SkipID, error) {
			return automations.SkipID(fixedAutomationLogID("ask", skipOrdinal.Add(1))), nil
		},
		NewCommandID: func() (devices.CommandID, error) {
			return devices.CommandID(fixedAutomationLogID("cmd", commandOrdinal.Add(1))), nil
		},
		NewCorrelationID: func() (devices.CorrelationID, error) {
			return devices.CorrelationID(fixedAutomationLogID("cor", correlationOrdinal.Add(1))), nil
		},
	}
}
