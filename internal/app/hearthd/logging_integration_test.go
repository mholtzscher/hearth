package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
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

func waitForRecord(
	t *testing.T,
	handler *recordingHandler,
	event string,
	timeout time.Duration,
) slog.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, record := range recordsWithEvent(handler.snapshot(), event) {
			return record
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log event %q", event)
	return slog.Record{}
}

// This test protects the explicit-listener contract and fails if a busy HTTP
// port ever emits core.http_listening or hides the failed stage.
func TestRunHTTPBindFailureEmitsNoListeningEvent(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelInfo)

	server := startLifecycleNATSServer(t)
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Close() })

	runErr := Run(ctx, Config{HouseholdTimezone: "UTC",
		HTTPAddr:   blocker.Addr().String(),
		NATSURL:    server.ClientURL(),
		SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
	}, logger)
	if runErr == nil {
		t.Fatal("Run succeeded with a busy HTTP port")
	}
	if stage := ErrorStage(runErr); stage != "http_listen" {
		t.Fatalf("Run error stage = %q, want %q (err: %v)", stage, "http_listen", runErr)
	}

	records := recorder.snapshot()
	if listening := recordsWithEvent(records, "core.http_listening"); len(listening) != 0 {
		t.Fatalf("busy port emitted core.http_listening: %#v", listening)
	}
	stages := recordsWithEvent(records, "core.startup_stage_completed")
	if len(stages) == 0 {
		t.Fatal("missing core.startup_stage_completed records")
	}
	requireRecordAttr(t, stages[0], "component", "core")
	if cleanup := recordsWithEvent(records, "process.cleanup_failed"); len(cleanup) != 0 {
		t.Fatalf("failed startup emitted process.cleanup_failed: %#v", cleanup)
	}
}

// This test protects the normal core lifecycle and fails if HTTP startup
// evidence, readiness, clean cancellation, or error-free shutdown regresses.
func TestRunCancelsCleanlyAfterReady(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelInfo)

	server := startLifecycleNATSServer(t)
	runContext, stopRun := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{HouseholdTimezone: "UTC",
			HTTPAddr:   freeLoopbackAddr(t),
			NATSURL:    server.ClientURL(),
			SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
		}, logger)
	}()

	listening := waitForRecord(t, recorder, "core.http_listening", 15*time.Second)
	requireRecordAttr(t, listening, "component", "core")
	httpAddr, addrOk := recordAttr(listening, "http_addr")
	if !addrOk || httpAddr.String() == "" {
		t.Fatalf("core.http_listening omitted http_addr: %#v", listening)
	}
	pollReadyz(ctx, t, "http://"+httpAddr.String()+"/readyz")

	stopRun()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatalf("Run returned after cancellation: %v", runErr)
		}
	case <-ctx.Done():
		t.Fatalf("Run did not stop after cancellation: %v", ctx.Err())
	}

	records := recorder.snapshot()
	if cleanup := recordsWithEvent(records, "process.cleanup_failed"); len(cleanup) != 0 {
		t.Fatalf("clean shutdown emitted process.cleanup_failed: %#v", cleanup)
	}
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

func startLifecycleNATSServer(t *testing.T) *natsserver.Server {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	return addr
}

func pollReadyz(ctx context.Context, t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for readyz: %v", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("readyz at %s never became ready", url)
}
