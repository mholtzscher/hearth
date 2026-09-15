package hearthd //nolint:testpackage // Tests exercise package-private scheduling and lifecycle behavior.

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

// stubHistoryPruner records each sweep it receives so scheduler tests can
// observe pass composition, task order, and shared timestamps. When permits is
// non-nil, every call blocks until the test sends one permit or the context is
// canceled, which lets a test hold a pass open while ticks elapse.
type stubHistoryPruner struct {
	name    string
	order   *historyPruneOrderLog
	permits chan struct{}
	fail    error

	mutex  sync.Mutex
	sweeps []time.Time
}

func (pruner *stubHistoryPruner) PruneHistory(ctx context.Context, now time.Time) error {
	pruner.mutex.Lock()
	pruner.sweeps = append(pruner.sweeps, now)
	pruner.mutex.Unlock()
	if pruner.order != nil {
		pruner.order.record(pruner.name)
	}
	if pruner.permits != nil {
		select {
		case <-pruner.permits:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return pruner.fail
}

func (pruner *stubHistoryPruner) recorded() []time.Time {
	pruner.mutex.Lock()
	defer pruner.mutex.Unlock()
	return append([]time.Time(nil), pruner.sweeps...)
}

// historyPruneOrderLog is a mutex-guarded record of task execution order shared
// by two stub pruners.
type historyPruneOrderLog struct {
	mutex sync.Mutex
	steps []string
}

func (log *historyPruneOrderLog) record(step string) {
	log.mutex.Lock()
	defer log.mutex.Unlock()
	log.steps = append(log.steps, step)
}

func (log *historyPruneOrderLog) recorded() []string {
	log.mutex.Lock()
	defer log.mutex.Unlock()
	return append([]string(nil), log.steps...)
}

// This test protects the startup-then-hourly schedule and fails if the startup
// pass never runs, runs more than once, runs before the first interval, or a
// tick starts a pass that does not share one UTC sweep time across tasks in
// devices-then-automations order.
func TestHistoryPruneSchedulerRunsStartupPassThenHourlyTicks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		order := &historyPruneOrderLog{}
		devicePruner := &stubHistoryPruner{name: devicesHistoryPruneModule, order: order}
		automationPruner := &stubHistoryPruner{name: automationsHistoryPruneModule, order: order}
		scheduler := newHistoryPruneScheduler(
			slog.New(slog.DiscardHandler),
			newHistoryPruneTasks(devicePruner, automationPruner),
		)
		worker := lifecycle.StartWorker(t.Context(), scheduler.run)
		synctest.Wait()

		startup := requireHistoryPrunePass(t, devicePruner, automationPruner, order, 1)
		if startup.Location() != time.UTC {
			t.Fatalf("startup sweep location = %v, want UTC", startup.Location())
		}

		// No pass runs before the first hourly interval elapses.
		time.Sleep(historyPruneInterval - time.Second)
		synctest.Wait()
		requireHistoryPruneSweeps(t, devicePruner, 1, "before the first tick")

		// The first tick starts exactly one more pass with a newer shared time.
		time.Sleep(time.Second)
		synctest.Wait()
		tick := requireHistoryPrunePass(t, devicePruner, automationPruner, order, 2)
		if !tick.After(startup) {
			t.Fatalf("tick sweep %v is not after startup sweep %v", tick, startup)
		}

		if stopErr := worker.Stop(context.Background()); stopErr != nil {
			t.Fatalf("stopping the scheduler returned %v", stopErr)
		}
	})
}

// requireHistoryPrunePass fails unless each pruner recorded exactly want sweeps,
// the latest sweep is one shared UTC instant, and the latest pass ran devices
// before automations. It returns the shared sweep time.
func requireHistoryPrunePass(
	t *testing.T,
	devicePruner, automationPruner *stubHistoryPruner,
	order *historyPruneOrderLog,
	want int,
) time.Time {
	t.Helper()
	requireHistoryPruneSweeps(t, devicePruner, want, "in pass")
	requireHistoryPruneSweeps(t, automationPruner, want, "in pass")
	deviceSweeps := devicePruner.recorded()
	sweep := deviceSweeps[want-1]
	if automationSweep := automationPruner.recorded()[want-1]; !sweep.Equal(automationSweep) {
		t.Fatalf(
			"pass %d sweeps differ: devices %v, automations %v",
			want, sweep, automationSweep,
		)
	}
	steps := order.recorded()
	if len(steps) != want*2 {
		t.Fatalf("task order after pass %d = %v, want %d entries", want, steps, want*2)
	}
	lastPass := steps[len(steps)-2:]
	if lastPass[0] != devicesHistoryPruneModule || lastPass[1] != automationsHistoryPruneModule {
		t.Fatalf("pass %d task order = %v, want devices then automations", want, lastPass)
	}
	return sweep
}

// requireHistoryPruneSweeps fails unless exactly want sweeps were recorded.
func requireHistoryPruneSweeps(t *testing.T, pruner *stubHistoryPruner, want int, when string) {
	t.Helper()
	if sweeps := pruner.recorded(); len(sweeps) != want {
		t.Fatalf("%s sweeps %s = %d, want %d", pruner.name, when, len(sweeps), want)
	}
}

// This test protects the non-overlapping, no-backlog schedule and fails if a
// tick starts a second pass while one is running, if the startup pass runs
// concurrently with automations, or if a pass that overran its interval replays
// the missed ticks as catch-up passes.
func TestHistoryPruneSchedulerSkipsTicksMissedBySlowPass(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		permits := make(chan struct{})
		devicePruner := &stubHistoryPruner{name: devicesHistoryPruneModule, permits: permits}
		automationPruner := &stubHistoryPruner{name: automationsHistoryPruneModule}
		scheduler := newHistoryPruneScheduler(
			slog.New(slog.DiscardHandler),
			newHistoryPruneTasks(devicePruner, automationPruner),
		)
		worker := lifecycle.StartWorker(t.Context(), scheduler.run)

		// The startup pass blocks inside devices, so automations has not started.
		synctest.Wait()
		if sweeps := devicePruner.recorded(); len(sweeps) != 1 {
			t.Fatalf("device sweeps while the startup pass blocks = %d, want 1", len(sweeps))
		}
		if sweeps := automationPruner.recorded(); len(sweeps) != 0 {
			t.Fatalf("automation sweeps before devices finished = %d, want 0", len(sweeps))
		}

		// Ticks that elapse during the startup pass cannot run: the hour ticker
		// starts only after the startup pass completes.
		time.Sleep(2 * historyPruneInterval)
		synctest.Wait()
		if sweeps := devicePruner.recorded(); len(sweeps) != 1 {
			t.Fatalf("a tick overlapped the startup pass: device sweeps = %d", len(sweeps))
		}
		if sweeps := automationPruner.recorded(); len(sweeps) != 0 {
			t.Fatalf("a tick overlapped the startup pass: automation sweeps = %d", len(sweeps))
		}

		// Releasing the startup pass lets automations run in the same pass.
		permits <- struct{}{}
		synctest.Wait()
		if sweeps := automationPruner.recorded(); len(sweeps) != 1 {
			t.Fatalf("automation sweeps after the startup pass = %d, want 1", len(sweeps))
		}

		// The next pass starts one interval after the previous pass completed and
		// blocks inside devices again.
		time.Sleep(historyPruneInterval)
		synctest.Wait()
		if sweeps := devicePruner.recorded(); len(sweeps) != 2 {
			t.Fatalf("device sweeps after one interval = %d, want 2", len(sweeps))
		}
		// Two more intervals elapse while this pass blocks; no pass may overlap it.
		time.Sleep(2 * historyPruneInterval)
		synctest.Wait()
		if sweeps := devicePruner.recorded(); len(sweeps) != 2 {
			t.Fatalf("device sweeps while a pass blocks = %d, want no overlapping pass", len(sweeps))
		}
		// Completing the late pass skips the missed ticks: no catch-up pass starts
		// immediately, only one pass runs for this completion.
		permits <- struct{}{}
		synctest.Wait()
		if sweeps := devicePruner.recorded(); len(sweeps) != 2 {
			t.Fatalf(
				"device sweeps after releasing a late pass = %d, want 2 (missed ticks skipped)",
				len(sweeps),
			)
		}
		if sweeps := automationPruner.recorded(); len(sweeps) != 2 {
			t.Fatalf("automation sweeps after releasing a late pass = %d, want 2", len(sweeps))
		}

		if stopErr := worker.Stop(context.Background()); stopErr != nil {
			t.Fatalf("stopping the scheduler returned %v", stopErr)
		}
	})
}

// historyPruneFailureCase names one failing module plus the fixed event, error
// code, and other-module event its failed pass must log under.
type historyPruneFailureCase struct {
	name             string
	failingModule    string
	secret           string
	wantEvent        string
	wantErrorCode    string
	otherModuleEvent string
}

// historyPruneFailureCases tables both modules so each module's preserved event
// and error code stay asserted together.
func historyPruneFailureCases() []historyPruneFailureCase {
	return []historyPruneFailureCase{
		{
			name:             "devices",
			failingModule:    devicesHistoryPruneModule,
			secret:           "s3cr3t-devices-detail",
			wantEvent:        "core.devices_history_prune_failed",
			wantErrorCode:    "devices_history_prune_failed",
			otherModuleEvent: "core.automation_history_prune_failed",
		},
		{
			name:             "automations",
			failingModule:    automationsHistoryPruneModule,
			secret:           "s3cr3t-automations-detail",
			wantEvent:        "core.automation_history_prune_failed",
			wantErrorCode:    "automation_history_prune_failed",
			otherModuleEvent: "core.devices_history_prune_failed",
		},
	}
}

// This test protects module failure isolation and safe failure logging for each
// module. It fails if a failed module pass suppresses the other module, logs
// more than once per failed module pass, leaks raw error text, or omits the
// fixed event, error code, or module name.
func TestHistoryPruneSchedulerContinuesAfterModuleFailure(t *testing.T) {
	t.Parallel()
	for _, testCase := range historyPruneFailureCases() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			runHistoryPruneFailureCase(t, testCase)
		})
	}
}

// historyPruneFailureRun holds one failing-module run's observable state: the
// pruners, the recording handler holding its logs, and the running worker.
type historyPruneFailureRun struct {
	testCase         historyPruneFailureCase
	recorder         *recordingHandler
	devicePruner     *stubHistoryPruner
	automationPruner *stubHistoryPruner
	worker           *lifecycle.WorkerHandle
}

// runHistoryPruneFailureCase drives one failing module through a startup pass
// and a retry pass inside its own synctest bubble, asserting the failed pass,
// its single fixed log record, the untouched other module, and the retry.
func runHistoryPruneFailureCase(t *testing.T, testCase historyPruneFailureCase) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		run := startFailingHistoryPruneRun(t, testCase)
		synctest.Wait()

		requireHistoryPruneFailureLogged(t, run)
		requireHistoryPruneFailureRetried(t, run)

		if stopErr := run.worker.Stop(context.Background()); stopErr != nil {
			t.Fatalf("stopping the scheduler returned %v", stopErr)
		}
	})
}

// startFailingHistoryPruneRun builds both pruners, makes testCase's module fail
// with secret-bearing error text, and starts the scheduler on a fresh context.
func startFailingHistoryPruneRun(
	t *testing.T,
	testCase historyPruneFailureCase,
) historyPruneFailureRun {
	t.Helper()
	logger, recorder := withRecording(slog.LevelDebug)
	run := historyPruneFailureRun{
		testCase:         testCase,
		recorder:         recorder,
		devicePruner:     &stubHistoryPruner{name: devicesHistoryPruneModule},
		automationPruner: &stubHistoryPruner{name: automationsHistoryPruneModule},
	}
	run.failedPruner().fail = errors.New("sweep failed " + testCase.secret)
	scheduler := newHistoryPruneScheduler(
		logger, newHistoryPruneTasks(run.devicePruner, run.automationPruner),
	)
	run.worker = lifecycle.StartWorker(t.Context(), scheduler.run)
	return run
}

// failedPruner returns the pruner for the module this run expects to fail.
func (run historyPruneFailureRun) failedPruner() *stubHistoryPruner {
	if run.testCase.failingModule == automationsHistoryPruneModule {
		return run.automationPruner
	}
	return run.devicePruner
}

// requireHistoryPruneFailureLogged fails unless the failed startup pass swept
// both modules once at one shared instant and logged exactly one error record
// for the failing module that carries its fixed error code and module name, no
// raw upstream text, and no record for the other module.
func requireHistoryPruneFailureLogged(t *testing.T, run historyPruneFailureRun) {
	t.Helper()
	deviceSweeps := run.devicePruner.recorded()
	automationSweeps := run.automationPruner.recorded()
	if len(deviceSweeps) != 1 || len(automationSweeps) != 1 {
		t.Fatalf(
			"sweeps after a failing %s pass = %d devices, %d automations, want 1 each",
			run.testCase.failingModule, len(deviceSweeps), len(automationSweeps),
		)
	}
	if !automationSweeps[0].Equal(deviceSweeps[0]) {
		t.Fatalf(
			"modules did not share the failed pass sweep time: %v vs %v",
			automationSweeps[0], deviceSweeps[0],
		)
	}
	failures := recordsWithEvent(run.recorder.snapshot(), run.testCase.wantEvent)
	if len(failures) != 1 {
		t.Fatalf("%s records = %d, want 1", run.testCase.wantEvent, len(failures))
	}
	if failures[0].Level != slog.LevelError {
		t.Fatalf("failure level = %v, want Error", failures[0].Level)
	}
	requireRecordAttr(t, failures[0], "error_code", run.testCase.wantErrorCode)
	requireRecordAttr(t, failures[0], "module", run.testCase.failingModule)
	requireNoRawHistoryPruneError(t, failures[0], run.testCase.secret)
	if got := len(recordsWithEvent(run.recorder.snapshot(), run.testCase.otherModuleEvent)); got != 0 {
		t.Fatalf("%s records = %d, want 0", run.testCase.otherModuleEvent, got)
	}
}

// requireHistoryPruneFailureRetried advances one interval and fails unless both
// modules ran another pass and the failing module logged exactly one more
// record.
func requireHistoryPruneFailureRetried(t *testing.T, run historyPruneFailureRun) {
	t.Helper()
	time.Sleep(historyPruneInterval)
	synctest.Wait()
	if sweeps := run.devicePruner.recorded(); len(sweeps) != 2 {
		t.Fatalf("device sweeps on the retry pass = %d, want 2", len(sweeps))
	}
	if sweeps := run.automationPruner.recorded(); len(sweeps) != 2 {
		t.Fatalf("automation sweeps on the retry pass = %d, want 2", len(sweeps))
	}
	retried := recordsWithEvent(run.recorder.snapshot(), run.testCase.wantEvent)
	if len(retried) != 2 {
		t.Fatalf("%s records after retry = %d, want 2", run.testCase.wantEvent, len(retried))
	}
}

// This test protects shutdown semantics and fails if cancellation does not
// prevent later tasks, does not interrupt an active pass, or logs normal
// shutdown cancellation as a pruning failure.
func TestHistoryPruneSchedulerCancellationPreventsTaskAndLogsNoFailure(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		logger, recorder := withRecording(slog.LevelDebug)
		permits := make(chan struct{})
		devicePruner := &stubHistoryPruner{name: devicesHistoryPruneModule, permits: permits}
		automationPruner := &stubHistoryPruner{name: automationsHistoryPruneModule}
		scheduler := newHistoryPruneScheduler(
			logger, newHistoryPruneTasks(devicePruner, automationPruner),
		)
		ctx, cancel := context.WithCancel(context.Background())
		worker := lifecycle.StartWorker(ctx, scheduler.run)
		synctest.Wait()
		if sweeps := devicePruner.recorded(); len(sweeps) != 1 {
			t.Fatalf("device sweeps = %d, want one in-flight pass", len(sweeps))
		}

		cancel()
		synctest.Wait()
		if sweeps := automationPruner.recorded(); len(sweeps) != 0 {
			t.Fatalf("cancellation did not prevent the automations task: sweeps = %d", len(sweeps))
		}
		if records := recorder.snapshot(); len(records) != 0 {
			t.Fatalf("shutdown cancellation logged pruning failures: %#v", records)
		}
		if stopErr := worker.Stop(context.Background()); stopErr != nil {
			t.Fatalf("stopping a canceled scheduler returned %v", stopErr)
		}
	})
}

// requireNoRawHistoryPruneError fails when a pruning failure record carries the
// upstream error text in its message or attributes.
func requireNoRawHistoryPruneError(t *testing.T, record slog.Record, secret string) {
	t.Helper()
	if strings.Contains(record.Message, secret) {
		t.Fatalf("failure record message leaks raw error text: %q", record.Message)
	}
	leaked := false
	record.Attrs(func(attr slog.Attr) bool {
		if strings.Contains(attr.Value.String(), secret) {
			leaked = true
			return false
		}
		return true
	})
	if leaked {
		t.Fatalf("failure record attrs leak raw error text: %#v", record)
	}
}

// This test protects the shutdown join order and fails if SQLite closes before
// the retention worker returns, which would let a pruning transaction race the
// closed database.
func TestCoreShutdownJoinsHistoryPruneWorkerBeforeDatabaseClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	workerReturned := make(chan bool, 1)
	worker := lifecycle.StartWorker(ctx, func(workerContext context.Context) error {
		<-workerContext.Done()
		// The database must still be open when the worker returns: shutdown joins
		// the worker before closing SQLite.
		workerReturned <- database.PingContext(context.Background()) == nil
		return nil
	})
	shutdown := &coreShutdown{
		runContext:         ctx,
		logger:             slog.New(slog.DiscardHandler),
		database:           database,
		historyPruneWorker: worker,
	}
	if runErr := shutdown.run(); runErr != nil {
		t.Fatalf("shutdown returned %v", runErr)
	}
	select {
	case open := <-workerReturned:
		if !open {
			t.Fatal("database closed before the retention worker was joined")
		}
	default:
		t.Fatal("shutdown returned without joining the retention worker")
	}
	if pingErr := database.PingContext(ctx); pingErr == nil {
		t.Fatal("shutdown did not close the database")
	}
}
