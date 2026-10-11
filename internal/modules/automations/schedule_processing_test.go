package automations_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func scheduledDefinition(t *testing.T, expression string, condition *automations.Condition) automations.Definition {
	t.Helper()
	definition := runtimeDefinition(t, 1)
	definition.Triggers = []automations.Trigger{
		{ID: "scheduled", Body: automations.CronTrigger{Expression: expression}},
	}
	definition.Conditions = condition
	return definition
}

// Activation must consume its minute even when the Service has matching definitions.
func TestScheduleServiceActivationAndCurrentMinute(t *testing.T) {
	t.Parallel()
	var clock atomic.Int64
	clock.Store(runtimeTestNow.UnixNano())
	dependencies := runtimeTestDependencies()
	dependencies.Now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	dependencies.HouseholdLocation = time.FixedZone("household", 3600)
	scripted := newScriptedDevices()
	scripted.setEntityStateSnapshotError(errors.New("unconditioned scheduling must not read State"))
	service, _ := newRuntimeService(t, scripted, dependencies)
	record := createRuntimeAutomation(t, service, scheduledDefinition(t, "* 13 * * *", nil))
	clock.Store(runtimeTestNow.Add(20 * time.Second).UnixNano())
	if err := service.InitializeSchedules(context.Background(), dependencies.Now()); err != nil {
		t.Fatal(err)
	}
	outcome, err := service.ProcessDueSchedules(context.Background())
	if err != nil || outcome.StartedRuns != 0 {
		t.Fatalf("activation minute = %#v, %v", outcome, err)
	}
	// A live stall skips intervening minutes but can admit the sampled minute at :59.
	clock.Store(runtimeTestNow.Add(10*time.Minute + 59*time.Second).UnixNano())
	outcome, err = service.ProcessDueSchedules(context.Background())
	if err != nil || outcome.StartedRuns != 1 || outcome.DuplicateOutcomes != 0 {
		t.Fatalf("live current minute = %#v, %v", outcome, err)
	}
	waitForRuns(t, service)
	outcome, err = service.ProcessDueSchedules(context.Background())
	if err != nil || outcome != (automations.AdmissionOutcome{}) {
		t.Fatalf("consumed minute = %#v, %v", outcome, err)
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 || automations.CauseSource(history[0].Cause) != automations.RunSourceSchedule ||
		scripted.executionCount() != 1 {
		t.Fatalf("history = %#v, commands = %d", history, scripted.executionCount())
	}
}

func TestScheduleServiceRequiresConfiguredLocationAndOpenAdmission(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	if err := service.InitializeSchedules(
		context.Background(),
		runtimeTestNow,
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("activation without location = %v", err)
	}
	if _, err := service.ProcessDueSchedules(context.Background()); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("processing without location = %v", err)
	}
	scripted.setCommandAdmissionOpen(false)
	if _, err := service.ProcessDueSchedules(
		context.Background(),
	); !errors.Is(
		err,
		automations.ErrAdmissionUnavailable,
	) {
		t.Fatalf("closed Command admission = %v", err)
	}
	service.StopAdmission()
	if _, err := service.ProcessDueSchedules(
		context.Background(),
	); !errors.Is(
		err,
		automations.ErrAdmissionUnavailable,
	) {
		t.Fatalf("closed Automation admission = %v", err)
	}
}

// Rollover requires a fresh evaluation sample and recollection for the new minute,
// not admission of the old minute or a union with unrelated Condition references.
func TestScheduleServiceRecollectsSnapshotAfterPreparationRollover(t *testing.T) {
	t.Parallel()
	var clock atomic.Int64
	clock.Store(runtimeTestNow.UnixNano())
	dependencies := runtimeTestDependencies()
	dependencies.Now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	dependencies.HouseholdLocation = time.UTC
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, dependencies)
	firstEntity, secondEntity := newEntityID(t), newEntityID(t)
	first := createRuntimeAutomation(
		t,
		service,
		scheduledDefinition(t, "1 12 * * *", admissionConditionTree(firstEntity, "30")),
	)
	second := createRuntimeAutomation(
		t,
		service,
		scheduledDefinition(t, "2 12 * * *", admissionConditionTree(secondEntity, "30")),
	)
	createRuntimeAutomation(
		t,
		service,
		scheduledDefinition(t, "3 12 * * *", admissionConditionTree(newEntityID(t), "30")),
	)
	if err := service.InitializeSchedules(context.Background(), runtimeTestNow); err != nil {
		t.Fatal(err)
	}
	clock.Store(runtimeTestNow.Add(time.Minute + 59*time.Second).UnixNano())
	scripted.setEntityStateSnapshot(admissionSnapshot(admissionState(t, firstEntity, `{"level":10}`, runtimeTestNow)))
	var reads int
	scripted.onSnapshotRead = func() {
		reads++
		if reads == 1 {
			clock.Store(runtimeTestNow.Add(2*time.Minute + 3*time.Second).UnixNano())
			scripted.setEntityStateSnapshot(
				admissionSnapshot(admissionState(t, secondEntity, `{"level":10}`, runtimeTestNow)),
			)
		}
	}
	outcome, err := service.ProcessDueSchedules(context.Background())
	if err != nil || outcome.StartedRuns != 1 {
		t.Fatalf("rollover admission = %#v, %v", outcome, err)
	}
	waitForRuns(t, service)
	requests := scripted.snapshotRequests()
	if len(requests) != 2 || !slices.Equal(requests[0], []devices.EntityID{firstEntity}) ||
		!slices.Equal(requests[1], []devices.EntityID{secondEntity}) {
		t.Fatalf("snapshot requests = %v", requests)
	}
	if old := listHistory(t, service, first.ID); len(old) != 0 {
		t.Fatalf("previous minute history = %#v", old)
	}
	history := listHistory(t, service, second.ID)
	if len(history) != 1 {
		t.Fatalf("current minute history = %#v", history)
	}
	entry := historyEntry(t, service, second.ID, history[0].ID)
	if runEntry(entry) == nil || !runEntry(entry).StartedAt.Equal(dependencies.Now()) {
		t.Fatalf("fresh admission timestamp = %#v", runEntry(entry))
	}
}

type scheduleAdmissionRepository struct {
	automations.Repository

	beforeListReturn func()
	admit            func(context.Context, devices.EntityStateSnapshot, automations.ScheduleTick) (automations.AdmissionResult, error)
	executorWrites   atomic.Int64
}

func (repository *scheduleAdmissionRepository) MarkStepRunning(ctx context.Context, start automations.StepStart) error {
	repository.executorWrites.Add(1)
	return repository.Repository.MarkStepRunning(ctx, start)
}

func (repository *scheduleAdmissionRepository) CompleteRun(
	ctx context.Context,
	completion automations.RunCompletion,
) error {
	repository.executorWrites.Add(1)
	return repository.Repository.CompleteRun(ctx, completion)
}

func (repository *scheduleAdmissionRepository) ListEnabledAutomations(
	ctx context.Context,
) ([]automations.Record, error) {
	records, err := repository.Repository.ListEnabledAutomations(ctx)
	if repository.beforeListReturn != nil {
		repository.beforeListReturn()
	}
	return records, err
}

func (repository *scheduleAdmissionRepository) AdmitDueSchedules(
	ctx context.Context, snapshot devices.EntityStateSnapshot, tick automations.ScheduleTick,
) (automations.AdmissionResult, error) {
	if repository.admit != nil {
		return repository.admit(ctx, snapshot, tick)
	}
	return repository.Repository.AdmitDueSchedules(ctx, snapshot, tick)
}

// A nonmatching pre-read must not suppress a newly eligible transactional revision.
func TestScheduleServiceRetriesNewlyMatchingRevision(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	dependencies.HouseholdLocation = time.UTC
	baseDatabase := openAutomationDatabase(t)
	base := automationssqlite.NewAutomationRepository(baseDatabase, dependencies)
	repository := &scheduleAdmissionRepository{Repository: base}
	service := automations.NewService(repository, scripted, dependencies)
	entity := newEntityID(t)
	record := createRuntimeAutomation(
		t,
		service,
		scheduledDefinition(t, "1 12 * * *", admissionConditionTree(entity, "30")),
	)
	if err := service.InitializeSchedules(context.Background(), runtimeTestNow.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Repository fixture writes occurred before this minute; replace at that same
	// earlier clock so the transactional revision is eligible for the sampled minute.
	var replaced bool
	repository.beforeListReturn = func() {
		if replaced {
			return
		}
		replaced = true
		definition := scheduledDefinition(t, "* 12 * * *", admissionConditionTree(entity, "30"))
		definition.Name = "Current revision"
		if _, err := base.ReplaceAutomation(context.Background(), record.ID, record.Revision, definition); err != nil {
			t.Fatal(err)
		}
		// Admission and completion must use the sampled service clock after the
		// revision write, which intentionally used the earlier fixture clock.
		dependencies.Now = func() time.Time { return runtimeTestNow.Add(2 * time.Minute) }
		base = automationssqlite.NewAutomationRepository(baseDatabase, dependencies)
		repository.Repository = base
	}
	// Sample 12:02, where the original expression does not match, but the replacement does.
	dependencies.Now = func() time.Time { return runtimeTestNow.Add(2 * time.Minute) }
	service = automations.NewService(repository, scripted, dependencies)
	scripted.setEntityStateSnapshot(admissionSnapshot(admissionState(t, entity, `{"level":10}`, runtimeTestNow)))
	outcome, err := service.ProcessDueSchedules(context.Background())
	if err != nil || outcome.StartedRuns != 1 {
		t.Fatalf("replacement admission = %#v, %v", outcome, err)
	}
	waitForRuns(t, service)
	requests := scripted.snapshotRequests()
	if len(requests) != 1 || !slices.Equal(requests[0], []devices.EntityID{entity}) {
		t.Fatalf("replacement snapshot requests = %v", requests)
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 {
		t.Fatalf("replacement history = %#v", history)
	}
	entry := historyEntry(t, service, record.ID, history[0].ID)
	if runEntry(entry) == nil || runEntry(entry).Revision != record.Revision+1 ||
		runEntry(entry).Snapshot.Name != "Current revision" {
		t.Fatalf("admitted revision = %#v", runEntry(entry))
	}
}

// Failed admission cannot launch Commands, and ordinary failures must not retry.
func TestScheduleServiceDoesNotExecuteOrRetryFailedAdmission(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	dependencies.HouseholdLocation = time.UTC
	repository := &scheduleAdmissionRepository{Repository: newAutomationRepository(t, openAutomationDatabase(t))}
	service := automations.NewService(repository, scripted, dependencies)
	record := createRuntimeAutomation(t, service, scheduledDefinition(t, "* * * * *", nil))
	wantErr := errors.New("commit failed")
	runID, err := automations.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	run := automations.NewRunSnapshot(record, runID, automations.ScheduleCause{},
		[]automations.TriggerID{"scheduled"}, automations.NotConfiguredDecision(), runtimeTestNow)
	calls := 0
	repository.admit = func(context.Context, devices.EntityStateSnapshot, automations.ScheduleTick) (automations.AdmissionResult, error) {
		calls++
		return automations.AdmissionResult{StartedRuns: []automations.Run{run}}, wantErr
	}
	if _, admissionErr := service.ProcessDueSchedules(context.Background()); !errors.Is(admissionErr, wantErr) {
		t.Fatalf("failed admission = %v", admissionErr)
	}
	waitForRuns(t, service)
	if calls != 1 || repository.executorWrites.Load() != 0 || scripted.executionCount() != 0 ||
		len(listHistory(t, service, record.ID)) != 0 {
		t.Fatalf("calls = %d, executor persistence attempts = %d, Commands = %d",
			calls, repository.executorWrites.Load(), scripted.executionCount())
	}
}

// Measure preparation and atomic admission together, not just a preloaded tick.
// The real two-second Service deadline is the bound; worker completion is untimed.
// Opt in without race instrumentation for capacity measurements, not CI timing.
//
//nolint:paralleltest // Capacity measurements must not compete with parallel fixture workloads.
func TestScheduleServiceLargeDefinitionSetAdmission(t *testing.T) {
	if os.Getenv("HEARTH_SCHEDULE_TIMING") != "1" {
		t.Skip("set HEARTH_SCHEDULE_TIMING=1 and run without race instrumentation for the bounded capacity measurement")
	}
	const definitionCount = 1000
	const matchingCount = 100
	const entityCount = 100
	dependencies := runtimeTestDependencies()
	dependencies.HouseholdLocation = time.UTC
	dependencies.Logger = slog.New(slog.DiscardHandler)
	scripted := newScriptedDevices()
	base := automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies)
	repository := &scheduleAdmissionRepository{Repository: base}
	service := automations.NewService(repository, scripted, dependencies)
	entities := make([]devices.EntityID, entityCount)
	entries := make([]devices.EntityStateSnapshotEntry, entityCount)
	for index := range entities {
		entities[index] = newEntityID(t)
		entries[index] = admissionState(t, entities[index], `{"level":10}`, runtimeTestNow)
	}
	scripted.setEntityStateSnapshot(admissionSnapshot(entries...))
	for index := range definitionCount {
		expression := "0 13 * * *"
		if index < matchingCount {
			expression = "* * * * *"
		}
		definition := scheduledDefinition(t, expression, admissionConditionTree(entities[index%entityCount], "30"))
		definition.Name = fmt.Sprintf("Scheduled room %d", index)
		createRuntimeAutomation(t, service, definition)
	}
	if err := service.InitializeSchedules(context.Background(), runtimeTestNow); err != nil {
		t.Fatal(err)
	}
	dependencies.Now = func() time.Time { return runtimeTestNow.Add(time.Minute) }
	service = automations.NewService(repository, scripted, dependencies)
	var transactionElapsed time.Duration
	repository.admit = func(ctx context.Context, snapshot devices.EntityStateSnapshot, tick automations.ScheduleTick) (automations.AdmissionResult, error) {
		started := time.Now()
		result, err := base.AdmitDueSchedules(ctx, snapshot, tick)
		transactionElapsed = time.Since(started)
		return result, err
	}
	started := time.Now()
	outcome, err := service.ProcessDueSchedules(context.Background())
	elapsed := time.Since(started)
	t.Logf(
		"definitions=%d, cron matches=%d with true Conditions, State entities=%d, Service preparation+admission+launch=%s, atomic repository tick=%s, admission deadline=%s",
		definitionCount,
		matchingCount,
		entityCount,
		elapsed,
		transactionElapsed,
		automations.AdmissionTimeout,
	)
	waitForRuns(t, service)
	if err != nil || outcome.MatchedAutomations != matchingCount || outcome.StartedRuns != matchingCount {
		t.Fatalf("large definition admission = %#v, %v", outcome, err)
	}
	requests := scripted.snapshotRequests()
	if len(requests) != 1 || len(requests[0]) != entityCount {
		t.Fatalf("snapshot requests = %v", requests)
	}
	if scripted.executionCount() != matchingCount {
		t.Fatalf("Commands = %d, want %d", scripted.executionCount(), matchingCount)
	}
}

// An in-flight transaction holds a reservation. Stop must join it and its
// committed worker, which interrupts without dispatching after admission closes.
func TestScheduleServiceDrainJoinsAdmissionBeforeCommit(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	dependencies.HouseholdLocation = time.UTC
	base := automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies)
	repository := &scheduleAdmissionRepository{Repository: base}
	service := automations.NewService(repository, scripted, dependencies)
	record := createRuntimeAutomation(t, service, scheduledDefinition(t, "* * * * *", nil))
	if err := service.InitializeSchedules(context.Background(), runtimeTestNow); err != nil {
		t.Fatal(err)
	}
	dependencies.Now = func() time.Time { return runtimeTestNow.Add(time.Minute) }
	service = automations.NewService(repository, scripted, dependencies)
	entered, release := make(chan struct{}), make(chan struct{})
	repository.admit = func(ctx context.Context, snapshot devices.EntityStateSnapshot, tick automations.ScheduleTick) (automations.AdmissionResult, error) {
		close(entered)
		<-release
		return base.AdmitDueSchedules(ctx, snapshot, tick)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := service.ProcessDueSchedules(context.Background())
		finished <- err
	}()
	<-entered
	if scripted.executionCount() != 0 || len(listHistory(t, service, record.ID)) != 0 {
		t.Fatal("schedule executed or persisted before admission")
	}
	service.StopAdmission()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	err := service.Drain(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain did not track in-flight admission: %v", err)
	}
	close(release)
	if admissionErr := <-finished; admissionErr != nil {
		t.Fatal(admissionErr)
	}
	if drainErr := service.Drain(context.Background()); drainErr != nil {
		t.Fatal(drainErr)
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 || history[0].Body.(automations.RunHistorySummary).Status != automations.RunInterrupted ||
		scripted.executionCount() != 0 {
		t.Fatalf("drained schedule = %#v, Commands = %d", history, scripted.executionCount())
	}
}
