package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// lockedTestLogWriter is a concurrency-safe slog destination. JetStream
// consumer callbacks log from background goroutines, so tests must never read
// an unlocked [bytes.Buffer] while callbacks write to it.
type lockedTestLogWriter struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (writer *lockedTestLogWriter) Write(payload []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.Write(payload)
}

func (writer *lockedTestLogWriter) output() string {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.String()
}

func (writer *lockedTestLogWriter) records(t *testing.T) []map[string]any {
	t.Helper()
	var parsed []map[string]any
	for line := range strings.SplitSeq(writer.output(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, line)
		}
		parsed = append(parsed, record)
	}
	return parsed
}

func newTestLogSink() (*lockedTestLogWriter, *slog.Logger) {
	writer := &lockedTestLogWriter{}
	return writer, slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func logEvents(records []map[string]any, event string) []map[string]any {
	var matched []map[string]any
	for _, record := range records {
		if record["event"] == event {
			matched = append(matched, record)
		}
	}
	return matched
}

// transportContextObserver records whether each handled log record observed
// the originating operation context. Transport handlers run on consumer and
// subscription callbacks, so the check runs inside Handle while the writer
// stays locked separately.
type transportContextObserver struct {
	mutex    sync.Mutex
	observed []bool
}

func (observer *transportContextObserver) record(ctx context.Context, check func(context.Context) bool) {
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	observer.observed = append(observer.observed, check(ctx))
}

func (observer *transportContextObserver) allObserved() bool {
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	if len(observer.observed) == 0 {
		return false
	}
	for _, seen := range observer.observed {
		if !seen {
			return false
		}
	}
	return true
}

// contextObservingTransportHandler forwards records to JSON while capturing
// context preservation evidence for transport emission sites.
type contextObservingTransportHandler struct {
	inner    slog.Handler
	check    func(context.Context) bool
	observer *transportContextObserver
}

func (handler *contextObservingTransportHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.inner.Enabled(ctx, level)
}

func (handler *contextObservingTransportHandler) Handle(
	ctx context.Context,
	record slog.Record,
) error {
	handler.observer.record(ctx, handler.check)
	return handler.inner.Handle(ctx, record)
}

func (handler *contextObservingTransportHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextObservingTransportHandler{
		inner:    handler.inner.WithAttrs(attrs),
		check:    handler.check,
		observer: handler.observer,
	}
}

func (handler *contextObservingTransportHandler) WithGroup(name string) slog.Handler {
	return &contextObservingTransportHandler{
		inner:    handler.inner.WithGroup(name),
		check:    handler.check,
		observer: handler.observer,
	}
}

func newContextObservingSink(
	check func(context.Context) bool,
) (*lockedTestLogWriter, *transportContextObserver, *slog.Logger) {
	writer := &lockedTestLogWriter{}
	observer := &transportContextObserver{}
	logger := slog.New(&contextObservingTransportHandler{
		inner:    slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}),
		check:    check,
		observer: observer,
	})
	return writer, observer, logger
}

// observedSpanContext reports whether the log context carries the trace
// extracted from incoming transport headers.
func observedSpanContext(ctx context.Context) bool {
	return trace.SpanContextFromContext(ctx).IsValid()
}

// testTraceContext returns a context carrying a fixed sampled span for
// header injection in transport tests.
func testTraceContext(t *testing.T) context.Context {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled, Remote: true,
	})
	return trace.ContextWithSpanContext(context.Background(), spanContext)
}
