//go:build integration

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func waitForRecord(
	t *testing.T,
	handler *recordingHandler,
	event string,
	timeout time.Duration,
	runErrors <-chan error,
) slog.Record {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, record := range recordsWithEvent(handler.snapshot(), event) {
			return record
		}
		select {
		case runErr := <-runErrors:
			t.Fatalf("Core exited before log event %q: stage=%s error=%v", event, ErrorStage(runErr), runErr)
		case <-time.After(10 * time.Millisecond):
		}
	}
	var stages []string
	for _, record := range recordsWithEvent(handler.snapshot(), "core.startup_stage_completed") {
		if stage, ok := recordAttr(record, "stage"); ok {
			stages = append(stages, stage.String())
		}
	}
	t.Fatalf("timed out waiting for log event %q; completed startup stages: %v", event, stages)
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
		Agent:      requiredAgentConfig(t),
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
	// Migration behavior has its own fresh-database tests. Keep setup outside
	// this lifecycle test's readiness/cancellation budget.
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	if err := dbtest.OpenMigrated(t, databasePath).Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelInfo)

	server := startLifecycleNATSServer(t)
	httpAddress, httpOptions := reserveLoopbackListener(t)
	runContext, stopRun := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	completed := make(chan struct{})
	activationLowerBound := time.Now().UTC().Truncate(time.Minute)
	go func() {
		runErrors <- runWithOptions(runContext, Config{HouseholdTimezone: "UTC",
			HTTPAddr:   httpAddress,
			NATSURL:    server.ClientURL(),
			SQLitePath: databasePath,
			Agent:      requiredAgentConfig(t),
		}, logger, httpOptions)
		close(completed)
	}()
	t.Cleanup(func() { stopRun(); <-completed })

	listening := waitForRecord(t, recorder, "core.http_listening", 15*time.Second, runErrors)
	requireRecordAttr(t, listening, "component", "core")
	httpAddr, addrOk := recordAttr(listening, "http_addr")
	if !addrOk || httpAddr.String() == "" {
		t.Fatalf("core.http_listening omitted http_addr: %#v", listening)
	}
	pollReadyz(ctx, t, "http://"+httpAddr.String()+"/readyz")
	// Even an empty household must persist activation before declaring readiness.
	database, openErr := platformdb.Open(ctx, databasePath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = database.Close() }()
	var storedWatermark string
	if queryErr := database.QueryRowContext(ctx,
		"SELECT highwater_at FROM automation_schedule_watermarks WHERE id = 'global'",
	).Scan(&storedWatermark); queryErr != nil {
		t.Fatal(queryErr)
	}
	watermark, parseErr := time.Parse("2006-01-02T15:04:05.000000000Z", storedWatermark)
	if parseErr != nil || watermark.Before(activationLowerBound) ||
		watermark.After(time.Now().UTC().Truncate(time.Minute)) || !watermark.Equal(watermark.Truncate(time.Minute)) {
		t.Fatalf("startup watermark = %q, parse error = %v", storedWatermark, parseErr)
	}

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
