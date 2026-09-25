package automations_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// blockingAdmissionRepository holds admission before commit and worker registration.
type blockingAdmissionRepository struct {
	automations.Repository

	enteredOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

func newBlockingAdmissionRepository(
	repository automations.Repository,
) *blockingAdmissionRepository {
	return &blockingAdmissionRepository{
		Repository: repository,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (repository *blockingAdmissionRepository) AdmitManualRun(
	ctx context.Context,
	input automations.ManualRunInput,
	snapshot devices.EntityStateSnapshot,
	now time.Time,
) (automations.ManualAdmissionResult, error) {
	repository.enteredOnce.Do(func() { close(repository.entered) })
	<-repository.release
	return repository.Repository.AdmitManualRun(ctx, input, snapshot, now)
}

func (repository *blockingAdmissionRepository) AdmitDeviceFact(
	ctx context.Context,
	fact automations.DeviceFact,
	snapshot devices.EntityStateSnapshot,
	now time.Time,
	startupAt time.Time,
) (automations.AdmissionResult, error) {
	repository.enteredOnce.Do(func() { close(repository.entered) })
	<-repository.release
	return repository.Repository.AdmitDeviceFact(ctx, fact, snapshot, now, startupAt)
}

// Drain must track a manual admission before commit, then join its worker
// as it drains with core_stopping.
func TestDrainJoinsManualAdmissionInFlightAtStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	blocking := newBlockingAdmissionRepository(
		automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies),
	)
	service := automations.NewService(blocking, scripted, dependencies)
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))

	admitted := make(chan automations.Run, 1)
	admitFailed := make(chan error, 1)
	go func() {
		run, err := service.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
		if err != nil {
			admitFailed <- err
			return
		}
		admitted <- run
	}()
	<-blocking.entered

	// Admission closes while the transaction is still in flight. The reservation
	// it already holds must keep Drain blocked until the Run drains.
	service.StopAdmission()
	waited := make(chan error, 1)
	go func() { waited <- service.Drain(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("Drain returned while a manual admission was in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(blocking.release)
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("Drain = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not join the in-flight manual admission's Run")
	}
	select {
	case err := <-admitFailed:
		t.Fatalf("StartManualRun = %v, want a committed Run", err)
	case run := <-admitted:
		entry := historyEntry(t, service, record.ID, string(run.ID))
		if entry.Run == nil || entry.Run.Status != automations.RunInterrupted ||
			entry.Run.FailureCode == nil ||
			*entry.Run.FailureCode != automations.FailureCoreStopping {
			t.Fatalf("in-flight manual Run = %#v, want interrupted/core_stopping", entry.Run)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartManualRun did not return after release")
	}
	if scripted.executionCount() != 0 {
		t.Fatalf("drain executed %d Commands, want 0", scripted.executionCount())
	}
}

// Drain must track a Fact admission before commit and join every Run it starts.
func TestDrainJoinsFactAdmissionInFlightAtStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	blocking := newBlockingAdmissionRepository(
		automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies),
	)
	service := automations.NewService(blocking, scripted, dependencies)
	entity := newEntityID(t)
	record := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))

	admitFailed := make(chan error, 1)
	go func() {
		_, err := service.ReceiveDeviceFact(ctx, newObservationFact(t, entity, runtimeTestNow))
		admitFailed <- err
	}()
	<-blocking.entered

	service.StopAdmission()
	waited := make(chan error, 1)
	go func() { waited <- service.Drain(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("Drain returned while a fact admission was in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(blocking.release)
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("Drain = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not join the in-flight fact admission's Run")
	}
	if err := <-admitFailed; err != nil {
		t.Fatalf("ReceiveDeviceFact = %v, want a committed admission", err)
	}
	history := listHistory(t, service, record.ID)
	if len(history) != 1 || history[0].Status != automations.RunInterrupted {
		t.Fatalf("in-flight fact Run history = %#v, want one interrupted Run", history)
	}
	entry := historyEntry(t, service, record.ID, history[0].ID)
	if entry.Run == nil || entry.Run.FailureCode == nil ||
		*entry.Run.FailureCode != automations.FailureCoreStopping {
		t.Fatalf("in-flight fact Run = %#v, want core_stopping", entry.Run)
	}
	if scripted.executionCount() != 0 {
		t.Fatalf("drain executed %d Commands, want 0", scripted.executionCount())
	}
}

// Closing admission mid-transaction must still let Drain join every resulting Run.
func TestDrainJoinsFactFanOutAdmissionInFlightAtStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	blocking := newBlockingAdmissionRepository(
		automationssqlite.NewAutomationRepository(openAutomationDatabase(t), dependencies),
	)
	service := automations.NewService(blocking, scripted, dependencies)
	entity := newEntityID(t)
	first := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))
	second := createRuntimeAutomation(t, service, runtimeDefinitionFor(t, entity))

	admitFailed := make(chan error, 1)
	go func() {
		_, err := service.ReceiveDeviceFact(ctx, newObservationFact(t, entity, runtimeTestNow))
		admitFailed <- err
	}()
	<-blocking.entered

	service.StopAdmission()
	// Drain must cover the transaction and both workers, with no gap.
	waited := make(chan error, 1)
	go func() { waited <- service.Drain(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("Drain returned while a fact fan-out admission was in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(blocking.release)
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("Drain = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Drain did not join every Run the in-flight fact admission started")
	}
	if err := <-admitFailed; err != nil {
		t.Fatalf("ReceiveDeviceFact = %v, want a committed admission", err)
	}
	for _, record := range []automations.Record{first, second} {
		history := listHistory(t, service, record.ID)
		if len(history) != 1 || history[0].Status != automations.RunInterrupted {
			t.Fatalf("automation %s history = %#v, want one interrupted Run", record.ID, history)
		}
		entry := historyEntry(t, service, record.ID, history[0].ID)
		if entry.Run == nil || entry.Run.FailureCode == nil ||
			*entry.Run.FailureCode != automations.FailureCoreStopping {
			t.Fatalf("automation %s Run = %#v, want core_stopping", record.ID, entry.Run)
		}
	}
	if scripted.executionCount() != 0 {
		t.Fatalf("drain executed %d Commands, want 0", scripted.executionCount())
	}
}

// A refused admission must release its reservation rather than block Drain.
func TestDrainReturnsWhenRefusedAdmissionReservationReleases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))

	// The device gate is closed, so the admission reserves and then abandons its
	// slot without committing a Run.
	scripted.setCommandAdmissionOpen(false)
	if _, err := service.StartManualRun(
		ctx, automations.ManualRunInput{AutomationID: record.ID},
	); err == nil {
		t.Fatal("StartManualRun with a closed device gate created a Run")
	}
	waiting, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Drain(waiting); err != nil {
		t.Fatalf("Drain after a refused admission = %v, want nil", err)
	}
}
