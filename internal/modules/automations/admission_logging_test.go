package automations_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
)

// Messages emitted by logRunStarted and logSkipped.
const (
	runStartedLogMessage = "automation run started"
	skippedLogMessage    = "automation run skipped"
)

// blockingMessageLogHandler blocks one message until release closes.
// Run completion logs pass through so tracked workers can finish.
type blockingMessageLogHandler struct {
	inner   slog.Handler
	blocked string
	entered chan struct{}
	release chan struct{}
}

func (handler *blockingMessageLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.inner.Enabled(ctx, level)
}

func (handler *blockingMessageLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == handler.blocked {
		select {
		case handler.entered <- struct{}{}:
		default:
		}
		<-handler.release
	}
	return handler.inner.Handle(ctx, record)
}

func (handler *blockingMessageLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &blockingMessageLogHandler{
		inner:   handler.inner.WithAttrs(attrs),
		blocked: handler.blocked,
		entered: handler.entered,
		release: handler.release,
	}
}

func (handler *blockingMessageLogHandler) WithGroup(name string) slog.Handler {
	return &blockingMessageLogHandler{
		inner:   handler.inner.WithGroup(name),
		blocked: handler.blocked,
		entered: handler.entered,
		release: handler.release,
	}
}

// newBlockedAdmissionLogger returns a selective logger, its sink, a blocked-message
// signal, and an unblock function. Cleanup also unblocks the logger.
func newBlockedAdmissionLogger(
	t *testing.T,
	blocked string,
) (*slog.Logger, *lockedAutomationLogWriter, <-chan struct{}, func()) {
	t.Helper()
	writer := &lockedAutomationLogWriter{}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	logger := slog.New(&blockingMessageLogHandler{
		inner:   slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}),
		blocked: blocked,
		entered: entered,
		release: release,
	})
	return logger, writer, entered, unblock
}

// requireLoggedEvents checks the exact count of an event in the sink.
func requireLoggedEvents(
	t *testing.T,
	writer *lockedAutomationLogWriter,
	event string,
	want int,
) {
	t.Helper()
	if got := automationLogEvents(writer.records(t), event); len(got) != want {
		t.Fatalf("%s events = %d, want %d:\n%s", event, len(got), want, writer.output())
	}
}

// requireAdmissionInFlight waits for admission to reach the blocking repository.
func requireAdmissionInFlight(t *testing.T, blocking *blockingAdmissionRepository) {
	t.Helper()
	select {
	case <-blocking.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the admission transaction never started")
	}
}

// requireBlockedDiagnostic ensures the log call was reached, not omitted.
func requireBlockedDiagnostic(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the blocked admission diagnostic was never attempted")
	}
}

// requireDrainCompletesWhileLogBlocked joins Runs without unblocking the sink.
func requireDrainCompletesWhileLogBlocked(t *testing.T, service *automations.Service) {
	t.Helper()
	waited := make(chan error, 1)
	go func() { waited <- service.Drain(context.Background()) }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("Drain = %v, want nil while the diagnostic sink was blocked", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not complete while the admission diagnostic was blocked")
	}
}

// requireDrainedRun checks that the sole Run is durably interrupted with core_stopping.
func requireDrainedRun(
	t *testing.T,
	service *automations.Service,
	automationID automations.AutomationID,
) {
	t.Helper()
	history := listHistory(t, service, automationID)
	if len(history) != 1 || history[0].Status != automations.RunInterrupted {
		t.Fatalf("automation %s history = %#v, want one interrupted Run", automationID, history)
	}
	entry := historyEntry(t, service, automationID, history[0].ID)
	if entry.Run == nil || entry.Run.FailureCode == nil ||
		*entry.Run.FailureCode != automations.FailureCoreStopping {
		t.Fatalf("automation %s Run = %#v, want core_stopping", automationID, entry.Run)
	}
}

// A blocked run_started log must not keep a completed manual Run tracked.
func TestDrainCompletesWhileManualRunStartedLogBlocked(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	logger, writer, entered, unblock := newBlockedAdmissionLogger(t, runStartedLogMessage)
	dependencies := runtimeTestDependencies()
	dependencies.Logger = logger
	blocking := newBlockingAdmissionRepository(
		automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies),
	)
	service := automations.NewService(blocking, scripted, dependencies)
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))

	admitted := make(chan error, 1)
	go func() {
		_, err := service.StartManualRun(context.Background(), automations.ManualRunInput{AutomationID: record.ID})
		admitted <- err
	}()
	requireAdmissionInFlight(t, blocking)

	// Close mid-transaction so the Run drains without executing Commands.
	service.StopAdmission()
	close(blocking.release)

	requireBlockedDiagnostic(t, entered)
	requireDrainCompletesWhileLogBlocked(t, service)
	// The join must cover Run completion, including its unblocked diagnostic.
	requireDrainedRun(t, service, record.ID)
	requireLoggedEvents(t, writer, "automation.run_completed", 1)

	unblock()
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("StartManualRun = %v, want a committed Run", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartManualRun did not return after the diagnostic sink was released")
	}
	requireLoggedEvents(t, writer, "automation.run_started", 1)
	if scripted.executionCount() != 0 {
		t.Fatalf("drain executed %d Commands, want 0", scripted.executionCount())
	}
}

// An admission that starts no Run must release its reservation before logging.
func TestDrainCompletesWhileSkippedLogBlocked(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	logger, writer, entered, unblock := newBlockedAdmissionLogger(t, skippedLogMessage)
	dependencies := runtimeTestDependencies()
	dependencies.Logger = logger
	service, _ := newRuntimeService(t, scripted, dependencies)
	entity := newEntityID(t)
	record := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))

	// A stale Fact records a Skip without starting a Run.
	stale := newObservationFact(t, entity, runtimeTestNow.Add(-automations.FactMaximumAge-time.Second))
	outcome := make(chan automations.AdmissionOutcome, 1)
	admitFailed := make(chan error, 1)
	go func() {
		result, err := service.ReceiveDeviceFact(context.Background(), stale)
		if err != nil {
			admitFailed <- err
			return
		}
		outcome <- result
	}()

	requireBlockedDiagnostic(t, entered)
	service.StopAdmission()
	requireDrainCompletesWhileLogBlocked(t, service)

	unblock()
	select {
	case err := <-admitFailed:
		t.Fatalf("ReceiveDeviceFact = %v, want a recorded stale_fact Skip", err)
	case result := <-outcome:
		if result.StartedRuns != 0 || result.RecordedSkips != 1 {
			t.Fatalf("admission outcome = %#v, want one skip and no Run", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReceiveDeviceFact did not return after the diagnostic sink was released")
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 || history[0].Kind != automations.HistorySkip ||
		history[0].Reason != automations.SkipStaleFact {
		t.Fatalf("automation %s history = %#v, want one stale_fact Skip", record.ID, history)
	}
	requireLoggedEvents(t, writer, "automation.skipped", 1)
	requireLoggedEvents(t, writer, "automation.run_started", 0)
	if scripted.executionCount() != 0 {
		t.Fatalf("skipped admission executed %d Commands, want 0", scripted.executionCount())
	}
}

// All committed Runs need workers before the first run_started log can block.
func TestDrainCompletesWhileFanOutRunStartedLogBlocked(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	logger, writer, entered, unblock := newBlockedAdmissionLogger(t, runStartedLogMessage)
	dependencies := runtimeTestDependencies()
	dependencies.Logger = logger
	blocking := newBlockingAdmissionRepository(
		automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies),
	)
	service := automations.NewService(blocking, scripted, dependencies)
	entity := newEntityID(t)
	first := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))
	second := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))

	admitted := make(chan error, 1)
	go func() {
		_, err := service.ReceiveDeviceFact(context.Background(), newObservationFact(t, entity, runtimeTestNow))
		admitted <- err
	}()
	requireAdmissionInFlight(t, blocking)

	service.StopAdmission()
	close(blocking.release)

	requireBlockedDiagnostic(t, entered)
	requireDrainCompletesWhileLogBlocked(t, service)
	// A stranded second Run would still be Running, not interrupted.
	requireDrainedRun(t, service, first.ID)
	requireDrainedRun(t, service, second.ID)
	// Completion logs must pass the blocked admission log.
	requireLoggedEvents(t, writer, "automation.run_completed", 2)

	unblock()
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("ReceiveDeviceFact = %v, want a committed fan-out admission", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReceiveDeviceFact did not return after the diagnostic sink was released")
	}
	requireLoggedEvents(t, writer, "automation.run_started", 2)
	if scripted.executionCount() != 0 {
		t.Fatalf("drain executed %d Commands, want 0", scripted.executionCount())
	}
}
