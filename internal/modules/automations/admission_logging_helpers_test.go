package automations_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
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
	if len(history) != 1 || history[0].Body.(automations.RunHistorySummary).Status != automations.RunInterrupted {
		t.Fatalf("automation %s history = %#v, want one interrupted Run", automationID, history)
	}
	entry := historyEntry(t, service, automationID, history[0].ID)
	if runEntry(entry) == nil || runFailure(runEntry(entry).State) == nil ||
		*runFailure(runEntry(entry).State) != automations.FailureCoreStopping {
		t.Fatalf("automation %s Run = %#v, want core_stopping", automationID, runEntry(entry))
	}
}
