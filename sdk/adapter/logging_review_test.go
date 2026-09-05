package adapter //nolint:testpackage // Tests exercise package-private logging behavior.

// Review regression tests for the SDK logging findings: Connect-context
// independence after lifecycle establishment, the safe NATS async
// ErrorHandler, and retry-episode state reset. They live here (not in
// logging_test.go) to avoid conflicting with concurrent work in that file.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
)

// Cancelling the Connect context after a successful Connect must not quiet
// unexpected disconnect/reconnect/closed diagnostics: the session lifecycle
// is independent via WithoutCancel, so only explicit/session lifecycle state
// classifies teardown thereafter.
func TestReviewConnectCancelAfterSuccessStaysUnexpected(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	startTestLifecycleResponder(t, server.ClientURL())
	writer := &lockedWriter{}
	config := testConfig(server.ClientURL())
	config.Logger = recordingLogger(t, writer)
	connectCtx, cancelConnect := context.WithCancel(context.Background())
	session, err := Connect(connectCtx, config)
	if err != nil {
		cancelConnect()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	cancelConnect()

	session.stateMutex.Lock()
	lifecycle := session.lifecycleCtx
	session.stateMutex.Unlock()
	if lifecycle == nil {
		t.Fatal("session lifecycle was not established")
	}
	select {
	case <-lifecycle.Done():
		t.Fatal("cancelling the Connect context cancelled the independent session lifecycle")
	default:
	}
	if session.isExpectedClose() {
		t.Fatal("isExpectedClose uses the cancelled Connect context after the lifecycle was established")
	}
	// The initial connected milestone is unrelated to this regression; force
	// it so the reconnected path is deterministic without waiting on the
	// client's async connect callback.
	session.connectedOnce.Store(true)

	session.onDisconnected(nil, errors.New("SENTINEL-review-disconnect"))
	session.onReconnected(nil)
	session.onClosed(nil)

	records := parseLogRecords(t, writer.snapshot())
	var disconnected, reconnected, closed map[string]any
	for _, record := range records {
		switch record["event"] {
		case "dependency.disconnected":
			disconnected = record
		case "dependency.reconnected":
			reconnected = record
		case "dependency.closed":
			if record["level"] == "ERROR" {
				closed = record
			}
		}
	}
	if disconnected == nil || disconnected["level"] != "WARN" || disconnected["dependency"] != "nats" {
		t.Errorf("disconnect after Connect-context cancel = %v, want WARN dependency nats", disconnected)
	}
	if reconnected == nil || reconnected["level"] != "INFO" || reconnected["dependency"] != "nats" {
		t.Errorf("reconnect after Connect-context cancel = %v, want INFO dependency nats", reconnected)
	}
	if closed == nil || closed["reason_code"] != "unexpected_close" {
		t.Errorf("closed after Connect-context cancel = %v, want ERROR reason unexpected_close", closed)
	}
	if strings.Contains(writer.snapshot(), "SENTINEL-review-disconnect") {
		t.Error("disconnect error text leaked into logs")
	}
}

// The NATS async ErrorHandler must emit a fixed diagnostic code without the
// raw error text or subscription subject the default handler prints to
// stderr.
func TestReviewAsyncErrorHandlerIsSafe(t *testing.T) {
	t.Parallel()
	server := startServer(t, -1, t.TempDir())
	startTestLifecycleResponder(t, server.ClientURL())
	writer := &lockedWriter{}
	session := connectSessionWithLogger(t, server.ClientURL(), recordingLogger(t, writer))

	subscription := &natsgo.Subscription{Subject: "SENTINEL-review-subject"}
	session.onAsyncError(nil, subscription,
		errors.New("SENTINEL-review-async token=hunter2"))

	failed := waitForLogRecord(t, writer, "dependency.operation_failed", nil, 3*time.Second)
	if failed["level"] != "ERROR" {
		t.Errorf("async error level = %v, want ERROR", failed["level"])
	}
	if failed["dependency"] != "nats" || failed["error_code"] != "nats_async_error" {
		t.Errorf("async error record = %v, want dependency nats code nats_async_error", failed)
	}
	for _, sentinel := range []string{"SENTINEL-review-async", "SENTINEL-review-subject", "hunter2"} {
		if strings.Contains(writer.snapshot(), sentinel) {
			t.Errorf("async error leaked %q into logs", sentinel)
		}
	}
}

// A reused retry episode must reset after success: the heartbeat loop
// continues to a newer generation with the same episode, and stale attempts
// would otherwise emit duplicate recovery for work that needed no retries.
func TestReviewRetryEpisodeSucceededResets(t *testing.T) {
	t.Parallel()
	writer := &lockedWriter{}
	logger := recordingLogger(t, writer)
	ctx := context.Background()

	episode := newRetryEpisode("heartbeat", "core")
	if err := episode.waitRetry(ctx, logger, slog.String("error_code", "no_responders"),
		time.Millisecond, natsgo.ErrNoResponders); err != nil {
		t.Fatal(err)
	}
	if err := episode.waitRetry(ctx, logger, slog.String("error_code", "no_responders"),
		time.Millisecond, natsgo.ErrNoResponders); err != nil {
		t.Fatal(err)
	}
	episode.succeeded(ctx, logger)
	// Continuing to a newer generation with no new failures must stay
	// silent: this second call is the heartbeat duplicate.
	episode.succeeded(ctx, logger)

	var recovered []map[string]any
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "dependency.recovered" {
			recovered = append(recovered, record)
		}
	}
	if len(recovered) != 1 {
		t.Fatalf("recovered records = %d, want 1 (no duplicate after reset)", len(recovered))
	}
	if recovered[0]["attempt"] != float64(3) {
		t.Errorf("recovery attempt = %v, want 3", recovered[0]["attempt"])
	}

	// The next failure starts a fresh episode at WARN attempt 1, proving the
	// warned-class state was reset as well.
	if err := episode.waitRetry(ctx, logger, slog.String("error_code", "no_responders"),
		time.Millisecond, natsgo.ErrNoResponders); err != nil {
		t.Fatal(err)
	}
	var retrying []map[string]any
	for _, record := range parseLogRecords(t, writer.snapshot()) {
		if record["event"] == "dependency.retrying" {
			retrying = append(retrying, record)
		}
	}
	last := retrying[len(retrying)-1]
	if last["level"] != "WARN" || last["attempt"] != float64(1) {
		t.Errorf("post-reset retry = %v, want WARN attempt 1", last)
	}
}
