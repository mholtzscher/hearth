package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// blockingAutomationDevices is the devices-facing Automation seam for drain
// ordering tests. Command execution blocks at a deterministic barrier, so a test
// can inspect both gates and the join order while an Automation worker is still
// in flight.
type blockingAutomationDevices struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingAutomationDevices() *blockingAutomationDevices {
	return &blockingAutomationDevices{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (seam *blockingAutomationDevices) ValidateObservationTrigger(context.Context, devices.EntityID) error {
	return nil
}

func (seam *blockingAutomationDevices) ValidateEntityEventTrigger(
	context.Context, devices.EntityID, devices.EntityEventName,
) error {
	return nil
}

func (seam *blockingAutomationDevices) ValidateCommand(
	_ context.Context, input devices.CommandInput,
) (devices.CommandParameters, error) {
	return input.Parameters, nil
}

func (seam *blockingAutomationDevices) CommandAdmissionOpen() bool { return true }

func (seam *blockingAutomationDevices) ExecuteCommand(
	context.Context, devices.CommandInput,
) (devices.CommandResult, error) {
	seam.once.Do(func() { close(seam.entered) })
	<-seam.release
	return devices.CommandResult{}, errors.New("released by test")
}

func (seam *blockingAutomationDevices) GetCommand(
	context.Context, devices.CommandID,
) (devices.CommandRecord, error) {
	return devices.CommandRecord{}, devices.ErrCommandNotFound
}

// TestCloseAdmissionClosesBothGatesBeforeJoiningWorkers protects A13's shutdown
// ordering: the automation Device Fact consumer stops, automation admission
// closes, and Command admission closes before any worker is waited on. It leaves
// an Automation worker blocked in a Command and proves that Command admission is
// already closed while that worker is still in flight, which fails if the
// automation wait runs before Command admission closes.
func TestCloseAdmissionClosesBothGatesBeforeJoiningWorkers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openOrderingDatabase(ctx, t)
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	deviceService := devices.NewService(
		devices.SQLiteStores(devices.NewSQLiteRepository(database, catalog)),
		nil, catalog, devices.Dependencies{},
	)
	seam := newBlockingAutomationDevices()
	automationService := automations.NewService(
		automations.NewSQLiteRepository(database, automations.AutomationDependencies{}),
		seam,
		automations.AutomationDependencies{Logger: slog.New(slog.DiscardHandler)},
	)
	record := createOrderingAutomation(ctx, t, automationService)
	run, err := automationService.StartManualRun(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The Run now holds an admitted Command worker blocked inside its first Step.
	<-seam.entered

	consumers := newAutomationConsumers(ctx, slog.New(slog.DiscardHandler))
	closed := make(chan struct{})
	go func() {
		closeAdmission(automationService, consumers, deviceService)
		close(closed)
	}()
	// closeAdmission must stop new admission without waiting for the blocked
	// worker, so the waits can run after both gates are closed.
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("closeAdmission waited for the in-flight worker")
	}

	if automationService.AdmissionOpen() {
		t.Fatal("automation admission stayed open after closeAdmission")
	}
	if deviceService.CommandAdmissionOpen() {
		t.Fatal("Command admission stayed open after closeAdmission")
	}
	// The Automation worker is still blocked, so Command admission must already be
	// closed: a new Command is refused before any worker wait has run.
	if _, commandErr := deviceService.ExecuteCommand(ctx, devices.CommandInput{}); !errors.Is(
		commandErr, devices.ErrCommandUnavailable,
	) {
		t.Fatalf("command admission during automation drain = %v, want ErrCommandUnavailable", commandErr)
	}

	close(seam.release)
	joined := make(chan struct{})
	go func() {
		joinAdmittedExecution(automationService, deviceService)
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("admitted execution did not join after release")
	}
	entry, err := automationService.GetHistoryEntry(ctx, record.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Run == nil || entry.Run.Status == automations.RunRunning {
		t.Fatalf("drained Run = %#v, want a terminal outcome", entry.Run)
	}
}

// TestDrainExecutionJoinsInFlightWorkerBeforeCancelingDependencies protects the
// deferred error-exit cleanup: drainExecution must close both gates, join the
// in-flight Automation worker, and only then cancel shared dependencies. It
// fails if a worker can outlive the dependency context or if the deferred
// cleanup returns before its worker is joined.
func TestDrainExecutionJoinsInFlightWorkerBeforeCancelingDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openOrderingDatabase(ctx, t)
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	deviceService := devices.NewService(
		devices.SQLiteStores(devices.NewSQLiteRepository(database, catalog)),
		nil, catalog, devices.Dependencies{},
	)
	seam := newBlockingAutomationDevices()
	automationService := automations.NewService(
		automations.NewSQLiteRepository(database, automations.AutomationDependencies{}),
		seam,
		automations.AutomationDependencies{Logger: slog.New(slog.DiscardHandler)},
	)
	record := createOrderingAutomation(ctx, t, automationService)
	run, err := automationService.StartManualRun(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	<-seam.entered

	dependencyContext, cancelDependencies := context.WithCancel(context.Background())
	consumers := newAutomationConsumers(ctx, slog.New(slog.DiscardHandler))
	drained := make(chan struct{})
	go func() {
		drainExecution(automationService, consumers, deviceService, cancelDependencies)
		close(drained)
	}()
	// The blocked worker must keep drainExecution from returning.
	select {
	case <-drained:
		t.Fatal("drainExecution returned while the Automation worker was blocked")
	case <-time.After(50 * time.Millisecond):
	}

	close(seam.release)
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("drainExecution did not join the in-flight worker")
	}
	if dependencyContext.Err() == nil {
		t.Fatal("drainExecution left shared dependencies uncanceled")
	}
	if automationService.AdmissionOpen() || deviceService.CommandAdmissionOpen() {
		t.Fatal("drainExecution left an admission gate open")
	}
	entry, err := automationService.GetHistoryEntry(ctx, record.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Run == nil || entry.Run.Status == automations.RunRunning {
		t.Fatalf("drained Run = %#v, want a terminal outcome", entry.Run)
	}
}

// TestExecutionCleanupPrecedesEveryDependencyTeardown protects Core's error-exit
// cleanup order. Go runs deferred calls in reverse registration order, so the
// health supervisor and the request/reply transports are registered after the
// execution cleanup and would otherwise be withdrawn while an admitted
// Automation worker is still running. It pins executionCleanup.teardown: the
// first dependency teardown must drain the automation consumer, close both
// admission gates, and join the in-flight worker before it withdraws its
// resource, later teardowns must reuse the same once without waiting again, and
// dependency cancellation must happen exactly once. It fails if a dependency
// teardown runs before the worker is joined or if the cleanup body runs twice.
func TestExecutionCleanupPrecedesEveryDependencyTeardown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openOrderingDatabase(ctx, t)
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	deviceService := devices.NewService(
		devices.SQLiteStores(devices.NewSQLiteRepository(database, catalog)),
		nil, catalog, devices.Dependencies{},
	)
	seam := newBlockingAutomationDevices()
	automationService := automations.NewService(
		automations.NewSQLiteRepository(database, automations.AutomationDependencies{}),
		seam,
		automations.AutomationDependencies{Logger: slog.New(slog.DiscardHandler)},
	)
	record := createOrderingAutomation(ctx, t, automationService)
	if _, startErr := automationService.StartManualRun(ctx, record.ID); startErr != nil {
		t.Fatal(startErr)
	}
	<-seam.entered

	dependencyContext, cancelDependencies := context.WithCancel(context.Background())
	var (
		cancellations atomic.Int64
		withdrawnMu   sync.Mutex
		withdrawn     []string
	)
	cleanup := &executionCleanup{
		automationService:   automationService,
		automationConsumers: newAutomationConsumers(ctx, slog.New(slog.DiscardHandler)),
		deviceService:       deviceService,
		cancelDependencies: func() {
			cancellations.Add(1)
			cancelDependencies()
		},
		maintenance: &sync.WaitGroup{},
	}
	withdraw := func(name string) func() {
		return func() {
			withdrawnMu.Lock()
			defer withdrawnMu.Unlock()
			withdrawn = append(withdrawn, name)
		}
	}
	// Run registers these teardowns after the execution cleanup, so reverse defer
	// order runs them in exactly this order on an error exit.
	teardowns := []func(){
		cleanup.teardown(withdraw("health")),
		cleanup.teardown(withdraw("transports")),
		cleanup.teardown(withdraw("database")),
	}

	firstDone := make(chan struct{})
	go func() {
		teardowns[0]()
		close(firstDone)
	}()
	// The blocked worker keeps the first dependency teardown from withdrawing
	// health, which proves the cleanup runs inside that teardown rather than
	// before the teardown sequence starts.
	select {
	case <-firstDone:
		t.Fatal("dependency teardown withdrew health before the admitted worker drained")
	case <-time.After(50 * time.Millisecond):
	}
	if automationService.AdmissionOpen() || deviceService.CommandAdmissionOpen() {
		t.Fatal("execution cleanup did not close both admission gates inside the teardown")
	}
	withdrawnMu.Lock()
	withdrewEarly := len(withdrawn)
	withdrawnMu.Unlock()
	if withdrewEarly != 0 {
		t.Fatalf("withdrew %d dependencies before the admitted worker joined", withdrewEarly)
	}

	close(seam.release)
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("dependency teardown did not join the in-flight worker")
	}
	// The remaining teardowns run afterward and must reuse the same once instead
	// of joining or canceling a second time.
	teardowns[1]()
	teardowns[2]()
	withdrawnMu.Lock()
	order := append([]string(nil), withdrawn...)
	withdrawnMu.Unlock()
	if !slices.Equal(order, []string{"health", "transports", "database"}) {
		t.Fatalf("dependency teardown order = %v, want [health transports database]", order)
	}
	if got := cancellations.Load(); got != 1 {
		t.Fatalf("dependency cancellation count = %d, want 1", got)
	}
	if dependencyContext.Err() == nil {
		t.Fatal("execution cleanup left shared dependencies uncanceled")
	}
}

// TestRunStartupErrorTearsDownStartedDependencies protects the deferred
// error-exit cleanup on a real startup failure. Holding the HTTP address makes
// Run fail at the HTTP stage, after the automation consumer, the request/reply
// transports, and the health supervisor have started. The deferred cleanup must
// still close both admission gates, join admitted workers, and release the
// shared NATS connection instead of hanging on a double wait or leaking it. It
// fails if the error exit is unbounded, reports the wrong stage, or leaves Core
// connected to the broker.
func TestRunStartupErrorTearsDownStartedDependencies(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := startLifecycleNATSServer(t)
	held, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	defer func() { _ = held.Close() }()

	started := time.Now()
	runErr := Run(ctx, Config{HouseholdTimezone: "UTC",
		HTTPAddr:   held.Addr().String(),
		NATSURL:    server.ClientURL(),
		SQLitePath: filepath.Join(t.TempDir(), "hearth.db"),
	}, slog.New(slog.DiscardHandler))
	if stage := ErrorStage(runErr); stage != "http_listen" {
		t.Fatalf("startup error stage = %q, want http_listen (error: %v)", stage, runErr)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("error exit took %v, want a bounded teardown", elapsed)
	}
	// Core owned the only broker connection, so the deferred teardown must have
	// closed it as well.
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return server.NumClients() == 0, nil
	})
}

// openOrderingDatabase opens and migrates one throwaway SQLite database.
func openOrderingDatabase(ctx context.Context, t *testing.T) *sql.DB {
	t.Helper()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := platformdb.Migrate(ctx, database); migrateErr != nil {
		t.Fatal(migrateErr)
	}
	return database
}

// createOrderingAutomation creates one enabled Automation with a single
// Observation Trigger and one Step that the blocking seam can hold open.
func createOrderingAutomation(
	ctx context.Context,
	t *testing.T,
	service *automations.Service,
) automations.AutomationRecord {
	t.Helper()
	entityID, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.CreateAutomation(ctx, automations.AutomationDefinition{
		Name:    "Drain order",
		Enabled: true,
		Triggers: []automations.AutomationTrigger{{
			ID:   "trigger",
			Kind: automations.TriggerKindObservation,
			Observation: &automations.ObservationTrigger{
				EntityID:     entityID,
				Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
			},
		}},
		Steps: []automations.AutomationStep{{
			ID:            "step_0",
			EntityID:      entityID,
			OperationName: devices.OperationNameSet,
			Parameters:    devices.CommandParameters(`{"value":true}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}
