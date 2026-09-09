package automations //nolint:testpackage // Scheduler lifecycle tests inject clocks, wakeups, and storage faults.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// schedulerTestClock is a mutex-guarded fake time source shared by the
// repository clocks and the scheduler loop clock, mirroring how application
// assembly aligns WithAutomationSchedulerClock with WithSchedulerClock.
type schedulerTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *schedulerTestClock) get() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *schedulerTestClock) set(now time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = now
}

// schedulerStubCommands executes scheduled Steps to satisfied completion
// through the same worker path as manual Runs. An optional release barrier
// holds a worker inside its first execution so overlap tests can keep a Run
// active without device timing.
type schedulerStubCommands struct {
	mu        sync.Mutex
	executed  []devices.CommandInput
	completed map[devices.CommandID]devices.CommandRecord
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
}

func newSchedulerStubCommands() *schedulerStubCommands {
	return &schedulerStubCommands{
		completed: make(map[devices.CommandID]devices.CommandRecord),
		entered:   make(chan struct{}),
	}
}

func (stub *schedulerStubCommands) ValidateCommand(
	_ context.Context,
	_ devices.CommandInput,
) (devices.CommandParameters, error) {
	return devices.CommandParameters(`{"value":true}`), nil
}

func (stub *schedulerStubCommands) ExecuteAutomationStepCommand(
	_ context.Context,
	input devices.CommandInput,
) (devices.CommandResult, error) {
	stub.enterOnce.Do(func() { close(stub.entered) })
	if stub.release != nil {
		<-stub.release
	}
	completedAt := time.Now().UTC()
	record := devices.CommandRecord{
		ID:            input.ID,
		CorrelationID: input.CorrelationID,
		Status:        devices.CommandStatusSatisfied,
		CompletedAt:   &completedAt,
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.executed = append(stub.executed, input)
	stub.completed[input.ID] = record
	return devices.CommandResult{}, nil
}

func (stub *schedulerStubCommands) GetCommand(
	_ context.Context,
	id devices.CommandID,
) (devices.CommandRecord, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	record, ok := stub.completed[id]
	if !ok {
		return devices.CommandRecord{}, devices.ErrCommandNotFound
	}
	return record, nil
}

func (stub *schedulerStubCommands) executions() int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return len(stub.executed)
}

// schedulerLogEvent is one captured slog record with stringified fields.
type schedulerLogEvent struct {
	message string
	fields  map[string]string
}

// schedulerLogCapture is a goroutine-safe slog handler for asserting literal
// diagnostic event names without touching process logging.
type schedulerLogCapture struct {
	mu     sync.Mutex
	events []schedulerLogEvent
}

func (capture *schedulerLogCapture) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (capture *schedulerLogCapture) Handle(_ context.Context, record slog.Record) error {
	fields := make(map[string]string, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		fields[attr.Key] = attr.Value.String()
		return true
	})
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.events = append(capture.events, schedulerLogEvent{message: record.Message, fields: fields})
	return nil
}

func (capture *schedulerLogCapture) WithAttrs(_ []slog.Attr) slog.Handler { return capture }
func (capture *schedulerLogCapture) WithGroup(_ string) slog.Handler      { return capture }

func (capture *schedulerLogCapture) hasEvent(event string) bool {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	for _, logged := range capture.events {
		if logged.fields["event"] == event {
			return true
		}
	}
	return false
}

func (capture *schedulerLogCapture) firstEventField(event, key string) (string, bool) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	for _, logged := range capture.events {
		if logged.fields["event"] == event {
			value, ok := logged.fields[key]
			return value, ok
		}
	}
	return "", false
}

// schedulerTestWakeupBuffer holds ticks the loop has not processed yet. Tests
// signal one tick at a time, so the buffer only absorbs scheduling lag.
const schedulerTestWakeupBuffer = 16

// schedulerLifecycleHarness wires one fake clock into every scheduler time
// source and drives the loop through a buffered wakeup channel.
type schedulerLifecycleHarness struct {
	repo     *SQLiteRepository
	database *sql.DB
	service  *Service
	stub     *schedulerStubCommands
	clock    *schedulerTestClock
	wakeup   chan struct{}
	logs     *schedulerLogCapture
}

func newSchedulerLifecycleHarness(t *testing.T, timezone *time.Location) *schedulerLifecycleHarness {
	t.Helper()
	repo, database := testAutomationRepository(t)
	clock := &schedulerTestClock{now: scheduleMinute(18, 59).Add(30 * time.Second)}
	repo.now = clock.get
	repo.scheduleNow = clock.get
	wakeup := make(chan struct{}, schedulerTestWakeupBuffer)
	logs := &schedulerLogCapture{}
	stub := newSchedulerStubCommands()
	codec, err := NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(repo, stub, stub, codec, timezone, slog.New(logs),
		WithSchedulerClock(clock.get), WithSchedulerWakeup(wakeup))
	harness := &schedulerLifecycleHarness{
		repo:     repo,
		database: database,
		service:  service,
		stub:     stub,
		clock:    clock,
		wakeup:   wakeup,
		logs:     logs,
	}
	t.Cleanup(func() {
		service.StopAutomationScheduler()
		service.StopAutomationExecutionAdmission()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if waitErr := service.WaitAutomationRuns(ctx); waitErr != nil {
			t.Error(waitErr)
		}
	})
	return harness
}

func createSchedulerAutomation(
	t *testing.T,
	harness *schedulerLifecycleHarness,
	triggers ...AutomationTrigger,
) AutomationRecord {
	t.Helper()
	definition := testAutomationDefinition()
	definition.Enabled = true
	definition.Steps = definition.Steps[:1]
	definition.Triggers = triggers
	record, err := harness.service.CreateAutomation(t.Context(), definition)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// signalSchedulerTick advances the shared fake clock and wakes the loop once.
func signalSchedulerTick(harness *schedulerLifecycleHarness, at time.Time) {
	harness.clock.set(at)
	harness.wakeup <- struct{}{}
}

func waitSchedulerCondition(t *testing.T, message string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		select {
		case <-time.After(time.Millisecond):
		case <-t.Context().Done():
			t.Fatalf("test context done waiting for scheduler condition: %s", message)
		}
	}
	t.Fatalf("scheduler condition not met: %s", message)
}

func schedulerOccurrenceCount(t *testing.T, harness *schedulerLifecycleHarness) int {
	t.Helper()
	page, err := harness.service.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Items)
}

func waitSchedulerOccurrences(t *testing.T, harness *schedulerLifecycleHarness, count int) {
	t.Helper()
	waitSchedulerCondition(t, fmt.Sprintf("%d occurrences", count), func() bool {
		page, err := harness.service.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
		return err == nil && len(page.Items) == count
	})
}

func waitSchedulerRunTerminal(
	t *testing.T,
	harness *schedulerLifecycleHarness,
	id AutomationRunID,
) AutomationRunRecord {
	t.Helper()
	var run AutomationRunRecord
	waitSchedulerCondition(t, "scheduled run terminal", func() bool {
		current, err := harness.service.GetAutomationRun(t.Context(), id)
		if err != nil {
			return false
		}
		run = current
		return run.Status != AutomationRunStatusRunning
	})
	return run
}

func assertSchedulerOccurrenceIDs(
	t *testing.T,
	occurrence AutomationOccurrence,
	ids ...AutomationTriggerID,
) {
	t.Helper()
	if len(occurrence.MatchedTriggers) != len(ids) {
		t.Fatalf("matched triggers = %+v, want %v", occurrence.MatchedTriggers, ids)
	}
	for index, id := range ids {
		if occurrence.MatchedTriggers[index].ID != id {
			t.Fatalf("matched triggers = %+v, want %v", occurrence.MatchedTriggers, ids)
		}
	}
}

// This test protects scheduled worker registration and fails if a loop tick
// does not commit one coalesced Run for same-minute matches or the worker
// does not execute it through the shared executor.
func TestAutomationSchedulerAdmitsAndExecutesCoalescedMinute(t *testing.T) {
	t.Parallel()
	harness := newSchedulerLifecycleHarness(t, time.UTC)
	automation := createSchedulerAutomation(t, harness,
		scheduleTrigger("evening", "0 19 * * *"),
		scheduleTrigger("hourly", "0 * * * *"),
		scheduleTrigger("later", "0 20 * * *"))
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !harness.service.AutomationSchedulerHealthy() || !harness.service.AutomationSchedulerReady() {
		t.Fatal("scheduler not healthy and ready after start")
	}
	if !harness.logs.hasEvent("automation.scheduler_started") {
		t.Fatal("missing automation.scheduler_started event")
	}
	// A synchronous evaluation inside the startup minute is a deterministic no-op.
	harness.clock.set(scheduleMinute(18, 59).Add(45 * time.Second))
	harness.service.evaluateAutomationSchedule()
	if count := schedulerOccurrenceCount(t, harness); count != 0 {
		t.Fatalf("startup-minute evaluation admitted: %d occurrences", count)
	}
	// The loop tick at the matching minute commits exactly one coalesced Run.
	signalSchedulerTick(harness, scheduleMinute(19, 0).Add(20*time.Second))
	waitSchedulerOccurrences(t, harness, 1)
	page, err := harness.service.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if err != nil {
		t.Fatal(err)
	}
	occurrence := page.Items[0]
	if occurrence.Status != AutomationOccurrenceStarted || occurrence.RunID == nil ||
		occurrence.Revision != 1 || occurrence.Timezone != "UTC" ||
		!occurrence.ScheduledAt.Equal(scheduleMinute(19, 0)) ||
		occurrence.AutomationID != automation.ID {
		t.Fatalf("scheduled occurrence = %+v", occurrence)
	}
	assertSchedulerOccurrenceIDs(t, occurrence, "evening", "hourly")
	run := waitSchedulerRunTerminal(t, harness, *occurrence.RunID)
	if run.Status != AutomationRunStatusSucceeded || run.Source != AutomationRunSourceScheduled ||
		run.ScheduledAt == nil || !run.ScheduledAt.Equal(scheduleMinute(19, 0)) ||
		len(run.MatchedTriggerIDs) != 2 || run.MatchedTriggerIDs[0] != "evening" ||
		run.MatchedTriggerIDs[1] != "hourly" || harness.stub.executions() != 1 {
		t.Fatalf("scheduled run = %+v, executions = %d", run, harness.stub.executions())
	}
	// A repeated evaluation for the same minute stays a no-op.
	harness.clock.set(scheduleMinute(19, 0).Add(50 * time.Second))
	harness.service.evaluateAutomationSchedule()
	if count := schedulerOccurrenceCount(t, harness); count != 1 {
		t.Fatalf("repeat tick duplicated: %d occurrences", count)
	}
}

// This test protects restart recovery and fails if a restart executes missed
// minutes, replays the in-progress restart minute, or skips the restart gap.
func TestAutomationSchedulerRestartSkipsMissedMinutes(t *testing.T) {
	t.Parallel()
	harness := newSchedulerLifecycleHarness(t, time.UTC)
	createSchedulerAutomation(t, harness, scheduleTrigger("every", "* * * * *"))
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	harness.clock.set(scheduleMinute(19, 0).Add(20 * time.Second))
	harness.service.evaluateAutomationSchedule()
	waitSchedulerOccurrences(t, harness, 1)
	page, err := harness.service.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if err != nil {
		t.Fatal(err)
	}
	first := waitSchedulerRunTerminal(t, harness, *page.Items[0].RunID)
	if first.Status != AutomationRunStatusSucceeded {
		t.Fatalf("first scheduled run = %+v", first)
	}
	harness.service.StopAutomationScheduler()
	// Restart at 19:10 records the unevaluated interval as a core restart gap.
	harness.clock.set(scheduleMinute(19, 10))
	if err = harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	gaps, err := harness.service.ListAutomationScheduleGaps(t.Context(), AutomationScheduleGapListParams{})
	if err != nil || len(gaps.Items) != 1 {
		t.Fatalf("restart gaps = %+v, %v", gaps, err)
	}
	gap := gaps.Items[0]
	if gap.Reason != AutomationScheduleGapCoreRestart ||
		!gap.FromExclusive.Equal(scheduleMinute(19, 0)) ||
		!gap.ThroughInclusive.Equal(scheduleMinute(19, 10)) {
		t.Fatalf("restart gap = %+v", gap)
	}
	if reason, ok := harness.logs.firstEventField("automation.schedule_gap", "reason"); !ok ||
		reason != AutomationScheduleGapCoreRestart {
		t.Fatalf("missing core restart schedule_gap diagnostic: %+v", harness.logs.events)
	}
	if id, ok := harness.logs.firstEventField("automation.schedule_gap", "gap_id"); !ok || id != gap.ID {
		t.Fatalf("restart schedule_gap lost its gap identity: %+v", harness.logs.events)
	}
	// Restart during a matching minute does not execute that minute.
	harness.clock.set(scheduleMinute(19, 10).Add(5 * time.Second))
	harness.service.evaluateAutomationSchedule()
	if count := schedulerOccurrenceCount(t, harness); count != 1 {
		t.Fatalf("restart minute executed: %d occurrences", count)
	}
	// The next future minute admits normally with no additional gap.
	harness.clock.set(scheduleMinute(19, 11).Add(5 * time.Second))
	harness.service.evaluateAutomationSchedule()
	waitSchedulerOccurrences(t, harness, 2)
	page, err = harness.service.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Items[0].ScheduledAt.Equal(scheduleMinute(19, 11)) ||
		page.Items[0].Status != AutomationOccurrenceStarted {
		t.Fatalf("post-restart occurrence = %+v", page.Items[0])
	}
	second := waitSchedulerRunTerminal(t, harness, *page.Items[0].RunID)
	if second.Status != AutomationRunStatusSucceeded {
		t.Fatalf("post-restart run = %+v", second)
	}
	gaps, err = harness.service.ListAutomationScheduleGaps(t.Context(), AutomationScheduleGapListParams{})
	if err != nil || len(gaps.Items) != 1 {
		t.Fatalf("contiguous minute recorded a gap: %+v, %v", gaps, err)
	}
}

// This test protects independent scheduler health and fails if a persistence
// failure closes execution admission, if a duplicate-minute no-op clears the
// failure, or if anything but an actual evaluation restores health.
func TestAutomationSchedulerFailureNeedsEvaluatedSuccess(t *testing.T) {
	t.Parallel()
	harness := newSchedulerLifecycleHarness(t, time.UTC)
	automation := createSchedulerAutomation(t, harness, scheduleTrigger("every", "* * * * *"))
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.database.ExecContext(
		t.Context(),
		`CREATE TRIGGER fail_scheduled_occurrence BEFORE INSERT ON automation_occurrences BEGIN SELECT RAISE(ABORT, 'injected failure'); END`,
	); err != nil {
		t.Fatal(err)
	}
	harness.clock.set(scheduleMinute(19, 0).Add(20 * time.Second))
	harness.service.evaluateAutomationSchedule()
	if harness.service.AutomationSchedulerHealthy() || harness.service.AutomationSchedulerReady() {
		t.Fatal("scheduler failure did not mark health and readiness false")
	}
	if !harness.logs.hasEvent("automation.scheduler_failed") {
		t.Fatal("missing automation.scheduler_failed event")
	}
	if zone, ok := harness.logs.firstEventField("automation.scheduler_failed", "timezone"); !ok || zone != "UTC" {
		t.Fatalf("evaluation failure lost its timezone: %+v", harness.logs.events)
	}
	// Scheduler failure leaves execution admission open: manual Runs still start.
	admission, err := harness.service.StartManualRun(
		t.Context(),
		AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "manual-during-outage"},
	)
	if err != nil {
		t.Fatalf("scheduler failure closed manual admission: %v", err)
	}
	manual := waitSchedulerRunTerminal(t, harness, admission.Run.ID)
	if manual.Status != AutomationRunStatusSucceeded {
		t.Fatalf("manual run during scheduler outage = %+v", manual)
	}
	// A backward-minute no-op cannot clear the unresolved failure.
	harness.clock.set(scheduleMinute(18, 59).Add(50 * time.Second))
	harness.service.evaluateAutomationSchedule()
	if harness.service.AutomationSchedulerHealthy() {
		t.Fatal("duplicate-minute no-op cleared an unresolved scheduler failure")
	}
	if _, err = harness.database.ExecContext(t.Context(), `DROP TRIGGER fail_scheduled_occurrence`); err != nil {
		t.Fatal(err)
	}
	// Only an actual minute evaluation restores health.
	harness.clock.set(scheduleMinute(19, 0).Add(40 * time.Second))
	harness.service.evaluateAutomationSchedule()
	waitSchedulerCondition(t, "scheduler healthy again", func() bool {
		return harness.service.AutomationSchedulerHealthy() && harness.service.AutomationSchedulerReady()
	})
	waitSchedulerOccurrences(t, harness, 1)
}

// This test protects shutdown ordering and fails if the minute transaction
// and worker registration do not serialize with admission closure, if Stop
// returns before an in-flight tick joins, or if ticks evaluate after the gate
// closes. Per B8 both post-commit outcomes are valid: a worker that begins its
// first Step before the gate closes drains to succeeded with one dispatch,
// while a worker blocked by closure interrupts with zero dispatch.
func TestAutomationSchedulerShutdownSerializesTickAndJoins(t *testing.T) {
	t.Parallel()
	harness := newSchedulerLifecycleHarness(t, time.UTC)
	createSchedulerAutomation(t, harness, scheduleTrigger("every", "* * * * *"))
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := harness.service.StartAutomationScheduler(t.Context()); !errors.Is(err, ErrAutomationUnavailable) {
		t.Fatalf("second start = %v", err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	originalNewRunID := harness.repo.newRunID
	harness.repo.newRunID = func() (AutomationRunID, error) {
		close(entered)
		<-release
		return originalNewRunID()
	}
	signalSchedulerTick(harness, scheduleMinute(19, 0).Add(20*time.Second))
	receiveAutomationBarrier(t, entered)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		harness.service.StopAutomationScheduler()
	}()
	closing, closed := make(chan struct{}), make(chan struct{})
	go func() {
		close(closing)
		harness.service.StopAutomationExecutionAdmission()
		close(closed)
	}()
	receiveAutomationBarrier(t, closing)
	select {
	case <-stopped:
		t.Fatal("scheduler stop joined before the in-flight tick completed")
	case <-closed:
		t.Fatal("admission closure passed the admitted tick gate")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	receiveAutomationBarrier(t, closed)
	receiveAutomationBarrier(t, stopped)
	// The committed Run registered exactly one worker before the gate closed.
	waitSchedulerOccurrences(t, harness, 1)
	page, err := harness.service.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if err != nil || len(page.Items) != 1 || page.Items[0].RunID == nil ||
		!page.Items[0].ScheduledAt.Equal(scheduleMinute(19, 0)) {
		t.Fatalf("committed occurrence = %+v, %v", page, err)
	}
	committed := waitSchedulerRunTerminal(t, harness, *page.Items[0].RunID)
	executions := harness.stub.executions()
	switch committed.Status {
	case AutomationRunStatusSucceeded:
		if committed.FailureCode != nil || executions != 1 || len(committed.Steps) != 1 ||
			committed.Steps[0].Status != AutomationStepStatusSatisfied {
			t.Fatalf("drained scheduled run = %+v, executions = %d", committed, executions)
		}
	case AutomationRunStatusInterrupted:
		if committed.FailureCode == nil || *committed.FailureCode != AutomationFailureCoreStopping ||
			executions != 0 || len(committed.Steps) != 1 ||
			committed.Steps[0].Status != AutomationStepStatusNotAttempted {
			t.Fatalf("shutdown-interrupted scheduled run = %+v, executions = %d", committed, executions)
		}
	case AutomationRunStatusFailed, AutomationRunStatusRunning:
		t.Fatalf("shutdown run neither drained nor interrupted: %+v, executions = %d", committed, executions)
	}
	if harness.service.AutomationExecutionReady() {
		t.Fatal("shutdown left execution admission open")
	}
	waitCtx, waitCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer waitCancel()
	if waitErr := harness.service.WaitAutomationRuns(waitCtx); waitErr != nil {
		t.Fatalf("shutdown did not drain registered workers: %v", waitErr)
	}
	// Ticks after gate closure evaluate nothing.
	harness.clock.set(scheduleMinute(19, 1).Add(5 * time.Second))
	harness.service.evaluateAutomationSchedule()
	if count := schedulerOccurrenceCount(t, harness); count != 1 {
		t.Fatalf("post-shutdown tick evaluated: %d occurrences", count)
	}
	harness.service.StopAutomationScheduler()
}

// This test protects sticky executor faults and fails if a scheduler success
// clears the fault, reopens admission, or reports readiness while the fault
// is latched.
func TestAutomationSchedulerSuccessNeverClearsExecutorFault(t *testing.T) {
	t.Parallel()
	harness := newSchedulerLifecycleHarness(t, time.UTC)
	automation := createSchedulerAutomation(t, harness, scheduleTrigger("every", "* * * * *"))
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	// A deterministic stand-in for an ambiguous executor fault; behavioral
	// fault paths already latch through execution tests.
	harness.service.latchAutomationFault()
	if harness.service.AutomationExecutionReady() || harness.service.AutomationSchedulerReady() {
		t.Fatal("latched executor fault did not close readiness")
	}
	if !harness.service.AutomationSchedulerHealthy() {
		t.Fatal("executor fault cleared independent scheduler health")
	}
	if _, err := harness.service.StartManualRun(
		t.Context(),
		AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "blocked-by-fault"},
	); !errors.Is(
		err,
		ErrAutomationUnavailable,
	) {
		t.Fatalf("latched fault still admits: %v", err)
	}
	// Ticks while the gate is closed evaluate nothing and change no health.
	harness.clock.set(scheduleMinute(19, 0).Add(20 * time.Second))
	harness.service.evaluateAutomationSchedule()
	if count := schedulerOccurrenceCount(t, harness); count != 0 {
		t.Fatalf("closed gate evaluated: %d occurrences", count)
	}
	// Restarting the scheduler with a latched fault restores scheduler health
	// alone: admission stays closed and readiness stays false.
	harness.service.StopAutomationScheduler()
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !harness.service.AutomationSchedulerHealthy() {
		t.Fatal("restart did not restore scheduler health")
	}
	if harness.service.AutomationSchedulerReady() || harness.service.AutomationExecutionReady() {
		t.Fatal("scheduler restart reopened a latched executor fault")
	}
	if _, err := harness.service.StartManualRun(
		t.Context(),
		AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "still-blocked"},
	); !errors.Is(
		err,
		ErrAutomationUnavailable,
	) {
		t.Fatalf("restart reopened closed admission: %v", err)
	}
}

// This test protects overlap diagnostics and fails if a blocked Automation
// queues scheduled work, records partial matches, or omits the skip event.
func TestAutomationSchedulerOverlapSkipDiagnostics(t *testing.T) {
	t.Parallel()
	harness := newSchedulerLifecycleHarness(t, time.UTC)
	harness.stub.release = make(chan struct{})
	automation := createSchedulerAutomation(t, harness,
		scheduleTrigger("first", "* * * * *"),
		scheduleTrigger("second", "* * * * *"))
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	admission, err := harness.service.StartManualRun(
		t.Context(),
		AutomationManualRequest{AutomationID: automation.ID, IdempotencyKey: "block"},
	)
	if err != nil {
		t.Fatal(err)
	}
	receiveAutomationBarrier(t, harness.stub.entered)
	harness.clock.set(scheduleMinute(19, 0).Add(20 * time.Second))
	harness.service.evaluateAutomationSchedule()
	page, err := harness.service.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("overlap occurrences = %+v, %v", page, err)
	}
	skipped := page.Items[0]
	if skipped.Status != AutomationOccurrenceSkipped || skipped.RunID != nil ||
		skipped.SkipReason == nil || *skipped.SkipReason != AutomationOccurrenceSkipActive {
		t.Fatalf("overlap occurrence = %+v", skipped)
	}
	assertSchedulerOccurrenceIDs(t, skipped, "first", "second")
	run, err := harness.service.GetAutomationRun(t.Context(), admission.Run.ID)
	if err != nil || run.Status != AutomationRunStatusRunning {
		t.Fatalf("blocked run lost its claim: %+v, %v", run, err)
	}
	matched, ok := harness.logs.firstEventField("automation.occurrence_skipped", "matched_trigger_ids")
	if !ok || !strings.Contains(matched, "first") || !strings.Contains(matched, "second") {
		t.Fatalf("missing skip diagnostic with all trigger IDs: %+v", harness.logs.events)
	}
	if reason, reasonOk := harness.logs.firstEventField("automation.occurrence_skipped", "skip_reason"); !reasonOk ||
		reason != AutomationOccurrenceSkipActive {
		t.Fatalf("missing skip reason diagnostic: %+v", harness.logs.events)
	}
	if zone, zoneOk := harness.logs.firstEventField("automation.occurrence_skipped", "timezone"); !zoneOk ||
		zone != "UTC" {
		t.Fatalf("overlap skip lost its timezone: %+v", harness.logs.events)
	}
	close(harness.stub.release)
	blocked := waitSchedulerRunTerminal(t, harness, admission.Run.ID)
	if blocked.Status != AutomationRunStatusSucceeded {
		t.Fatalf("released manual run = %+v", blocked)
	}
}

// This test protects the diagnostic seams for API and application workers and
// fails if accessors, list validation, or startup validation drift.
func TestAutomationSchedulerDiagnosticsSeams(t *testing.T) {
	t.Parallel()
	harness := newSchedulerLifecycleHarness(t, time.UTC)
	if harness.service.HouseholdTimezone() != time.UTC {
		t.Fatal("household timezone accessor drifted")
	}
	occurrences, err := harness.service.ListAutomationOccurrences(t.Context(), AutomationOccurrenceListParams{})
	if err != nil || occurrences.Items == nil || len(occurrences.Items) != 0 || occurrences.HasMore {
		t.Fatalf("empty occurrence page = %+v, %v", occurrences, err)
	}
	gaps, err := harness.service.ListAutomationScheduleGaps(t.Context(), AutomationScheduleGapListParams{})
	if err != nil || gaps.Items == nil || len(gaps.Items) != 0 || gaps.HasMore {
		t.Fatalf("empty gap page = %+v, %v", gaps, err)
	}
	head := scheduleMinute(19, 0)
	badID := AutomationID("aut_not-an-id")
	for _, params := range []AutomationOccurrenceListParams{
		{AutomationID: &badID},
		{BeforeAutomationID: &badID},
		{BeforeScheduledAt: &head},
		{BeforeScheduledAt: &head, BeforeAutomationID: &badID},
		{Limit: 201},
	} {
		if _, err = harness.service.ListAutomationOccurrences(t.Context(), params); !errors.Is(
			err,
			ErrInvalidAutomation,
		) {
			t.Fatalf("occurrence misuse accepted: %+v", params)
		}
	}
	badGapID := "asg_not-a-uuid"
	for _, params := range []AutomationScheduleGapListParams{
		{BeforeID: &badGapID},
		{BeforeRecordedAt: &head},
		{Limit: 201},
	} {
		if _, err = harness.service.ListAutomationScheduleGaps(t.Context(), params); !errors.Is(
			err,
			ErrInvalidAutomation,
		) {
			t.Fatalf("gap misuse accepted: %+v", params)
		}
	}
	// Missing household timezone fails startup before readiness.
	codec, err := NewAutomationDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	zoneless := NewService(
		harness.repo,
		harness.stub,
		harness.stub,
		codec,
		nil,
		slog.New(harness.logs),
		WithSchedulerClock(harness.clock.get),
	)
	if err = zoneless.StartAutomationScheduler(t.Context()); !errors.Is(err, ErrInvalidAutomation) {
		t.Fatalf("zoneless start = %v", err)
	}
	if zoneless.AutomationSchedulerHealthy() || zoneless.AutomationSchedulerReady() {
		t.Fatal("failed startup reported scheduler health")
	}
	if !harness.logs.hasEvent("automation.scheduler_failed") {
		t.Fatal("missing automation.scheduler_failed event for zoneless startup")
	}
	if zone, ok := harness.logs.firstEventField("automation.scheduler_failed", "timezone"); ok || zone != "" {
		t.Fatalf("zoneless failure logged a timezone it never loaded: %q", zone)
	}
}

// This test protects failed-start cleanup and fails if a Stop racing a failed
// initialization deadlocks, leaves the scheduler marked running or holding
// lifecycle channels, or poisons a later start. The gated clock holds the
// start inside initialization while a concurrent Stop parks behind the
// lifecycle mutex; releasing the gate with a missing household timezone fails
// the start deterministically. Channel handshakes order every step, so no
// sleep is needed; the shared barrier watchdog turns a deadlock into a test
// failure.
func TestAutomationSchedulerStopRacingFailedInit(t *testing.T) {
	t.Parallel()
	harness := newSchedulerLifecycleHarness(t, time.UTC)
	clockEntered, clockRelease := make(chan struct{}), make(chan struct{})
	originalClock := harness.service.schedulerClock
	var clockCalls atomic.Int64
	harness.service.schedulerClock = func() time.Time {
		if clockCalls.Add(1) == 1 {
			close(clockEntered)
			select {
			case <-clockRelease:
			case <-time.After(10 * time.Second):
				t.Error("scheduler init gate was never released")
			}
		}
		return originalClock()
	}
	// Fail initialization deterministically without touching storage: no
	// household zone. The write races nothing; both lifecycle goroutines start
	// below.
	harness.service.timezone = nil
	startDone := make(chan struct{})
	var startErr error
	go func() {
		defer close(startDone)
		startErr = harness.service.StartAutomationScheduler(t.Context())
	}()
	receiveAutomationBarrier(t, clockEntered)
	// The previous lifecycle published its channels before initialization, so a
	// racing Stop parked on done forever when init failed. Channels are now
	// assigned only on success; when the racing start exposed a stop signal,
	// wait for the Stop to park on it before failing the init, so the overlap
	// is guaranteed rather than racy.
	stopCapture := harness.service.schedulerStop
	stopCalled := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		close(stopCalled)
		harness.service.StopAutomationScheduler()
	}()
	receiveAutomationBarrier(t, stopCalled)
	if stopCapture != nil {
		select {
		case <-stopCapture:
		case <-time.After(10 * time.Second):
			t.Fatal("racing stop never parked on the exposed stop signal")
		}
	}
	close(clockRelease)
	receiveAutomationBarrier(t, stopped)
	receiveAutomationBarrier(t, startDone)
	if !errors.Is(startErr, ErrInvalidAutomation) {
		t.Fatalf("racing failed start = %v", startErr)
	}
	harness.service.schedulerMu.Lock()
	running, stop, done := harness.service.schedulerRunning, harness.service.schedulerStop, harness.service.schedulerDone
	harness.service.schedulerMu.Unlock()
	if running || stop != nil || done != nil {
		t.Fatal("failed start left the scheduler marked running or holding channels")
	}
	if harness.service.AutomationSchedulerHealthy() || harness.service.AutomationSchedulerReady() {
		t.Fatal("failed start reported scheduler health")
	}
	// A failed start must leave the lifecycle reusable: restore the zone and
	// the ungated clock, restart successfully, then prove Stop clears health
	// so a stopped scheduler is not ready.
	harness.service.timezone = time.UTC
	harness.service.schedulerClock = originalClock
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatalf("restart after failed start = %v", err)
	}
	if !harness.service.AutomationSchedulerHealthy() || !harness.service.AutomationSchedulerReady() {
		t.Fatal("restart after failed start did not restore health and readiness")
	}
	harness.service.StopAutomationScheduler()
	if harness.service.AutomationSchedulerHealthy() || harness.service.AutomationSchedulerReady() {
		t.Fatal("stopped scheduler reported ready")
	}
}

// This test protects serialized restart and fails if a Start racing a Stop
// initializes a new loop before the prior loop exits. The barrier-held tick
// keeps the old loop open while Stop enters its join, proven by the closed
// stop signal; only then is the racing Start attempted. The swapped clock
// stamps the racing initialization, which must sort after the old loop exit.
// Channel handshakes order every step, so no sleep is needed.
func TestAutomationSchedulerStartWaitsForPriorLoopExit(t *testing.T) {
	t.Parallel()
	harness := newSchedulerLifecycleHarness(t, time.UTC)
	createSchedulerAutomation(t, harness, scheduleTrigger("every", "* * * * *"))
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	stopA := harness.service.schedulerStop
	doneA := harness.service.schedulerDone
	if stopA == nil || doneA == nil {
		t.Fatal("started scheduler exposed no lifecycle channels")
	}
	// Hold the old loop inside one tick so Stop parks in its join.
	entered, release := make(chan struct{}), make(chan struct{})
	originalNewRunID := harness.repo.newRunID
	harness.repo.newRunID = func() (AutomationRunID, error) {
		close(entered)
		<-release
		return originalNewRunID()
	}
	signalSchedulerTick(harness, scheduleMinute(19, 0).Add(20*time.Second))
	receiveAutomationBarrier(t, entered)
	// The in-flight tick already read the loop clock before reaching the
	// barrier, so every read after this swap belongs to the racing start. The
	// probe is ordered, not timed: on the serialized lifecycle the racing
	// start can only acquire the lifecycle mutex after the parked Stop joins,
	// and the join only completes after the old done channel closes, so the
	// non-blocking receive must observe the closed channel. A wall-clock
	// comparison would be racy here because an exit watcher goroutine can be
	// descheduled arbitrarily long after the close it observes.
	originalClock := harness.service.schedulerClock
	var initAfterExit bool
	harness.service.schedulerClock = func() time.Time {
		select {
		case <-doneA:
			initAfterExit = true
		default:
			initAfterExit = false
		}
		return originalClock()
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		harness.service.StopAutomationScheduler()
	}()
	// The closed stop signal proves Stop holds the lifecycle mutex inside its
	// join; only then may the racing Start be attempted.
	select {
	case <-stopA:
	case <-time.After(10 * time.Second):
		t.Fatal("stop never entered its join")
	}
	startCalled := make(chan struct{})
	startDone := make(chan struct{})
	var startErr error
	go func() {
		defer close(startDone)
		close(startCalled)
		startErr = harness.service.StartAutomationScheduler(t.Context())
	}()
	receiveAutomationBarrier(t, startCalled)
	// Release the in-flight tick: the old loop exits, the parked Stop joins,
	// and only then may the racing Start initialize.
	close(release)
	receiveAutomationBarrier(t, stopped)
	receiveAutomationBarrier(t, startDone)
	if startErr != nil {
		t.Fatalf("racing start = %v", startErr)
	}
	if !initAfterExit {
		t.Fatal("racing start initialized before the prior loop exited")
	}
	// Exactly the held tick committed; the new loop evaluated nothing.
	waitSchedulerOccurrences(t, harness, 1)
	if !harness.service.AutomationSchedulerHealthy() || !harness.service.AutomationSchedulerReady() {
		t.Fatal("restarted scheduler is not healthy and ready")
	}
}

// This test protects injected-wakeup closure and fails if a closed wakeup busy
// spins the loop instead of terminating it, or if the dead scheduler still
// reports ready. Closing the harness wakeup before start drives the exact
// path; the done channel proves termination without any sleep.
func TestAutomationSchedulerClosedWakeupTerminates(t *testing.T) {
	t.Parallel()
	harness := newSchedulerLifecycleHarness(t, time.UTC)
	close(harness.wakeup)
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := harness.service.schedulerDone
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("closed wakeup never terminated the loop")
	}
	if count := schedulerOccurrenceCount(t, harness); count != 0 {
		t.Fatalf("closed wakeup evaluated: %d occurrences", count)
	}
	if harness.service.AutomationSchedulerHealthy() || harness.service.AutomationSchedulerReady() {
		t.Fatal("terminated scheduler reported ready")
	}
	if !harness.logs.hasEvent("automation.scheduler_failed") {
		t.Fatal("missing automation.scheduler_failed event for closed wakeup")
	}
	if zone, ok := harness.logs.firstEventField("automation.scheduler_failed", "timezone"); !ok || zone != "UTC" {
		t.Fatalf("closed-wakeup failure lost its timezone: %+v", harness.logs.events)
	}
	// The dead loop still joins cleanly through Stop, and a restart on a fresh
	// wakeup recovers health.
	harness.service.StopAutomationScheduler()
	harness.wakeup = make(chan struct{}, schedulerTestWakeupBuffer)
	harness.service.schedulerWakeup = harness.wakeup
	if err := harness.service.StartAutomationScheduler(t.Context()); err != nil {
		t.Fatalf("restart after closed wakeup = %v", err)
	}
	if !harness.service.AutomationSchedulerHealthy() || !harness.service.AutomationSchedulerReady() {
		t.Fatal("restart after closed wakeup did not restore health and readiness")
	}
}
