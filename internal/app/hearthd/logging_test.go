package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
)

// recordStore holds lifecycle records shared by a recorder and its With-derived
// handlers, so component scoping stays visible to assertions.
type recordStore struct {
	mutex   sync.Mutex
	records []slog.Record
}

// recordingHandler is a concurrency-safe slog handler for asserting structured
// lifecycle events emitted from background goroutines and NATS callbacks.
type recordingHandler struct {
	store    *recordStore
	minLevel slog.Level
	attrs    []slog.Attr
}

// withRecording groups a logger with its handler; With scoping is preserved
// on later records through the shared store.
func withRecording(level slog.Level) (*slog.Logger, *recordingHandler) {
	handler := &recordingHandler{store: &recordStore{}, minLevel: level}
	return slog.New(handler), handler
}

func (handler *recordingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= handler.minLevel
}

func (handler *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	combined := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	combined.AddAttrs(handler.attrs...)
	record.Attrs(func(attr slog.Attr) bool {
		combined.AddAttrs(attr)
		return true
	})
	handler.store.mutex.Lock()
	defer handler.store.mutex.Unlock()
	handler.store.records = append(handler.store.records, combined)
	return nil
}

func (handler *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := append(append([]slog.Attr(nil), handler.attrs...), attrs...)
	return &recordingHandler{store: handler.store, minLevel: handler.minLevel, attrs: combined}
}

func (handler *recordingHandler) WithGroup(string) slog.Handler { return handler }

func (handler *recordingHandler) snapshot() []slog.Record {
	handler.store.mutex.Lock()
	defer handler.store.mutex.Unlock()
	return append([]slog.Record(nil), handler.store.records...)
}

func recordAttr(record slog.Record, key string) (slog.Value, bool) {
	var found slog.Value
	matched := false
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == key {
			found = attr.Value
			matched = true
			return false
		}
		return true
	})
	return found, matched
}

func requireRecordAttr(t *testing.T, record slog.Record, key, want string) {
	t.Helper()
	value, ok := recordAttr(record, key)
	if !ok || value.String() != want {
		t.Fatalf("log record %q attr %q = %#v, want %q", record.Message, key, value, want)
	}
}

func recordsWithEvent(records []slog.Record, event string) []slog.Record {
	var matched []slog.Record
	for _, record := range records {
		if value, ok := recordAttr(record, "event"); ok && value.String() == event {
			matched = append(matched, record)
		}
	}
	return matched
}

// This test protects fatal stage classification and fails if a wrapped stage
// is lost or an unstaged error claims a stage.
func TestErrorStageReportsStartupStage(t *testing.T) {
	t.Parallel()
	if stage := ErrorStage(failStage("http_listen", errors.New("busy"))); stage != "http_listen" {
		t.Fatalf("ErrorStage = %q, want http_listen", stage)
	}
	if stage := ErrorStage(errors.New("boom")); stage != "run" {
		t.Fatalf("ErrorStage = %q, want run", stage)
	}
	if err := failStage("migrate_database", nil); err != nil {
		t.Fatalf("failStage with nil error = %v, want nil", err)
	}
}
