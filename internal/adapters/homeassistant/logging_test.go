package homeassistant //nolint:testpackage // Tests assert package-private logging behavior.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureHandler is a minimal slog handler for asserting structured records.
type captureStore struct {
	mutex   sync.Mutex
	records []capturedRecord
}

type captureHandler struct {
	store  *captureStore
	prefix []slog.Attr
}

type capturedRecord struct {
	level   slog.Level
	message string
	attrs   map[string]any
}

func newCaptureHandler() *captureHandler {
	return &captureHandler{store: &captureStore{}}
}

func (handler *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (handler *captureHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]any, len(handler.prefix)+record.NumAttrs())
	for _, attr := range handler.prefix {
		attrs[attr.Key] = attr.Value.Any()
	}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	handler.store.mutex.Lock()
	handler.store.records = append(handler.store.records, capturedRecord{
		level: record.Level, message: record.Message, attrs: attrs,
	})
	handler.store.mutex.Unlock()
	return nil
}

func (handler *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &captureHandler{store: handler.store, prefix: append(handler.prefix, attrs...)}
}

func (handler *captureHandler) WithGroup(string) slog.Handler { return handler }

func (handler *captureHandler) snapshot() []capturedRecord {
	handler.store.mutex.Lock()
	defer handler.store.mutex.Unlock()
	return append([]capturedRecord(nil), handler.store.records...)
}

func (handler *captureHandler) count(level slog.Level, event string) int {
	total := 0
	for _, record := range handler.snapshot() {
		if record.level == level && record.attrs["event"] == event {
			total++
		}
	}
	return total
}

func (handler *captureHandler) containsText(text string) bool {
	for _, record := range handler.snapshot() {
		if strings.Contains(record.message, text) {
			return true
		}
		for _, value := range record.attrs {
			if strings.Contains(fmt.Sprintf("%v", value), text) {
				return true
			}
		}
	}
	return false
}

func newCaptureAdapter(
	t *testing.T,
	session Session,
	token string,
	handler *captureHandler,
) *Adapter {
	t.Helper()
	value, err := New(session, Config{
		URL: "http://127.0.0.1:1", Token: token,
		ExternalEntityID: testExternalEntityID, EntityID: testEntityID,
	}, slog.New(handler))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func waitForCapture(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for log condition")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAdapterAttachesBoundedComponentOnce(t *testing.T) {
	t.Parallel()
	handler := newCaptureHandler()
	adapter := newCaptureAdapter(t, newRecordingSession(), "test-token", handler)

	adapter.logUnsupportedState(context.Background(), "event")

	records := handler.snapshot()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	record := records[0]
	if record.attrs["component"] != adapterComponent {
		t.Fatalf("component = %v, want %q", record.attrs["component"], adapterComponent)
	}
	if record.attrs["event"] != "adapter.unsupported_state" {
		t.Fatalf("event = %v", record.attrs["event"])
	}
	for _, key := range []string{"app", "pid", "state"} {
		if _, exists := record.attrs[key]; exists {
			t.Fatalf("record carries forbidden key %q: %#v", key, record.attrs)
		}
	}
}

func TestConnectionRetryIsDebugWithFixedCode(t *testing.T) {
	t.Parallel()
	const tokenSentinel = "token-sentinel-9f3b7a1d"
	handler := newCaptureHandler()
	adapter := newCaptureAdapter(t, newRecordingSession(), tokenSentinel, handler)

	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- adapter.Run(ctx) }()
	waitForCapture(t, func() bool {
		return handler.count(slog.LevelDebug, "dependency.retrying") >= 1
	})
	cancel()
	if err := <-runResult; err != nil {
		t.Fatal(err)
	}

	if got := handler.count(slog.LevelWarn, "dependency.retrying"); got != 0 {
		t.Fatalf("warn retry records = %d, want 0 (retries stay at Debug)", got)
	}
	for _, record := range handler.snapshot() {
		if record.attrs["event"] != "dependency.retrying" {
			continue
		}
		if record.attrs["dependency"] != adapterComponent {
			t.Fatalf("dependency = %v: %#v", record.attrs["dependency"], record.attrs)
		}
		if _, exists := record.attrs["retry_in_ms"]; !exists {
			t.Fatalf("retry record misses retry_in_ms: %#v", record.attrs)
		}
		if _, exists := record.attrs["error"]; exists {
			t.Fatalf("retry record carries arbitrary error text: %#v", record.attrs)
		}
		if _, exists := record.attrs["error_code"]; !exists {
			t.Fatalf("retry record misses error_code: %#v", record.attrs)
		}
	}
	for _, sentinel := range []string{tokenSentinel, "127.0.0.1:1"} {
		if handler.containsText(sentinel) {
			t.Fatalf("logs leak sentinel %q", sentinel)
		}
	}
}

func TestUnsupportedStateOmitsUpstreamPayload(t *testing.T) {
	t.Parallel()
	const payloadSentinel = "payload-sentinel-4c2e8b6a"
	handler := newCaptureHandler()
	adapter := newCaptureAdapter(t, newRecordingSession(), "test-token", handler)

	if err := adapter.processState(context.Background(), upstreamState{
		EntityID: testExternalEntityID, State: payloadSentinel,
	}, time.Now().UTC()); err == nil {
		t.Fatal("unsupported State returned nil")
	}
	adapter.logUnsupportedState(context.Background(), "snapshot")

	if handler.containsText(payloadSentinel) {
		t.Fatalf("logs leak upstream State payload %q", payloadSentinel)
	}
	if handler.containsText(testExternalEntityID) {
		t.Fatalf("logs export vendor Entity identity %q", testExternalEntityID)
	}
	if handler.count(slog.LevelWarn, "adapter.unsupported_state") == 0 {
		t.Fatal("missing adapter.unsupported_state record")
	}
}

func TestInvalidSourceTimestampOmitsRawValue(t *testing.T) {
	t.Parallel()
	const timestampSentinel = "timestamp-sentinel-7d1a5f3c"
	handler := newCaptureHandler()
	adapter := newCaptureAdapter(t, newRecordingSession(), "test-token", handler)

	if err := adapter.publish(context.Background(), upstreamState{
		EntityID: testExternalEntityID, State: "on", LastUpdated: timestampSentinel,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	if handler.containsText(timestampSentinel) {
		t.Fatalf("logs leak raw timestamp value %q", timestampSentinel)
	}
	if handler.count(slog.LevelWarn, "adapter.invalid_source_timestamp") != 1 {
		t.Fatalf("records = %#v", handler.snapshot())
	}
}
