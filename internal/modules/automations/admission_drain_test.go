package automations_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// blockingAdmissionRepository holds one admission call at a deterministic
// barrier before it commits, so a test can close automation admission while an
// admission is in flight and before any worker is registered.
type blockingAdmissionRepository struct {
	automations.AutomationRepository

	enteredOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

func newBlockingAdmissionRepository(
	repository automations.AutomationRepository,
) *blockingAdmissionRepository {
	return &blockingAdmissionRepository{
		AutomationRepository: repository,
		entered:              make(chan struct{}),
		release:              make(chan struct{}),
	}
}

func (repository *blockingAdmissionRepository) AdmitManualRun(
	ctx context.Context,
	id automations.AutomationID,
	now time.Time,
) (automations.AutomationRun, error) {
	repository.enteredOnce.Do(func() { close(repository.entered) })
	<-repository.release
	return repository.AutomationRepository.AdmitManualRun(ctx, id, now)
}

func (repository *blockingAdmissionRepository) AdmitDeviceFact(
	ctx context.Context,
	fact automations.DeviceFact,
	now time.Time,
) (automations.AdmissionResult, error) {
	repository.enteredOnce.Do(func() { close(repository.entered) })
	<-repository.release
	return repository.AutomationRepository.AdmitDeviceFact(ctx, fact, now)
}

// TestWaitRunsJoinsManualAdmissionInFlightAtStop protects A13: a manual
// admission that passed the gate but has not committed when StopAdmission runs
// must participate in WaitRuns, so its committed Run worker is joined and drains
// with core_stopping instead of registering after the wait. It fails if
// registration escapes the gate by only being tracked after the admission
// transaction returns.
func TestWaitRunsJoinsManualAdmissionInFlightAtStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	blocking := newBlockingAdmissionRepository(
		automations.NewSQLiteRepository(openAutomationDatabase(t), dependencies),
	)
	service := automations.NewService(blocking, scripted, dependencies)
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))

	admitted := make(chan automations.AutomationRun, 1)
	admitFailed := make(chan error, 1)
	go func() {
		run, err := service.StartManualRun(ctx, record.ID)
		if err != nil {
			admitFailed <- err
			return
		}
		admitted <- run
	}()
	<-blocking.entered

	// Admission closes while the transaction is still in flight. The reservation
	// it already holds must keep WaitRuns blocked until the Run drains.
	service.StopAdmission()
	waited := make(chan error, 1)
	go func() { waited <- service.WaitRuns(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("WaitRuns returned while a manual admission was in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(blocking.release)
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("WaitRuns = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitRuns did not join the in-flight manual admission's Run")
	}
	select {
	case err := <-admitFailed:
		t.Fatalf("StartManualRun = %v, want a committed Run", err)
	case run := <-admitted:
		entry := historyEntry(t, service, record.ID, string(run.ID))
		if entry.Run == nil || entry.Run.Status != automations.RunInterrupted ||
			entry.Run.FailureCode == nil ||
			*entry.Run.FailureCode != automations.AutomationFailureCoreStopping {
			t.Fatalf("in-flight manual Run = %#v, want interrupted/core_stopping", entry.Run)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartManualRun did not return after release")
	}
	if scripted.executionCount() != 0 {
		t.Fatalf("drain executed %d Commands, want 0", scripted.executionCount())
	}
}

// TestWaitRunsJoinsFactAdmissionInFlightAtStop protects A13 for automatic
// admission: a fact admission that passed the gate but has not committed when
// StopAdmission runs must participate in WaitRuns, so every Run it started is
// joined and drains with core_stopping. It fails if a committed fan-out Run can
// be registered after the wait returned.
func TestWaitRunsJoinsFactAdmissionInFlightAtStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	dependencies := runtimeTestDependencies()
	blocking := newBlockingAdmissionRepository(
		automations.NewSQLiteRepository(openAutomationDatabase(t), dependencies),
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
	go func() { waited <- service.WaitRuns(context.Background()) }()
	select {
	case err := <-waited:
		t.Fatalf("WaitRuns returned while a fact admission was in flight: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(blocking.release)
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("WaitRuns = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitRuns did not join the in-flight fact admission's Run")
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
		*entry.Run.FailureCode != automations.AutomationFailureCoreStopping {
		t.Fatalf("in-flight fact Run = %#v, want core_stopping", entry.Run)
	}
	if scripted.executionCount() != 0 {
		t.Fatalf("drain executed %d Commands, want 0", scripted.executionCount())
	}
}

// TestWaitRunsReturnsWhenRefusedAdmissionReservationReleases protects the
// inverse of A13: an admission that reserves a slot but is refused before it
// commits must release it, so a later WaitRuns is not blocked by an admission
// that created no work. It fails if a refused manual admission leaves the
// service permanently non-idle.
func TestWaitRunsReturnsWhenRefusedAdmissionReservationReleases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scripted := newScriptedDevices()
	service, _ := newRuntimeService(t, scripted, runtimeTestDependencies())
	record := createRuntimeAutomation(t, service, runtimeDefinition(t, 1))

	// The device gate is closed, so the admission reserves and then abandons its
	// slot without committing a Run.
	scripted.setCommandAdmissionOpen(false)
	if _, err := service.StartManualRun(ctx, record.ID); err == nil {
		t.Fatal("StartManualRun with a closed device gate created a Run")
	}
	waiting, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.WaitRuns(waiting); err != nil {
		t.Fatalf("WaitRuns after a refused admission = %v, want nil", err)
	}
}
