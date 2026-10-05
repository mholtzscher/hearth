package automations_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// The hooks surround real migrated repository operations, not timer callbacks.
type delayWriteRepository struct {
	automations.Repository

	start    func(context.Context, automations.DelayStart) error
	complete func(context.Context, automations.DelayCompletion) error
}

func (repository *delayWriteRepository) RecordDelayStart(ctx context.Context, start automations.DelayStart) error {
	if repository.start != nil {
		return repository.start(ctx, start)
	}
	return repository.Repository.RecordDelayStart(ctx, start)
}

func (repository *delayWriteRepository) CompleteDelay(
	ctx context.Context,
	completion automations.DelayCompletion,
) error {
	if repository.complete != nil {
		return repository.complete(ctx, completion)
	}
	return repository.Repository.CompleteDelay(ctx, completion)
}

func delayStep(id automations.StepID, duration int64) automations.Step {
	return automations.Step{ID: id, Kind: automations.StepKindDelay,
		Delay: &automations.DelayStep{DurationMS: duration}}
}

func delayService(
	t *testing.T,
	scripted *scriptedDevices,
	dependencies automations.Dependencies,
) (*automations.Service, *delayWriteRepository) {
	t.Helper()
	database := openAutomationDatabase(t)
	repository := &delayWriteRepository{Repository: automationssqlite.NewAutomationRepository(database, dependencies)}
	service := automations.NewService(repository, scripted, dependencies)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := service.Drain(ctx); err != nil {
			t.Error(err)
		}
	})
	return service, repository
}

func awaitDelaySignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("delay boundary was not reached")
	}
}

func admitDelayRun(t *testing.T, service *automations.Service, record automations.Record) automations.Run {
	t.Helper()
	run, err := service.StartManualRun(context.Background(), automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func assertDelayInterrupted(t *testing.T, run *automations.Run, code string) {
	t.Helper()
	if run == nil || run.Status != automations.RunInterrupted || run.FailureCode == nil || *run.FailureCode != code ||
		len(run.Delays) != 1 || run.Delays[0].Status != automations.DelayInterrupted ||
		run.Delays[0].FailureCode == nil || *run.Delays[0].FailureCode != code {
		t.Fatalf("expected atomic delay/Run interruption %s, got %#v", code, run)
	}
}

// A2: a delay-only Run succeeds without Command identities or device execution.
func TestDelayOnlyRun(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	service, _ := delayService(t, scripted, runtimeTestDependencies())
	definition := runtimeDefinition(t, 0)
	definition.Steps = []automations.Step{delayStep("wait", 10)}
	run := startBranchRun(t, service, definition)
	if run.Status != automations.RunSucceeded || run.Steps == nil || len(run.Steps) != 0 ||
		scripted.executionCount() != 0 || len(run.Delays) != 1 || run.Delays[0].Status != automations.DelayCompleted {
		t.Fatalf("delay-only result = %#v", run)
	}
	if run.Delays[0].DurationMS != 10 || !run.Delays[0].DueAt.Equal(run.Delays[0].StartedAt.Add(10*time.Millisecond)) {
		t.Fatalf("delay metadata = %#v", run.Delays[0])
	}
}

// A4: waits follow successful Commands and retain independent reached positions.
//
//nolint:gocognit // The routing/failure matrix checks independent evidence and device ordering together.
func TestDelaySelectedTraversalAndPriorFailure(t *testing.T) {
	t.Parallel()
	for _, failed := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "selected nested waits", true: "prior Command failed"}[failed],
			func(t *testing.T) {
				t.Parallel()
				scripted := newScriptedDevices()
				service, repository := delayService(t, scripted, runtimeTestDependencies())
				definition := runtimeDefinition(t, 3)
				commands := definition.Steps
				definition.Steps = []automations.Step{commands[0],
					branchIf("outer", branchTriggerCondition(true), []automations.Step{
						delayStep(
							"first",
							10,
						),
						branchChoose(branchTriggerCondition(false), branchTriggerCondition(true), []automations.Step{
							delayStep("unselected", 86400000), delayStep("second", 10)}, false),
						commands[1],
					}, []automations.Step{delayStep("else-wait", 86400000), commands[2]}), delayStep("last", 10)}
				var reached atomic.Int32
				repository.start = func(ctx context.Context, start automations.DelayStart) error {
					if scripted.executionCount() != 1 && start.StepID != "last" {
						t.Error("wait started before prior Command succeeded")
					}
					reached.Add(1)
					return repository.Repository.RecordDelayStart(ctx, start)
				}
				scripted.execute = func(_ context.Context, input devices.CommandInput) (devices.CommandResult, error) {
					if failed {
						return devices.CommandResult{}, devices.ErrInvalidCommand
					}
					if input.EntityID == commands[1].EntityID && reached.Load() != 2 {
						t.Error("later Command preceded waits")
					}
					scripted.recordCommand(terminalCommand(t, input, devices.CommandStatusSatisfied, nil))
					return devices.CommandResult{CommandID: input.ID, Outcome: devices.OutcomeDispatched}, nil
				}
				run := startBranchRun(t, service, definition)
				if failed {
					if run.Status != automations.RunFailed || len(run.Delays) != 0 || len(run.BranchDecisions) != 0 ||
						scripted.executionCount() != 1 {
						t.Fatalf("prior failure = %#v", run)
					}
					return
				}
				if run.Status != automations.RunSucceeded || len(run.Delays) != 3 || len(run.BranchDecisions) != 2 ||
					scripted.executionCount() != 2 {
					t.Fatalf("selected traversal = %#v", run)
				}
				for index, id := range []automations.StepID{"first", "second", "last"} {
					if run.Delays[index].StepID != id || run.Delays[index].Position != index ||
						run.Delays[index].Status != automations.DelayCompleted {
						t.Fatalf("reached delay = %#v", run.Delays[index])
					}
				}
				if run.Steps[0].Position != 0 || run.Steps[1].Position != 1 || run.Steps[2].Position != 2 ||
					run.Steps[2].Status != automations.StepNotAttempted ||
					run.BranchDecisions[0].Position != 0 ||
					run.BranchDecisions[1].Position != 1 {
					t.Fatal("delay changed Command or branch positions")
				}
			},
		)
	}
}

// A5/A6: caller cancellation leaves the Run busy; service stop wakes a 24h wait.
func TestDelayBusyCallerCancellationAndShutdown(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	service, repository := delayService(t, scripted, runtimeTestDependencies())
	started := make(chan struct{})
	repository.start = func(ctx context.Context, start automations.DelayStart) error {
		err := repository.Repository.RecordDelayStart(ctx, start)
		close(started)
		return err
	}
	definition := runtimeDefinition(t, 1)
	definition.Steps = append([]automations.Step{delayStep("long", 86400000)}, definition.Steps...)
	record := createRuntimeAutomation(t, service, definition)
	caller, cancelCaller := context.WithCancel(context.Background())
	run, err := service.StartManualRun(caller, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	cancelCaller()
	awaitDelaySignal(t, started)
	active := historyEntry(t, service, record.ID, string(run.ID)).Run
	if active.Status != automations.RunRunning || active.Delays[0].Status != automations.DelayRunning {
		t.Fatalf("active = %#v", active)
	}
	_, err = service.StartManualRun(context.Background(), automations.ManualRunInput{AutomationID: record.ID})
	if !errors.Is(err, automations.ErrAutomationBusy) {
		t.Fatalf("manual busy = %v", err)
	}
	if len(listHistory(t, service, record.ID)) != 1 {
		t.Fatal("manual busy invented a Skip")
	}
	result, err := service.ReceiveDeviceFact(
		context.Background(),
		newObservationFact(t, definition.Triggers[0].Observation.EntityID, runtimeTestNow),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.RecordedSkips != 1 || result.StartedRuns != 0 {
		t.Fatalf("automatic busy = %#v", result)
	}
	for _, item := range listHistory(t, service, record.ID) {
		if item.Kind == automations.HistorySkip {
			entry := historyEntry(t, service, record.ID, item.ID)
			if entry.Skip == nil || entry.Skip.Reason != automations.SkipBusy {
				t.Fatalf("busy Skip = %#v", entry)
			}
		}
	}
	service.StopAdmission()
	service.StopAdmission()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = service.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	assertDelayInterrupted(t, historyEntry(t, service, record.ID, string(run.ID)).Run, automations.FailureCoreStopping)
	if scripted.executionCount() != 0 {
		t.Fatal("shutdown executed later Command")
	}
}

// A8: start persistence counts toward waiting; stop wins after expiry but before
// completion, while closure during a completion commit preserves completed evidence.
//
//nolint:gocognit // Each persistence/stop boundary has a distinct durable result in the same fixture.
func TestDelayPersistenceAndStopBoundaries(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"expired start", "stop before completion", "stop during completion"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			scripted := newScriptedDevices()
			service, repository := delayService(t, scripted, runtimeTestDependencies())
			reached, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			defer close(release)
			repository.start = func(ctx context.Context, start automations.DelayStart) error {
				if err := repository.Repository.RecordDelayStart(ctx, start); err != nil {
					return err
				}
				close(reached)
				if boundary != "stop during completion" {
					<-release
				}
				return nil
			}
			repository.complete = func(ctx context.Context, completion automations.DelayCompletion) error {
				if completion.Status == automations.DelayCompleted {
					close(completed)
					if boundary == "stop during completion" {
						service.StopAdmission()
					}
				}
				return repository.Repository.CompleteDelay(ctx, completion)
			}
			definition := runtimeDefinition(t, 1)
			definition.Steps = append([]automations.Step{delayStep("wait", 300)}, definition.Steps...)
			record := createRuntimeAutomation(t, service, definition)
			run := admitDelayRun(t, service, record)
			awaitDelaySignal(t, reached)
			if boundary != "stop during completion" {
				<-time.After(350 * time.Millisecond)
				if boundary == "stop before completion" {
					service.StopAdmission()
				}
				release <- struct{}{}
			}
			if boundary == "expired start" {
				select {
				case <-completed:
				case <-time.After(200 * time.Millisecond):
					t.Fatal("start persistence did not count toward duration")
				}
			}
			waitForRuns(t, service)
			finished := historyEntry(t, service, record.ID, string(run.ID)).Run
			switch boundary {
			case "expired start":
				if finished.Status != automations.RunSucceeded || scripted.executionCount() != 1 {
					t.Fatalf("result = %#v", finished)
				}
			case "stop before completion":
				assertDelayInterrupted(t, finished, automations.FailureCoreStopping)
				select {
				case <-completed:
					t.Fatal("stop lost to elapsed timer")
				default:
				}
			case "stop during completion":
				if finished.Status != automations.RunInterrupted || finished.FailureCode == nil ||
					*finished.FailureCode != automations.FailureCoreStopping ||
					finished.Delays[0].Status != automations.DelayCompleted {
					t.Fatalf("committed wait = %#v", finished)
				}
			}
			if boundary != "expired start" && scripted.executionCount() != 0 {
				t.Fatal("closure dispatched later Command")
			}
		})
	}
}

// A7: persistence faults stop the faulty Run and wake unrelated waits. No
// completion retry or later device call may hide failed or ambiguous evidence.
//
//nolint:gocognit // The failure matrix checks two Runs, persisted states, closed admission, and no retries.
func TestDelayFaultBroadcastAndDurableOutcomes(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"start", "completion", "ambiguous completion", "unavailable"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			scripted := newScriptedDevices()
			service, repository := delayService(t, scripted, runtimeTestDependencies())
			longStarted := make(chan struct{})
			var starts, completions atomic.Int32
			writeErr := errors.New("injected persistence fault")
			repository.start = func(ctx context.Context, start automations.DelayStart) error {
				if start.StepID == "fault" {
					starts.Add(1)
					if fault == "start" {
						return writeErr
					}
				}
				err := repository.Repository.RecordDelayStart(ctx, start)
				if start.StepID == "long" {
					close(longStarted)
				}
				return err
			}
			repository.complete = func(ctx context.Context, completion automations.DelayCompletion) error {
				if completion.StepID != "fault" {
					return repository.Repository.CompleteDelay(ctx, completion)
				}
				if completion.Status == automations.DelayCompleted {
					completions.Add(1)
					if fault == "ambiguous completion" {
						if err := repository.Repository.CompleteDelay(ctx, completion); err != nil {
							return err
						}
					}
					return writeErr
				}
				if fault == "unavailable" {
					return writeErr
				}
				return repository.Repository.CompleteDelay(ctx, completion)
			}
			longDefinition := runtimeDefinition(t, 1)
			longDefinition.Steps = append([]automations.Step{delayStep("long", 86400000)}, longDefinition.Steps...)
			longRecord := createRuntimeAutomation(t, service, longDefinition)
			longRun := admitDelayRun(t, service, longRecord)
			awaitDelaySignal(t, longStarted)
			definition := runtimeDefinition(t, 1)
			definition.Steps = append([]automations.Step{delayStep("fault", 10)}, definition.Steps...)
			record := createRuntimeAutomation(t, service, definition)
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			service.StopAdmission() // A later shutdown cannot replace executor_fault.
			if service.AdmissionOpen() || scripted.executionCount() != 0 || starts.Load() != 1 {
				t.Fatal("fault did not latch or execution retried")
			}
			_, err := service.StartManualRun(context.Background(), automations.ManualRunInput{AutomationID: record.ID})
			if !errors.Is(err, automations.ErrAdmissionUnavailable) {
				t.Fatalf("fault admission = %v", err)
			}
			assertDelayInterrupted(
				t,
				historyEntry(t, service, longRecord.ID, string(longRun.ID)).Run,
				automations.FailureExecutorFault,
			)
			finished := historyEntry(t, service, record.ID, string(run.ID)).Run
			if fault == "start" {
				if len(finished.Delays) != 0 || finished.Status != automations.RunInterrupted ||
					*finished.FailureCode != automations.FailureExecutorFault ||
					completions.Load() != 0 {
					t.Fatalf("start fault = %#v", finished)
				}
				return
			}
			if completions.Load() != 1 {
				t.Fatal("completion was retried")
			}
			switch fault {
			case "completion":
				assertDelayInterrupted(t, finished, automations.FailureExecutorFault)
			case "ambiguous completion":
				if finished.Status != automations.RunRunning ||
					finished.Delays[0].Status != automations.DelayCompleted {
					t.Fatalf("ambiguous durable state = %#v", finished)
				}
				assertDelayFaultRecovery(t, service, record.ID, run.ID, finished)
			case "unavailable":
				if finished.Status != automations.RunRunning || finished.Delays[0].Status != automations.DelayRunning {
					t.Fatalf("unavailable durable state = %#v", finished)
				}
				assertDelayFaultRecovery(t, service, record.ID, run.ID, finished)
			}
		})
	}
}

func assertDelayFaultRecovery(
	t *testing.T,
	service *automations.Service,
	automationID automations.AutomationID,
	runID automations.RunID,
	before *automations.Run,
) {
	t.Helper()
	if err := service.InterruptActiveRuns(context.Background(), runtimeTestNow.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	recovered := historyEntry(t, service, automationID, string(runID)).Run
	if before.Delays[0].Status == automations.DelayRunning {
		assertDelayInterrupted(t, recovered, automations.FailureCoreRestarted)
	} else if recovered.Status != automations.RunInterrupted || recovered.FailureCode == nil ||
		*recovered.FailureCode != automations.FailureCoreRestarted || !reflect.DeepEqual(recovered.Delays, before.Delays) {
		t.Fatalf("recovery rewrote completed delay or retained active parent: %#v", recovered)
	}
}

// A8: diagnostic jumps cannot shorten or extend the native wait.
func TestDelayWallClockJumpsPreserveNativeInterval(t *testing.T) {
	t.Parallel()
	for _, jump := range []time.Duration{-48 * time.Hour, 48 * time.Hour} {
		t.Run(jump.String(), func(t *testing.T) {
			t.Parallel()
			var wallOffset atomic.Int64
			dependencies := runtimeTestDependencies()
			dependencies.Now = func() time.Time { return runtimeTestNow.Add(time.Duration(wallOffset.Load())) }
			scripted := newScriptedDevices()
			service, repository := delayService(t, scripted, dependencies)
			started := make(chan struct{})
			var begin, ended time.Time
			repository.start = func(ctx context.Context, start automations.DelayStart) error {
				begin = time.Now()
				err := repository.Repository.RecordDelayStart(ctx, start)
				wallOffset.Store(int64(jump))
				close(started)
				return err
			}
			repository.complete = func(ctx context.Context, completion automations.DelayCompletion) error {
				ended = time.Now()
				return repository.Repository.CompleteDelay(ctx, completion)
			}
			definition := runtimeDefinition(t, 0)
			definition.Steps = []automations.Step{delayStep("wait", 150)}
			record := createRuntimeAutomation(t, service, definition)
			run := admitDelayRun(t, service, record)
			awaitDelaySignal(t, started)
			waitForRuns(t, service)
			elapsed := ended.Sub(begin)
			finished := historyEntry(t, service, record.ID, string(run.ID)).Run
			// The repository seam follows the executor's private native start.
			// Allow 10ms for that observation gap, not admission or editing work.
			if elapsed < 140*time.Millisecond || elapsed > 5*time.Second {
				t.Fatalf("native interval = %s", elapsed)
			}
			if finished.Status != automations.RunSucceeded || finished.Delays[0].DurationMS != 150 ||
				!finished.Delays[0].StartedAt.Equal(
					runtimeTestNow,
				) || !finished.Delays[0].CompletedAt.Equal(runtimeTestNow.Add(jump)) ||
				scripted.executionCount() != 0 || len(finished.Steps) != 0 {
				t.Fatalf("snapshot or post-wait State = %#v", finished)
			}
		})
	}
}

func TestDelayActiveSnapshotSurvivesReplacementDisablementAndDeletion(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	service, repository := delayService(t, scripted, runtimeTestDependencies())
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	repository.start = func(ctx context.Context, start automations.DelayStart) error {
		err := repository.Repository.RecordDelayStart(ctx, start)
		close(started)
		<-release
		return err
	}
	definition := runtimeDefinition(t, 1)
	definition.Steps = append([]automations.Step{delayStep("wait", 10)}, definition.Steps...)
	record := createRuntimeAutomation(t, service, definition)
	run := admitDelayRun(t, service, record)
	awaitDelaySignal(t, started)
	replacement := definition
	replacement.Enabled = false
	replacement.Steps = []automations.Step{delayStep("replacement", 86400000)}
	updated, err := service.ReplaceAutomation(context.Background(), record.ID, record.Revision, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.DeleteAutomation(context.Background(), record.ID, updated.Revision); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	waitForRuns(t, service)
	finished := historyEntry(t, service, record.ID, string(run.ID)).Run
	if finished.Status != automations.RunSucceeded || finished.Delays[0].StepID != "wait" ||
		finished.Delays[0].DurationMS != 10 ||
		scripted.executionCount() != 1 ||
		finished.Steps[0].Status != automations.StepSatisfied {
		t.Fatalf("active snapshot changed: %#v", finished)
	}
}

// Ambiguous writes are injected after real commits. Workers must neither retry
// nor continue; startup may interrupt active evidence but never rewrite terminal evidence.
//
//nolint:gocognit // Each ambiguity boundary has distinct durable states before and after recovery.
func TestDelayAmbiguousStartAndInterruptionRecovery(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"start", "interruption"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			scripted := newScriptedDevices()
			service, repository := delayService(t, scripted, runtimeTestDependencies())
			var starts, completions atomic.Int32
			writeErr := errors.New("commit succeeded but acknowledgement failed")
			repository.start = func(ctx context.Context, start automations.DelayStart) error {
				starts.Add(1)
				if err := repository.Repository.RecordDelayStart(ctx, start); err != nil {
					return err
				}
				if boundary == "start" {
					return writeErr
				}
				service.StopAdmission()
				return nil
			}
			repository.complete = func(ctx context.Context, completion automations.DelayCompletion) error {
				completions.Add(1)
				if completion.Status != automations.DelayInterrupted {
					t.Error("stopped wait attempted successful completion")
				}
				if err := repository.Repository.CompleteDelay(ctx, completion); err != nil {
					return err
				}
				return writeErr
			}
			definition := runtimeDefinition(t, 1)
			definition.Steps = append([]automations.Step{delayStep("wait", 86400000)}, definition.Steps...)
			record := createRuntimeAutomation(t, service, definition)
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			finished := historyEntry(t, service, record.ID, string(run.ID)).Run
			if service.AdmissionOpen() || scripted.executionCount() != 0 || starts.Load() != 1 ||
				len(finished.Delays) != 1 ||
				finished.Steps[0].Status != automations.StepNotAttempted ||
				finished.Steps[0].ReservedCommandID != nil {
				t.Fatalf("ambiguous write advanced or retried execution: %#v", finished)
			}
			_, err := service.StartManualRun(context.Background(), automations.ManualRunInput{AutomationID: record.ID})
			if !errors.Is(err, automations.ErrAdmissionUnavailable) {
				t.Fatalf("fault admission = %v", err)
			}
			if boundary == "start" {
				if completions.Load() != 0 || finished.Status != automations.RunRunning ||
					finished.Delays[0].Status != automations.DelayRunning {
					t.Fatalf("ambiguous start durable state = %#v", finished)
				}
			} else {
				if completions.Load() != 1 {
					t.Fatal("interruption retried")
				}
				assertDelayInterrupted(t, finished, automations.FailureCoreStopping)
				if !finished.CompletedAt.Equal(*finished.Delays[0].CompletedAt) {
					t.Fatal("interruption timestamps differ")
				}
			}
			if err = service.InterruptActiveRuns(context.Background(), runtimeTestNow.Add(48*time.Hour)); err != nil {
				t.Fatal(err)
			}
			recovered := historyEntry(t, service, record.ID, string(run.ID)).Run
			if boundary == "start" {
				assertDelayInterrupted(t, recovered, automations.FailureCoreRestarted)
			} else if !reflect.DeepEqual(recovered, finished) {
				t.Fatal("recovery rewrote committed interruption")
			}
			if starts.Load() != 1 ||
				completions.Load() != int32(map[string]int{"start": 0, "interruption": 1}[boundary]) ||
				scripted.executionCount() != 0 {
				t.Fatal("recovery retried execution")
			}
		})
	}
}

func TestDelayBranchReadsFreshStateWithoutRepeatingAdmissionConditions(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	service, repository := delayService(t, scripted, runtimeTestDependencies())
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	repository.start = func(ctx context.Context, start automations.DelayStart) error {
		err := repository.Repository.RecordDelayStart(ctx, start)
		close(started)
		<-release
		return err
	}
	entity := newEntityID(t)
	scripted.setEntityStateSnapshot(admissionSnapshot(admissionState(t, entity, `{"level":5}`, runtimeTestNow)))
	definition := runtimeDefinition(t, 2)
	commands := definition.Steps
	definition.Conditions = admissionConditionTree(entity, "10")
	definition.Steps = []automations.Step{
		delayStep("wait", 50),
		branchIf("fresh", *admissionConditionTree(entity, "10"), commands[:1], commands[1:]),
	}
	record := createRuntimeAutomation(t, service, definition)
	run := admitDelayRun(t, service, record)
	awaitDelaySignal(t, started)
	scripted.setEntityStateSnapshot(admissionSnapshot(admissionState(t, entity, `{"level":20}`, runtimeTestNow)))
	release <- struct{}{}
	waitForRuns(t, service)
	finished := historyEntry(t, service, record.ID, string(run.ID)).Run
	if finished.Status != automations.RunSucceeded || len(finished.BranchDecisions) != 1 ||
		finished.BranchDecisions[0].Outcome != automations.BranchElse ||
		len(scripted.snapshotRequests()) != 2 ||
		scripted.executionCount() != 1 ||
		scripted.executions[0].EntityID != commands[1].EntityID {
		t.Fatalf("fresh post-delay branch = %#v", finished)
	}
}

// A6: stopping during a Command leaves its context alive, then prevents the
// following delay from creating either reached evidence or a timer.
func TestDelayBoundaryAfterInFlightCommandShutdown(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	scripted.execute = func(ctx context.Context, input devices.CommandInput) (devices.CommandResult, error) {
		close(entered)
		<-release
		if ctx.Err() != nil {
			t.Error("service stop canceled the admitted device Command context")
		}
		scripted.recordCommand(terminalCommand(t, input, devices.CommandStatusSatisfied, nil))
		return devices.CommandResult{CommandID: input.ID, Outcome: devices.OutcomeDispatched}, nil
	}
	service, _ := delayService(t, scripted, runtimeTestDependencies())
	definition := runtimeDefinition(t, 2)
	definition.Steps = []automations.Step{definition.Steps[0], delayStep("unreached", 86400000), definition.Steps[1]}
	record := createRuntimeAutomation(t, service, definition)
	run := admitDelayRun(t, service, record)
	awaitDelaySignal(t, entered)
	service.StopAdmission()
	release <- struct{}{}
	waitForRuns(t, service)
	finished := historyEntry(t, service, record.ID, string(run.ID)).Run
	if finished.Status != automations.RunInterrupted || finished.FailureCode == nil ||
		*finished.FailureCode != automations.FailureCoreStopping || len(finished.Delays) != 0 {
		t.Fatalf("pre-delay shutdown = %#v", finished)
	}
	if finished.Steps[0].Status != automations.StepSatisfied ||
		finished.Steps[1].Status != automations.StepNotAttempted ||
		scripted.executionCount() != 1 {
		t.Fatalf("shutdown Command attempts = %#v", finished.Steps)
	}
}

// A7: Command reconciliation faults use the same broadcast as delay write
// faults, so unrelated pending waits cannot hold Drain open.
func TestDelayWakesOnCommandExecutorFault(t *testing.T) {
	t.Parallel()
	scripted := newScriptedDevices()
	scripted.getCommand = func(context.Context, devices.CommandID) (devices.CommandRecord, error) {
		return devices.CommandRecord{}, devices.ErrCommandNotFound
	}
	service, repository := delayService(t, scripted, runtimeTestDependencies())
	started := make(chan struct{})
	repository.start = func(ctx context.Context, start automations.DelayStart) error {
		err := repository.Repository.RecordDelayStart(ctx, start)
		close(started)
		return err
	}
	definition := runtimeDefinition(t, 0)
	definition.Steps = []automations.Step{delayStep("long", 86400000)}
	record := createRuntimeAutomation(t, service, definition)
	run := admitDelayRun(t, service, record)
	awaitDelaySignal(t, started)
	faultRecord := createRuntimeAutomation(t, service, runtimeDefinition(t, 2))
	faultRun := admitDelayRun(t, service, faultRecord)
	waitForRuns(t, service)
	assertDelayInterrupted(t, historyEntry(t, service, record.ID, string(run.ID)).Run, automations.FailureExecutorFault)
	finished := historyEntry(t, service, faultRecord.ID, string(faultRun.ID)).Run
	if service.AdmissionOpen() || finished.Status != automations.RunInterrupted || finished.FailureCode == nil ||
		*finished.FailureCode != automations.FailureExecutorFault || scripted.executionCount() != 1 || finished.Steps[1].Status != automations.StepNotAttempted {
		t.Fatalf("Command fault = %#v", finished)
	}
}
