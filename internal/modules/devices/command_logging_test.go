package devices //nolint:testpackage // Tests exercise package-private command logging seams and fixtures.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// commandTestContextKey carries a caller value that must survive transport
// handling and asynchronous outcome logging, including after HTTP
// cancellation.
type commandTestContextKey struct{}

// lockedCommandLogWriter is a concurrency-safe slog destination. The command
// lifecycle logs from a background goroutine, so tests must never read an
// unlocked [bytes.Buffer] while it writes.
type lockedCommandLogWriter struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (writer *lockedCommandLogWriter) Write(payload []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.Write(payload)
}

func (writer *lockedCommandLogWriter) output() string {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return writer.buffer.String()
}

func (writer *lockedCommandLogWriter) records(t *testing.T) []map[string]any {
	t.Helper()
	raw := writer.output()
	var parsed []map[string]any
	for line := range strings.SplitSeq(raw, "\n") {
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

// contextObserver records whether each handled log record observed the
// originating operation context value.
type contextObserver struct {
	mutex    sync.Mutex
	observed []bool
}

func (observer *contextObserver) allObserved() bool {
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

// contextObservingCommandHandler forwards records to JSON while capturing
// context preservation evidence.
type contextObservingCommandHandler struct {
	inner    slog.Handler
	observer *contextObserver
}

func (handler *contextObservingCommandHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.inner.Enabled(ctx, level)
}

func (handler *contextObservingCommandHandler) Handle(ctx context.Context, record slog.Record) error {
	handler.observer.mutex.Lock()
	handler.observer.observed = append(handler.observer.observed, ctx.Value(commandTestContextKey{}) != nil)
	handler.observer.mutex.Unlock()
	return handler.inner.Handle(ctx, record)
}

func (handler *contextObservingCommandHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextObservingCommandHandler{inner: handler.inner.WithAttrs(attrs), observer: handler.observer}
}

func (handler *contextObservingCommandHandler) WithGroup(name string) slog.Handler {
	return &contextObservingCommandHandler{inner: handler.inner.WithGroup(name), observer: handler.observer}
}

func newCommandLogSink() (*lockedCommandLogWriter, *contextObserver, *slog.Logger) {
	writer := &lockedCommandLogWriter{}
	observer := &contextObserver{}
	logger := slog.New(&contextObservingCommandHandler{
		inner:    slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}),
		observer: observer,
	})
	return writer, observer, logger
}

func commandLogDependencies(logger *slog.Logger) Dependencies {
	dependencies := commandDependencies()
	dependencies.Logger = logger
	return dependencies
}

func commandOperationContext() context.Context {
	return context.WithValue(context.Background(), commandTestContextKey{}, "operation-123")
}

func commandEvents(records []map[string]any, event string) []map[string]any {
	var matched []map[string]any
	for _, record := range records {
		if record["event"] == event {
			matched = append(matched, record)
		}
	}
	return matched
}

func requireCommandField(t *testing.T, record map[string]any, field, want string) {
	t.Helper()
	got, ok := record[field].(string)
	if !ok || got != want {
		t.Fatalf("log field %q = %#v, want %q (record = %#v)", field, record[field], want, record)
	}
}

func waitForCommandEvent(t *testing.T, writer *lockedCommandLogWriter, event string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, record := range writer.records(t) {
			if record["event"] == event {
				return record
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log event %q:\n%s", event, writer.output())
	return nil
}

// This test protects the happy-path terminal summary and fails if creation,
// dispatch, or the satisfied outcome is missing, duplicated, or carries the
// wrong level, status, IDs, or sensitive parameters.
func TestExecuteCommandLogsSingleSatisfiedOutcome(t *testing.T) {
	t.Parallel()
	writer, observer, logger := newCommandLogSink()
	repository := newCommandRepository()
	var service *Service
	sender := commandSenderFunc(
		func(ctx context.Context, adapterID string, runtimeID RuntimeID, request CommandRequest) (CommandAcceptance, error) {
			observation := Observation{
				ID: commandTestObservationID, EntityID: request.EntityID, Value: Value(`true`),
				AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &request.ID,
			}
			if _, err := service.ProjectObservation(
				ctx, adapterID, runtimeID, observation, time.Now().UTC(),
			); err != nil {
				return CommandAcceptance{}, err
			}
			return CommandAcceptance{Accepted: true}, nil
		},
	)
	service = newTestService(repository, sender, commandCatalog(t, time.Second), commandLogDependencies(logger))

	result, err := service.ExecuteCommand(
		commandOperationContext(),
		commandTestEntityID,
		OperationNameSet,
		CommandParameters(`{"value":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ObservationID != commandTestObservationID {
		t.Fatalf("result = %#v", result)
	}

	records := writer.records(t)
	if completed := commandEvents(records, "command.completed"); len(completed) != 1 {
		t.Fatalf("command.completed events = %d, want 1:\n%s", len(completed), writer.output())
	} else {
		summary := completed[0]
		if summary["level"] != "INFO" {
			t.Fatalf("completed level = %#v, want INFO (record = %#v)", summary["level"], summary)
		}
		requireCommandField(t, summary, "status", string(CommandStatusSatisfied))
		requireCommandField(t, summary, "command_id", string(commandTestID))
		requireCommandField(t, summary, "correlation_id", string(commandTestCorrelationID))
		requireCommandField(t, summary, "entity_id", string(commandTestEntityID))
		requireCommandField(t, summary, "adapter_id", "simulator")
		requireCommandField(t, summary, "runtime_id", string(commandTestRuntimeID))
		requireCommandField(t, summary, "operation", string(OperationNameSet))
		requireCommandField(t, summary, "observation_id", string(commandTestObservationID))
		if _, ok := summary["failure_code"]; ok {
			t.Fatalf("satisfied summary carries failure_code (record = %#v)", summary)
		}
		duration, ok := summary["duration_ms"].(float64)
		if !ok || duration < 0 {
			t.Fatalf("duration_ms = %#v, want nonnegative number (record = %#v)", summary["duration_ms"], summary)
		}
	}
	if created := commandEvents(records, "command.created"); len(created) != 1 {
		t.Fatalf("command.created events = %d, want 1:\n%s", len(created), writer.output())
	} else {
		requireCommandField(t, created[0], "command_id", string(commandTestID))
		requireCommandField(t, created[0], "correlation_id", string(commandTestCorrelationID))
	}
	if dispatched := commandEvents(records, "command.dispatched"); len(dispatched) != 1 {
		t.Fatalf("command.dispatched events = %d, want 1:\n%s", len(dispatched), writer.output())
	} else if dispatched[0]["level"] != "DEBUG" {
		t.Fatalf("dispatched level = %#v, want DEBUG", dispatched[0]["level"])
	}
	if failed := commandEvents(records, "command.execution_failed"); len(failed) != 0 {
		t.Fatalf("command.execution_failed events = %d, want 0:\n%s", len(failed), writer.output())
	}
	if !observer.allObserved() {
		t.Fatal("command log emission lost the originating operation context")
	}
	if output := writer.output(); strings.Contains(output, `{"value":true}`) ||
		strings.Contains(output, `"parameters"`) {
		t.Fatalf("command logs contain sensitive parameters:\n%s", output)
	}
}

// This test protects durable failure classification and fails if a terminal
// failure uses the wrong event, level, status, or failure code, or if an
// internal failure is additionally reported as a completion.
func TestExecuteCommandLogsFailureMatrix(t *testing.T) {
	t.Parallel()
	tests := []commandLogFailureCase{
		{
			"adapter unhealthy", CommandAcceptance{}, ErrAdapterUnhealthy, time.Second, false,
			"command.completed", "WARN", CommandStatusAdapterUnhealthy, CommandFailureAdapterUnhealthy,
		},
		{
			"entity unavailable", CommandAcceptance{}, ErrEntityUnavailable, time.Second, false,
			"command.completed", "WARN", CommandStatusEntityUnavailable, CommandFailureEntityUnavailable,
		},
		{
			"upstream rejected", CommandAcceptance{Accepted: false}, nil, time.Second, false,
			"command.completed", "WARN", CommandStatusRejected, CommandFailureUpstreamRejected,
		},
		{
			"internal send failure", CommandAcceptance{}, errors.New("boom s3cr3t-transport-token"), time.Second,
			false, "command.execution_failed", "ERROR", CommandStatusInternalFailure, CommandFailureInternalError,
		},
		{
			"outcome timeout", CommandAcceptance{Accepted: true}, nil, 15 * time.Millisecond, false,
			"command.completed", "WARN", CommandStatusOutcomeTimeout, CommandFailureOutcomeTimeout,
		},
		{
			"immediate disabled", CommandAcceptance{}, nil, time.Second, true,
			"command.completed", "WARN", CommandStatusEntityDisabled, CommandFailureEntityDisabled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runCommandLogFailureCase(t, test)
		})
	}
}

type commandLogFailureCase struct {
	name       string
	acceptance CommandAcceptance
	sendErr    error
	deadline   time.Duration
	disabled   bool
	event      string
	level      string
	status     CommandStatus
	code       CommandFailureCode
}

func runCommandLogFailureCase(t *testing.T, test commandLogFailureCase) {
	t.Helper()
	writer, _, logger := newCommandLogSink()
	repository := newCommandRepository()
	repository.view.Entity.Enabled = !test.disabled
	sender := commandSenderFunc(func(
		context.Context,
		string,
		RuntimeID,
		CommandRequest,
	) (CommandAcceptance, error) {
		return test.acceptance, test.sendErr
	})
	service := newTestService(
		repository, sender, commandCatalog(t, test.deadline), commandLogDependencies(logger),
	)
	if _, err := service.ExecuteCommand(
		commandOperationContext(),
		commandTestEntityID,
		OperationNameSet,
		CommandParameters(`{"value":true}`),
	); err == nil {
		t.Fatal("expected command execution error")
	}
	requireSingleTerminalSummary(t, writer, test)
	if created := commandEvents(writer.records(t), "command.created"); len(created) != 1 {
		t.Fatalf("command.created events = %d, want 1:\n%s", len(created), writer.output())
	}
	if output := writer.output(); strings.Contains(output, "s3cr3t-transport-token") {
		t.Fatalf("command logs leak transport error text:\n%s", output)
	}
	stored := repository.command(commandTestID)
	if stored.Status != test.status || stored.FailureCode == nil || *stored.FailureCode != test.code {
		t.Fatalf("stored command = %#v, want status %q", stored, test.status)
	}
}

func requireSingleTerminalSummary(t *testing.T, writer *lockedCommandLogWriter, test commandLogFailureCase) {
	t.Helper()
	records := writer.records(t)
	terminal := commandEvents(records, test.event)
	if len(terminal) != 1 {
		t.Fatalf("%s events = %d, want 1:\n%s", test.event, len(terminal), writer.output())
	}
	if terminal[0]["level"] != test.level {
		t.Fatalf("terminal level = %#v, want %q", terminal[0]["level"], test.level)
	}
	requireCommandField(t, terminal[0], "status", string(test.status))
	requireCommandField(t, terminal[0], "failure_code", string(test.code))
	requireCommandField(t, terminal[0], "command_id", string(commandTestID))
	requireCommandField(t, terminal[0], "correlation_id", string(commandTestCorrelationID))
	other := "command.execution_failed"
	if test.event == other {
		other = "command.completed"
	}
	if unexpected := commandEvents(records, other); len(unexpected) != 0 {
		t.Fatalf("%s events = %d, want 0:\n%s", other, len(unexpected), writer.output())
	}
}

// This test protects async terminal logging after HTTP cancellation and fails
// if the outcome is missing, duplicated, loses operation context, or diverges
// from persisted history.
func TestExecuteCommandLogsOnceAfterCallerCancellation(t *testing.T) {
	t.Parallel()
	writer, observer, logger := newCommandLogSink()
	repository := newCommandRepository()
	dispatched := make(chan CommandRequest, 1)
	release := make(chan struct{})
	sender := commandSenderFunc(func(
		_ context.Context,
		_ string,
		_ RuntimeID,
		request CommandRequest,
	) (CommandAcceptance, error) {
		dispatched <- request
		<-release
		return CommandAcceptance{Accepted: false}, nil
	})
	service := newTestService(repository, sender, commandCatalog(t, time.Second), commandLogDependencies(logger))
	ctx, cancel := context.WithCancel(commandOperationContext())
	returned := make(chan error, 1)
	go func() {
		_, err := service.ExecuteCommand(
			ctx,
			commandTestEntityID,
			OperationNameSet,
			CommandParameters(`{"value":true}`),
		)
		returned <- err
	}()
	request := <-dispatched
	cancel()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller error = %v", err)
	}
	close(release)
	summary := waitForCommandEvent(t, writer, "command.completed")
	requireCommandField(t, summary, "status", string(CommandStatusRejected))
	requireCommandField(t, summary, "failure_code", string(CommandFailureUpstreamRejected))
	requireCommandField(t, summary, "command_id", string(request.ID))
	if summary["level"] != "WARN" {
		t.Fatalf("completed level = %#v, want WARN", summary["level"])
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if repository.command(request.ID).Status == CommandStatusRejected {
			break
		}
		time.Sleep(time.Millisecond)
	}
	stored := repository.command(request.ID)
	if stored.Status != CommandStatusRejected {
		t.Fatalf("stored command = %#v, log summary = %#v", stored, summary)
	}
	// Allow a quiescence window so a duplicate terminal emission would be observed.
	time.Sleep(200 * time.Millisecond)
	if completed := commandEvents(writer.records(t), "command.completed"); len(completed) != 1 {
		t.Fatalf("command.completed events = %d, want exactly 1:\n%s", len(completed), writer.output())
	}
	if !observer.allObserved() {
		t.Fatal("async command log emission lost the originating operation context after cancellation")
	}
}

// This test protects honest failure reporting and fails if a failed
// persistence write is reported as a durable completion or leaks store error
// text.
func TestExecuteCommandFailedPersistenceLogsExecutionFailed(t *testing.T) {
	t.Parallel()
	writer, _, logger := newCommandLogSink()
	repository := newCommandRepository()
	repository.completeErr = errors.New("SQLite unavailable s3cr3t-persist")
	sender := commandSenderFunc(func(
		context.Context,
		string,
		RuntimeID,
		CommandRequest,
	) (CommandAcceptance, error) {
		return CommandAcceptance{}, ErrEntityUnavailable
	})
	service := newTestService(repository, sender, commandCatalog(t, time.Second), commandLogDependencies(logger))
	if _, err := service.ExecuteCommand(
		commandOperationContext(),
		commandTestEntityID,
		OperationNameSet,
		CommandParameters(`{"value":true}`),
	); err == nil {
		t.Fatal("expected command execution error")
	}
	records := writer.records(t)
	if completed := commandEvents(records, "command.completed"); len(completed) != 0 {
		t.Fatalf("command.completed events = %d, want 0 (persistence failed):\n%s", len(completed), writer.output())
	}
	failed := commandEvents(records, "command.execution_failed")
	if len(failed) != 1 {
		t.Fatalf("command.execution_failed events = %d, want 1:\n%s", len(failed), writer.output())
	}
	if failed[0]["level"] != "ERROR" {
		t.Fatalf("execution_failed level = %#v, want ERROR", failed[0]["level"])
	}
	requireCommandField(t, failed[0], "error_code", "outcome_unknown")
	if output := writer.output(); strings.Contains(output, "s3cr3t-persist") {
		t.Fatalf("command logs leak persistence error text:\n%s", output)
	}
	if stored := repository.command(commandTestID); stored.Status != CommandStatusRequested {
		t.Fatalf("stored command = %#v, want requested (no fabricated completion)", stored)
	}
}

// This test protects pre-commit silence and fails if validation or creation
// failures emit creation events or fabricate IDs.
func TestExecuteCommandLogsNothingBeforeDurableCreation(t *testing.T) {
	t.Parallel()
	writer, _, logger := newCommandLogSink()
	repository := newCommandRepository()
	sender := commandSenderFunc(func(context.Context, string, RuntimeID, CommandRequest) (CommandAcceptance, error) {
		return CommandAcceptance{Accepted: true}, nil
	})
	service := newTestService(repository, sender, commandCatalog(t, time.Second), commandLogDependencies(logger))

	if _, err := service.ExecuteCommand(
		commandOperationContext(),
		commandTestEntityID,
		OperationNameSet,
		CommandParameters(`{"value":1}`),
	); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("invalid parameters error = %v", err)
	}
	repository.createErr = errors.New("SQLite unavailable")
	if _, err := service.ExecuteCommand(
		commandOperationContext(),
		commandTestEntityID,
		OperationNameSet,
		CommandParameters(`{"value":true}`),
	); !errors.Is(err, repository.createErr) {
		t.Fatalf("creation error = %v", err)
	}
	if records := writer.records(t); len(records) != 0 {
		t.Fatalf("log records = %d, want 0 before durable creation:\n%s", len(records), writer.output())
	}
}

// This test protects terminal race handling and fails if a satisfaction that
// beats a dispatch failure is reported as that failure instead of satisfied.
func TestExecuteCommandSatisfactionRaceLogsSingleSatisfiedOutcome(t *testing.T) {
	t.Parallel()
	writer, _, logger := newCommandLogSink()
	repository := newCommandRepository()
	var service *Service
	sender := commandSenderFunc(
		func(
			ctx context.Context,
			adapterID string,
			runtimeID RuntimeID,
			request CommandRequest,
		) (CommandAcceptance, error) {
			_, err := service.ProjectObservation(ctx, adapterID, runtimeID, Observation{
				ID: commandTestObservationID, EntityID: request.EntityID, Value: Value(`true`),
				AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &request.ID,
			}, time.Now().UTC())
			if err != nil {
				return CommandAcceptance{}, err
			}
			return CommandAcceptance{Accepted: false}, nil
		},
	)
	service = newTestService(repository, sender, commandCatalog(t, time.Second), commandLogDependencies(logger))

	result, err := service.ExecuteCommand(
		commandOperationContext(),
		commandTestEntityID,
		OperationNameSet,
		CommandParameters(`{"value":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ObservationID != commandTestObservationID {
		t.Fatalf("result = %#v", result)
	}
	records := writer.records(t)
	completed := commandEvents(records, "command.completed")
	if len(completed) != 1 {
		t.Fatalf("command.completed events = %d, want 1:\n%s", len(completed), writer.output())
	}
	requireCommandField(t, completed[0], "status", string(CommandStatusSatisfied))
	requireCommandField(t, completed[0], "observation_id", string(commandTestObservationID))
	if failed := commandEvents(records, "command.execution_failed"); len(failed) != 0 {
		t.Fatalf("command.execution_failed events = %d, want 0:\n%s", len(failed), writer.output())
	}
}
