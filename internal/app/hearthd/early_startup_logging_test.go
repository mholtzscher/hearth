package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// This test protects early startup teardown and fails if a Migrate failure
// after the database opens returns without one process.stopping. The
// SQLite-enforced read-only URI makes Open succeed and Migrate fail for real
// (filesystem permissions are bypassed by root), so no assembly hooks are
// needed.
func TestRunMigrateFailureEmitsStoppingBeforeCleanup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelInfo)

	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	database, openErr := platformdb.Open(ctx, databasePath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	// A fresh file has no applied migrations, so reopening it through a
	// SQLite-enforced read-only URI turns the real Migrate write into a
	// failure while Open still succeeds.
	readOnlyPath := "file:" + databasePath + "?mode=ro"

	runErr := Run(ctx, Config{
		HTTPAddr:   freeLoopbackAddr(t),
		NATSURL:    "nats://127.0.0.1:1",
		SQLitePath: readOnlyPath,
	}, logger)
	if runErr == nil {
		t.Fatal("Run succeeded with an unmigratable database")
	}
	if stage := ErrorStage(runErr); stage != "migrate_database" {
		t.Fatalf("Run error stage = %q, want %q (err: %v)", stage, "migrate_database", runErr)
	}
	requireEarlyStartupStopping(t, recorder.snapshot(), "migrate_database", nil)
}

// This test protects early startup teardown and fails if an
// InterruptActiveCommands failure after migration returns without one
// process.stopping. The SQLite-enforced read-only URI over the migrated
// database makes Migrate a successful no-op and the interrupting write fail
// for real.
func TestRunInterruptCommandsFailureEmitsStoppingBeforeCleanup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelInfo)

	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	database, openErr := platformdb.Open(ctx, databasePath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	if migrateErr := platformdb.Migrate(ctx, database); migrateErr != nil {
		t.Fatal(migrateErr)
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	readOnlyPath := "file:" + databasePath + "?mode=ro"

	runErr := Run(ctx, Config{
		HTTPAddr:   freeLoopbackAddr(t),
		NATSURL:    "nats://127.0.0.1:1",
		SQLitePath: readOnlyPath,
	}, logger)
	if runErr == nil {
		t.Fatal("Run succeeded with a read-only migrated database")
	}
	if stage := ErrorStage(runErr); stage != "interrupt_commands" {
		t.Fatalf("Run error stage = %q, want %q (err: %v)", stage, "interrupt_commands", runErr)
	}
	requireEarlyStartupStopping(t, recorder.snapshot(), "interrupt_commands", []string{"database_migrated"})
}

// This test protects early startup teardown and fails if a NATS connect
// failure after command interruption returns without one process.stopping.
// The refused port fails the real dial while the database stays writable,
// so deferred database cleanup runs after the stopping record.
func TestRunConnectNATSFailureEmitsStoppingBeforeCleanup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelInfo)

	runErr := Run(ctx, Config{
		HTTPAddr:   freeLoopbackAddr(t),
		NATSURL:    "nats://127.0.0.1:1",
		SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
	}, logger)
	if runErr == nil {
		t.Fatal("Run succeeded with a refused NATS address")
	}
	if stage := ErrorStage(runErr); stage != "connect_nats" {
		t.Fatalf("Run error stage = %q, want %q (err: %v)", stage, "connect_nats", runErr)
	}
	requireEarlyStartupStopping(t, recorder.snapshot(), "connect_nats", []string{
		"database_migrated",
		"active_commands_interrupted",
	})
}

// This test protects startup cancellation reporting and fails if a startup
// teardown after cancellation claims startup_failed instead of
// context_cancelled, matching the adapter startupReason contract.
func TestFailStartupDistinguishesCancellation(t *testing.T) {
	t.Parallel()
	logger, recorder := withRecording(slog.LevelInfo)
	boom := errors.New("boom")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := failStartup(canceled, logger, "migrate_database", boom); ErrorStage(err) != "migrate_database" {
		t.Fatalf("canceled failStartup stage = %q, want migrate_database", ErrorStage(err))
	}
	stopping := recordsWithEvent(recorder.snapshot(), "process.stopping")
	if len(stopping) != 1 {
		t.Fatalf("process.stopping records = %d, want exactly one", len(stopping))
	}
	requireRecordAttr(t, stopping[0], "reason_code", "context_cancelled")
	requireRecordAttr(t, stopping[0], "stage", "migrate_database")

	liveLogger, liveRecorder := withRecording(slog.LevelInfo)
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	if err := failStartup(ctx, liveLogger, "connect_nats", boom); ErrorStage(err) != "connect_nats" {
		t.Fatalf("failed failStartup stage = %q, want connect_nats", ErrorStage(err))
	}
	liveStopping := recordsWithEvent(liveRecorder.snapshot(), "process.stopping")
	if len(liveStopping) != 1 {
		t.Fatalf("process.stopping records = %d, want exactly one", len(liveStopping))
	}
	requireRecordAttr(t, liveStopping[0], "reason_code", "startup_failed")

	silentLogger, silentRecorder := withRecording(slog.LevelInfo)
	if nilErr := failStartup(ctx, silentLogger, "connect_nats", nil); nilErr != nil {
		t.Fatalf("failStartup with nil error = %v, want nil", nilErr)
	}
	if silent := recordsWithEvent(silentRecorder.snapshot(), "process.stopping"); len(silent) != 0 {
		t.Fatalf("nil failure emitted process.stopping: %#v", silent)
	}
}

// requireEarlyStartupStopping asserts the single process.stopping teardown
// for a failed startup stage: the safe failed stage, no invented completed
// stage, and every completed stage ordered before stopping. Deferred cleanup
// runs after the stopping log by construction (failStartup logs before Run
// returns into its defers), and a silent successful cleanup leaves no later
// record; that ordering is asserted as far as it stays observable without
// assembly hooks.
func requireEarlyStartupStopping(
	t *testing.T,
	records []slog.Record,
	failedStage string,
	completedStages []string,
) {
	t.Helper()
	stopping := recordsWithEvent(records, "process.stopping")
	if len(stopping) != 1 {
		t.Fatalf("process.stopping records = %d, want exactly one before teardown", len(stopping))
	}
	requireRecordAttr(t, stopping[0], "component", "process")
	requireRecordAttr(t, stopping[0], "reason_code", "startup_failed")
	requireRecordAttr(t, stopping[0], "stage", failedStage)
	if value, ok := recordAttr(stopping[0], "error"); ok {
		t.Fatalf("process.stopping leaks raw error text: %#v", value)
	}

	stoppingIndex := -1
	completed := map[string]int{}
	for index, record := range records {
		event, eventOk := recordAttr(record, "event")
		if !eventOk {
			continue
		}
		switch event.String() {
		case "process.stopping":
			stoppingIndex = index
		case "core.startup_stage_completed":
			stage, stageOk := recordAttr(record, "stage")
			if !stageOk {
				t.Fatalf("core.startup_stage_completed omitted stage: %#v", record)
			}
			completed[stage.String()] = index
		}
	}
	if stoppingIndex == -1 {
		t.Fatal("missing process.stopping index")
	}
	if len(completed) != len(completedStages) {
		t.Fatalf("completed startup stages = %v, want %v", completed, completedStages)
	}
	for _, stage := range completedStages {
		index, ok := completed[stage]
		if !ok {
			t.Fatalf("missing core.startup_stage_completed for stage %q", stage)
		}
		if index > stoppingIndex {
			t.Fatalf("stage %q completed after process.stopping", stage)
		}
	}
	if listening := recordsWithEvent(records, "core.http_listening"); len(listening) != 0 {
		t.Fatalf("failed startup emitted core.http_listening: %#v", listening)
	}
	if cleanup := recordsWithEvent(records, "process.cleanup_failed"); len(cleanup) != 0 {
		t.Fatalf("failed startup emitted process.cleanup_failed: %#v", cleanup)
	}
	if failures := errorRecords(records); len(failures) != 0 {
		t.Fatalf("failed startup emitted Error records: %#v", failures)
	}
}
