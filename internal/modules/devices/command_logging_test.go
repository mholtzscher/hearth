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
// handling and asynchronous failure diagnostics, including after HTTP
// cancellation.
type commandTestContextKey struct{}

// lockedCommandLogWriter is a concurrency-safe slog destination. The command
// failure diagnostic logs from a background goroutine, so tests must never
// read an unlocked [bytes.Buffer] while it writes.
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

// This test protects startup logging and fails if creation or dispatch is
// missing, duplicated, or carries the wrong level, IDs, or sensitive
// parameters. Lifecycle outcomes belong to the command API, never to logs,
// so any terminal summary here is a failure.
func TestExecuteCommandLogsCreationAndDispatch(t *testing.T) {
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
	requireObservedResult(t, result)

	// Creation and dispatch logging run on the lifecycle goroutine, so wait
	// for the async emissions instead of assuming they finished before
	// ExecuteCommand returned.
	waitForCommandEvent(t, writer, "command.dispatched")
	waitForCommandEvent(t, writer, "command.created")
	records := writer.records(t)
	if created := commandEvents(records, "command.created"); len(created) != 1 {
		t.Fatalf("command.created events = %d, want 1:\n%s", len(created), writer.output())
	} else {
		if created[0]["level"] != "INFO" {
			t.Fatalf("created level = %#v, want INFO (record = %#v)", created[0]["level"], created[0])
		}
		requireCommandField(t, created[0], "command_id", string(commandTestID))
		requireCommandField(t, created[0], "correlation_id", string(commandTestCorrelationID))
		requireCommandField(t, created[0], "entity_id", string(commandTestEntityID))
		requireCommandField(t, created[0], "adapter_id", "simulator")
		requireCommandField(t, created[0], "operation", string(OperationNameSet))
	}
	if dispatched := commandEvents(records, "command.dispatched"); len(dispatched) != 1 {
		t.Fatalf("command.dispatched events = %d, want 1:\n%s", len(dispatched), writer.output())
	} else {
		if dispatched[0]["level"] != "DEBUG" {
			t.Fatalf("dispatched level = %#v, want DEBUG (record = %#v)", dispatched[0]["level"], dispatched[0])
		}
		requireCommandField(t, dispatched[0], "command_id", string(commandTestID))
		requireCommandField(t, dispatched[0], "correlation_id", string(commandTestCorrelationID))
		requireCommandField(t, dispatched[0], "entity_id", string(commandTestEntityID))
		requireCommandField(t, dispatched[0], "adapter_id", "simulator")
		requireCommandField(t, dispatched[0], "runtime_id", string(commandTestRuntimeID))
		requireCommandField(t, dispatched[0], "operation", string(OperationNameSet))
	}
	if completed := commandEvents(records, "command.completed"); len(completed) != 0 {
		t.Fatalf(
			"command.completed events = %d, want 0 (lifecycle belongs to the API):\n%s",
			len(completed),
			writer.output(),
		)
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

// This test protects honest failure reporting and fails if a failed
// persistence write is reported as a durable completion, invents a terminal
// status, or leaks store error text.
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
	// Failure diagnostics log from the lifecycle goroutine; wait for the
	// async emission.
	waitForCommandEvent(t, writer, "command.execution_failed")
	records := writer.records(t)
	if completed := commandEvents(records, "command.completed"); len(completed) != 0 {
		t.Fatalf("command.completed events = %d, want 0 (no durable outcome):\n%s", len(completed), writer.output())
	}
	failed := commandEvents(records, "command.execution_failed")
	if len(failed) != 1 {
		t.Fatalf("command.execution_failed events = %d, want 1:\n%s", len(failed), writer.output())
	}
	if failed[0]["level"] != "ERROR" {
		t.Fatalf("execution_failed level = %#v, want ERROR", failed[0]["level"])
	}
	requireCommandField(t, failed[0], "error_code", "internal_error")
	requireCommandField(t, failed[0], "command_id", string(commandTestID))
	requireCommandField(t, failed[0], "correlation_id", string(commandTestCorrelationID))
	if _, ok := failed[0]["status"]; ok {
		t.Fatalf("execution_failed invents a status (record = %#v)", failed[0])
	}
	if output := writer.output(); strings.Contains(output, "s3cr3t-persist") {
		t.Fatalf("command logs leak persistence error text:\n%s", output)
	}
	if stored := repository.command(commandTestID); stored.Status != CommandStatusRequested {
		t.Fatalf("stored command = %#v, want requested (no fabricated completion)", stored)
	}
}

// This test protects swallowed async failure diagnostics and fails if a
// persistence failure after HTTP cancellation is lost, loses operation
// context, invents a terminal summary, or leaks store error text.
func TestExecuteCommandLogsSwallowedFailureAfterCallerCancellation(t *testing.T) {
	t.Parallel()
	writer, observer, logger := newCommandLogSink()
	repository := newCommandRepository()
	repository.completeErr = errors.New("SQLite unavailable s3cr3t-persist")
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
	// The caller is gone and the outcome is swallowed; the background
	// lifecycle must still leave an execution_failed diagnostic.
	failed := waitForCommandEvent(t, writer, "command.execution_failed")
	if failed["level"] != "ERROR" {
		t.Fatalf("execution_failed level = %#v, want ERROR", failed["level"])
	}
	requireCommandField(t, failed, "error_code", "internal_error")
	requireCommandField(t, failed, "command_id", string(request.ID))
	requireCommandField(
		t, failed, "correlation_id", string(repository.command(request.ID).CorrelationID),
	)
	if _, ok := failed["status"]; ok {
		t.Fatalf("execution_failed invents a status (record = %#v)", failed)
	}
	if completed := commandEvents(writer.records(t), "command.completed"); len(completed) != 0 {
		t.Fatalf("command.completed events = %d, want 0 (no durable outcome):\n%s", len(completed), writer.output())
	}
	if output := writer.output(); strings.Contains(output, "s3cr3t-persist") {
		t.Fatalf("command logs leak persistence error text:\n%s", output)
	}
	if stored := repository.command(request.ID); stored.Status != CommandStatusRequested {
		t.Fatalf("stored command = %#v, want requested (no fabricated completion)", stored)
	}
	if !observer.allObserved() {
		t.Fatal("async command log emission lost the originating operation context after cancellation")
	}
}

// This test protects swallowed persisted internal-failure diagnostics and
// fails if an unexpected dispatch error committed as internal_failure after
// HTTP cancellation is lost, loses operation context, invents a terminal
// summary, or leaks dispatch error text.
func TestExecuteCommandLogsPersistedInternalFailureAfterCallerCancellation(t *testing.T) {
	t.Parallel()
	writer, observer, logger := newCommandLogSink()
	repository := newCommandRepository()
	dispatched := make(chan CommandRequest, 1)
	release := make(chan struct{})
	dispatchErr := errors.New("boom s3cr3t-dispatch")
	sender := commandSenderFunc(func(
		_ context.Context,
		_ string,
		_ RuntimeID,
		request CommandRequest,
	) (CommandAcceptance, error) {
		dispatched <- request
		<-release
		return CommandAcceptance{}, dispatchErr
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
	// The persisted internal failure has no waiting caller; the background
	// lifecycle must still leave an execution_failed diagnostic.
	failed := waitForCommandEvent(t, writer, "command.execution_failed")
	if failed["level"] != "ERROR" {
		t.Fatalf("execution_failed level = %#v, want ERROR", failed["level"])
	}
	requireCommandField(t, failed, "error_code", "internal_error")
	requireCommandField(t, failed, "command_id", string(request.ID))
	requireCommandField(
		t, failed, "correlation_id", string(repository.command(request.ID).CorrelationID),
	)
	if _, ok := failed["status"]; ok {
		t.Fatalf("execution_failed invents a status (record = %#v)", failed)
	}
	if completed := commandEvents(writer.records(t), "command.completed"); len(completed) != 0 {
		t.Fatalf(
			"command.completed events = %d, want 0 (outcome belongs to the API):\n%s",
			len(completed),
			writer.output(),
		)
	}
	if output := writer.output(); strings.Contains(output, "s3cr3t-dispatch") {
		t.Fatalf("command logs leak dispatch error text:\n%s", output)
	}
	stored := repository.command(request.ID)
	if stored.Status != CommandStatusInternalFailure || stored.FailureCode == nil ||
		*stored.FailureCode != CommandFailureInternalError || stored.CompletedAt == nil {
		t.Fatalf("stored command = %#v, want persisted internal_failure", stored)
	}
	if !observer.allObserved() {
		t.Fatal("async command log emission lost the originating operation context after cancellation")
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

// blockingCommandCreatedHandler blocks the synchronous log destination on
// the creation record until released. Any lifecycle or cancellation step
// gated behind creation emission deadlocks while it blocks.
type blockingCommandCreatedHandler struct {
	inner   slog.Handler
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (handler *blockingCommandCreatedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.inner.Enabled(ctx, level)
}

func (handler *blockingCommandCreatedHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "command created" {
		handler.once.Do(func() { close(handler.entered) })
		<-handler.release
	}
	return handler.inner.Handle(ctx, record)
}

func (handler *blockingCommandCreatedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &blockingCommandCreatedHandler{
		inner: handler.inner.WithAttrs(attrs), release: handler.release, entered: handler.entered,
	}
}

func (handler *blockingCommandCreatedHandler) WithGroup(name string) slog.Handler {
	return &blockingCommandCreatedHandler{
		inner: handler.inner.WithGroup(name), release: handler.release, entered: handler.entered,
	}
}

func newBlockingCommandLogSink() (*lockedCommandLogWriter, *slog.Logger, chan struct{}, chan struct{}) {
	writer := &lockedCommandLogWriter{}
	entered := make(chan struct{})
	release := make(chan struct{})
	logger := slog.New(&blockingCommandCreatedHandler{
		inner:   slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug}),
		release: release,
		entered: entered,
	})
	return writer, logger, entered, release
}

// This test protects lifecycle progress under a blocked log destination and
// fails if a stuck command.created writer delays waiter startup, dispatch,
// or the waiting caller. The creation record must follow the buffered run
// outcome instead of gating it.
func TestExecuteCommandLifecycleProgressesUnderBlockedCreationLog(t *testing.T) {
	t.Parallel()
	writer, logger, entered, release := newBlockingCommandLogSink()
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

	type outcome struct {
		result CommandResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := service.ExecuteCommand(
			commandOperationContext(),
			commandTestEntityID,
			OperationNameSet,
			CommandParameters(`{"value":true}`),
		)
		finished <- outcome{result: result, err: err}
	}()
	select {
	case completed := <-finished:
		if completed.err != nil {
			close(release)
			t.Fatalf("ExecuteCommand error = %v", completed.err)
		}
		requireObservedResult(t, completed.result)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("ExecuteCommand did not return while command.created writer was blocked")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("blocked command.created emission was never attempted")
	}
	close(release)
	created := waitForCommandEvent(t, writer, "command.created")
	requireCommandField(t, created, "command_id", string(commandTestID))
}

// This test protects caller cancellation under a blocked log destination
// and fails if a stuck command.created writer delays waiter startup or the
// cancellation select. The caller must observe cancellation even though the
// creation record is still pending.
func TestExecuteCommandCallerCancellationProgressesUnderBlockedCreationLog(t *testing.T) {
	t.Parallel()
	writer, logger, _, release := newBlockingCommandLogSink()
	repository := newCommandRepository()
	dispatched := make(chan CommandRequest, 1)
	dispatchRelease := make(chan struct{})
	sender := commandSenderFunc(func(
		_ context.Context,
		_ string,
		_ RuntimeID,
		request CommandRequest,
	) (CommandAcceptance, error) {
		dispatched <- request
		<-dispatchRelease
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
	select {
	case request := <-dispatched:
		if request.EntityID != commandTestEntityID {
			cancel()
			close(dispatchRelease)
			close(release)
			t.Fatalf("dispatched request = %#v", request)
		}
	case <-time.After(3 * time.Second):
		cancel()
		close(dispatchRelease)
		close(release)
		t.Fatal("dispatch never started while command.created writer was blocked")
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			close(dispatchRelease)
			close(release)
			t.Fatalf("caller error = %v", err)
		}
	case <-time.After(3 * time.Second):
		close(dispatchRelease)
		close(release)
		t.Fatal("caller cancellation did not return while command.created writer was blocked")
	}
	close(dispatchRelease)
	close(release)
	created := waitForCommandEvent(t, writer, "command.created")
	if created["level"] != "INFO" {
		t.Fatalf("created level = %#v, want INFO", created["level"])
	}
}
