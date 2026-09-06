package homeassistant //nolint:testpackage // Tests assert package-private logging behavior.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// logRecordSnapshot is one captured slog record with its resolved attributes.
type logRecordSnapshot struct {
	level   slog.Level
	message string
	attrs   map[string]any
}

// recordingHandler is a concurrency-safe slog handler for asserting structured
// log records without reading a shared buffer from multiple goroutines.
type recordingStore struct {
	mutex   sync.Mutex
	records []logRecordSnapshot
}

type recordingHandler struct {
	level  slog.Level
	prefix []slog.Attr
	store  *recordingStore
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{level: slog.LevelDebug, store: &recordingStore{}}
}

func (handler *recordingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= handler.level
}

func (handler *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]any, len(handler.prefix)+record.NumAttrs())
	for _, attr := range handler.prefix {
		attrs[attr.Key] = attr.Value.Any()
	}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	handler.store.mutex.Lock()
	handler.store.records = append(handler.store.records, logRecordSnapshot{
		level: record.Level, message: record.Message, attrs: attrs,
	})
	handler.store.mutex.Unlock()
	return nil
}

func (handler *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &recordingHandler{
		level: handler.level, prefix: append(handler.prefix, attrs...), store: handler.store,
	}
}

func (handler *recordingHandler) WithGroup(string) slog.Handler { return handler }

func (handler *recordingHandler) snapshot() []logRecordSnapshot {
	handler.store.mutex.Lock()
	defer handler.store.mutex.Unlock()
	return append([]logRecordSnapshot(nil), handler.store.records...)
}

func (handler *recordingHandler) count(level slog.Level, event string) int {
	total := 0
	for _, record := range handler.snapshot() {
		if record.level == level && record.attrs["event"] == event {
			total++
		}
	}
	return total
}

func (handler *recordingHandler) containsText(text string) bool {
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

func newRecordingAdapter(
	t *testing.T,
	session Session,
	upstreamURL, token string,
	handler *recordingHandler,
) *Adapter {
	t.Helper()
	value, err := New(session, Config{
		URL: upstreamURL, Token: token,
		ExternalEntityID: testExternalEntityID, EntityID: testEntityID,
	}, slog.New(handler))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func waitForLogCondition(t *testing.T, condition func() bool) {
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
	handler := newRecordingHandler()
	session := newRecordingSession()
	migrationAdapter := newRecordingAdapter(t, session, "http://127.0.0.1:1", "test-token", handler)

	migrationAdapter.logUnsupportedState(context.Background(), "event")

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
	for _, key := range []string{"app", "pid"} {
		if _, exists := record.attrs[key]; exists {
			t.Fatalf("record carries root-only key %q: %#v", key, record.attrs)
		}
	}
	if _, exists := record.attrs["state"]; exists {
		t.Fatalf("record carries raw upstream State: %#v", record.attrs)
	}
}

func TestRetryEpisodeWarnsOnceThenDebugs(t *testing.T) {
	t.Parallel()
	const tokenSentinel = "token-sentinel-9f3b7a1d"
	handler := newRecordingHandler()
	session := newRecordingSession()
	migrationAdapter := newRecordingAdapter(
		t, session, "http://127.0.0.1:1", tokenSentinel, handler,
	)

	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- migrationAdapter.Run(ctx) }()
	waitForLogCondition(t, func() bool {
		return handler.count(slog.LevelDebug, "dependency.retrying") >= 1 &&
			handler.count(slog.LevelWarn, "dependency.retrying") == 1
	})
	cancel()
	if err := <-runResult; err != nil {
		t.Fatal(err)
	}

	warns := 0
	attempts := make(map[int]int)
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
		if record.level == slog.LevelWarn {
			warns++
		}
		if attempt, ok := record.attrs["attempt"].(int64); ok {
			attempts[int(attempt)]++
		}
	}
	if warns != 1 {
		t.Fatalf("warn retry records = %d, want exactly 1", warns)
	}
	for _, sentinel := range []string{tokenSentinel, "127.0.0.1:1"} {
		if handler.containsText(sentinel) {
			t.Fatalf("logs leak sentinel %q", sentinel)
		}
	}
}

func TestRetryEpisodeWarnsOncePerClassAcrossInterleaving(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	session := newRecordingSession()
	migrationAdapter := newRecordingAdapter(t, session, "http://127.0.0.1:1", "test-token", handler)

	ctx := context.Background()
	var episode retryEpisode
	migrationAdapter.logConnectionRetry(ctx, &episode, &AuthenticationError{}, time.Second)
	migrationAdapter.logConnectionRetry(
		ctx, &episode, &requestRejectedError{Code: "code", Message: "rejected"}, time.Second,
	)
	migrationAdapter.logConnectionRetry(ctx, &episode, &AuthenticationError{}, time.Second)

	var levels []slog.Level
	var codes []string
	for _, record := range handler.snapshot() {
		if record.attrs["event"] != "dependency.retrying" {
			continue
		}
		levels = append(levels, record.level)
		code, _ := record.attrs["error_code"].(string)
		codes = append(codes, code)
	}
	wantLevels := []slog.Level{slog.LevelWarn, slog.LevelWarn, slog.LevelDebug}
	wantCodes := []string{"authentication_failed", "upstream_rejected", "authentication_failed"}
	if len(levels) != len(wantLevels) {
		t.Fatalf("retry records = %d, want %d: %#v", len(levels), len(wantLevels), handler.snapshot())
	}
	for index := range wantLevels {
		if levels[index] != wantLevels[index] || codes[index] != wantCodes[index] {
			t.Fatalf(
				"retry record %d = (%v, %q), want (%v, %q)",
				index, levels[index], codes[index], wantLevels[index], wantCodes[index],
			)
		}
	}
}

func TestRetryEpisodeResetsWarnedClassesAfterRecovery(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	session := newRecordingSession()
	migrationAdapter := newRecordingAdapter(t, session, "http://127.0.0.1:1", "test-token", handler)

	ctx := context.Background()
	var episode retryEpisode
	migrationAdapter.logConnectionRetry(ctx, &episode, &AuthenticationError{}, time.Second)
	migrationAdapter.logUpstreamReady(ctx, &episode)
	if episode.active() {
		t.Fatalf("episode still active after recovery: %#v", episode)
	}
	migrationAdapter.logConnectionRetry(ctx, &episode, &AuthenticationError{}, time.Second)

	if got := handler.count(slog.LevelWarn, "dependency.retrying"); got != 2 {
		t.Fatalf("warn retry records = %d, want 2 (one per episode)", got)
	}
	if got := handler.count(slog.LevelDebug, "dependency.retrying"); got != 0 {
		t.Fatalf("debug retry records = %d, want 0", got)
	}
	if got := handler.count(slog.LevelInfo, "dependency.recovered"); got != 1 {
		t.Fatalf("recovered records = %d, want 1", got)
	}
}

func TestCancelledRetryBackoffEmitsNoAdditionalWarning(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	session := newRecordingSession()
	migrationAdapter := newRecordingAdapter(t, session, "http://127.0.0.1:1", "test-token", handler)

	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- migrationAdapter.Run(ctx) }()
	waitForLogCondition(t, func() bool {
		return handler.count(slog.LevelWarn, "dependency.retrying") == 1
	})
	cancel()
	if err := <-runResult; err != nil {
		t.Fatal(err)
	}

	if got := handler.count(slog.LevelWarn, "dependency.retrying"); got != 1 {
		t.Fatalf("warn retry records = %d, want exactly 1 with no cancellation warning", got)
	}
	for _, record := range handler.snapshot() {
		if record.level == slog.LevelError {
			t.Fatalf("cancellation produced an Error record: %#v", record)
		}
		if event, ok := record.attrs["event"].(string); ok {
			switch event {
			case "dependency.connected", "dependency.disconnected", "dependency.reconnected", "dependency.closed":
				t.Fatalf("unexpected SDK-owned event %q: %#v", event, record)
			}
		}
	}
}

// newReconnectScriptedServer serves one failing snapshot connection and then a
// healthy one, so retry, recovery, and readiness evidence can be asserted.
func newReconnectScriptedServer(
	t *testing.T,
	session *recordingSession,
) (*httptest.Server, <-chan error) {
	t.Helper()
	return newScriptedServer(t, func(ctx context.Context, connection *websocket.Conn) error {
		subscribe, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		if err := writeResult(ctx, connection, subscribe.ID, nil); err != nil {
			return err
		}
		snapshot, requestErr := readRequest(ctx, connection)
		if requestErr != nil {
			return requestErr
		}
		state := "off"
		if len(session.values()) > 0 {
			state = "on"
		}
		if err := writeResult(ctx, connection, snapshot.ID, []upstreamState{{
			EntityID: testExternalEntityID, State: state,
			LastUpdated: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		}}); err != nil {
			return err
		}
		if state == "off" {
			<-session.published
			return connection.CloseNow()
		}
		_, _, _ = connection.Read(ctx)
		return nil
	})
}

func assertRecoveryRecords(t *testing.T, handler *recordingHandler) {
	t.Helper()
	for _, record := range handler.snapshot() {
		if record.attrs["event"] == "adapter.upstream_ready" &&
			record.attrs["entity_id"] != testEntityID {
			t.Fatalf("upstream_ready entity_id = %v, want canonical %q", record.attrs["entity_id"], testEntityID)
		}
		if record.attrs["event"] == "dependency.recovered" {
			if record.attrs["attempts"] == int64(0) || record.attrs["attempts"] == nil {
				t.Fatalf("recovered record misses attempts: %#v", record.attrs)
			}
			if _, exists := record.attrs["duration_ms"]; !exists {
				t.Fatalf("recovered record misses duration_ms: %#v", record.attrs)
			}
		}
	}
	if handler.containsText(testExternalEntityID) {
		t.Fatalf("logs export vendor Entity identity %q", testExternalEntityID)
	}
	for _, record := range handler.snapshot() {
		if event, ok := record.attrs["event"].(string); ok {
			switch event {
			case "dependency.connected", "dependency.disconnected", "dependency.reconnected", "dependency.closed":
				t.Fatalf("unexpected SDK-owned event %q: %#v", event, record)
			}
		}
	}
}

func TestReconnectEmitsRecoveryAndUpstreamReady(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	session := newRecordingSession()
	server, serverErrors := newReconnectScriptedServer(t, session)
	defer server.Close()

	migrationAdapter := newRecordingAdapter(t, session, server.URL, "test-token", handler)
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- migrationAdapter.Run(ctx) }()
	waitForLogCondition(t, func() bool {
		return handler.count(slog.LevelInfo, "adapter.upstream_ready") == 2 &&
			handler.count(slog.LevelInfo, "dependency.recovered") == 1 &&
			handler.count(slog.LevelWarn, "dependency.retrying") == 1
	})
	cancel()
	if err := <-runResult; err != nil {
		t.Fatal(err)
	}
	assertNoServerError(t, serverErrors)
	assertRecoveryRecords(t, handler)
}

func TestUnsupportedStateOmitsUpstreamPayload(t *testing.T) {
	t.Parallel()
	const payloadSentinel = "payload-sentinel-4c2e8b6a"
	handler := newRecordingHandler()
	session := newRecordingSession()
	migrationAdapter := newRecordingAdapter(t, session, "http://127.0.0.1:1", "test-token", handler)

	if err := migrationAdapter.processState(context.Background(), upstreamState{
		EntityID: testExternalEntityID, State: payloadSentinel,
	}, time.Now().UTC()); err == nil {
		t.Fatal("unsupported State returned nil")
	}
	migrationAdapter.logUnsupportedState(context.Background(), "snapshot")

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
	handler := newRecordingHandler()
	session := newRecordingSession()
	migrationAdapter := newRecordingAdapter(t, session, "http://127.0.0.1:1", "test-token", handler)

	if err := migrationAdapter.publish(context.Background(), upstreamState{
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
