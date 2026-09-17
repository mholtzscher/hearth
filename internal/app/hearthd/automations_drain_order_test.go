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
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// blockingAutomationDevices holds a Command in flight while tests inspect shutdown.
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

func (seam *blockingAutomationDevices) ValidateConditionEntity(context.Context, devices.EntityID) error {
	return nil
}

func (seam *blockingAutomationDevices) GetEntityStateSnapshot(
	context.Context, []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	return devices.EntityStateSnapshot{}, nil
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

// A blocked Automation Run must keep dependencies alive without leaving either
// admission gate open. Releasing it must let shutdown finish with a terminal Run.
func TestCoreShutdownClosesAdmissionGatesBeforeJoiningWorkers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	automationService, deviceService, seam, record, run := startBlockedAutomationRun(ctx, t)
	<-seam.entered

	dependencyContext, cancelDependencies := context.WithCancel(context.Background())
	shutdown := &coreShutdown{
		runContext:          ctx,
		logger:              slog.New(slog.DiscardHandler),
		automationService:   automationService,
		automationConsumers: newAutomationConsumers(ctx, slog.New(slog.DiscardHandler)),
		deviceService:       deviceService,
		cancelDependencies:  cancelDependencies,
	}
	shutdownErrors := make(chan error, 1)
	go func() { shutdownErrors <- shutdown.run() }()

	waitForClosedAdmissionGates(t, automationService, deviceService)
	// The blocked worker still needs its dependencies, so the cancel must not
	// have run yet, and a new Command must already be refused.
	if dependencyContext.Err() != nil {
		t.Fatal("shutdown canceled shared dependencies before joining the admitted worker")
	}
	if _, commandErr := deviceService.ExecuteCommand(ctx, devices.CommandInput{}); !errors.Is(
		commandErr, devices.ErrCommandUnavailable,
	) {
		t.Fatalf("command admission during automation drain = %v, want ErrCommandUnavailable", commandErr)
	}
	select {
	case shutdownErr := <-shutdownErrors:
		t.Fatalf("shutdown returned while the admitted worker was blocked: %v", shutdownErr)
	default:
	}

	close(seam.release)
	select {
	case shutdownErr := <-shutdownErrors:
		if shutdownErr != nil {
			t.Fatalf("shutdown error = %v", shutdownErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join the admitted worker")
	}
	if dependencyContext.Err() == nil {
		t.Fatal("shutdown left shared dependencies uncanceled")
	}
	entry, err := automationService.GetHistoryEntry(ctx, record.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Run == nil || entry.Run.Status == automations.RunRunning {
		t.Fatalf("drained Run = %#v, want a terminal outcome", entry.Run)
	}
}

// Transports must outlive admitted workers and be withdrawn exactly once.
func TestCoreShutdownDrainsWorkersBeforeWithdrawingResources(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	automationService, deviceService, seam, record, run := startBlockedAutomationRun(ctx, t)
	<-seam.entered

	var (
		drainCount  atomic.Int64
		cancelCount atomic.Int64
	)
	shutdown := &coreShutdown{
		runContext:          ctx,
		logger:              slog.New(slog.DiscardHandler),
		automationService:   automationService,
		automationConsumers: newAutomationConsumers(ctx, slog.New(slog.DiscardHandler)),
		deviceService:       deviceService,
		cancelDependencies:  func() { cancelCount.Add(1) },
		transports: []registeredDrain{{
			stage: "drain_recording_server",
			drain: drainFunc(func() error { drainCount.Add(1); return nil }),
		}},
	}
	shutdownErrors := make(chan error, 1)
	go func() { shutdownErrors <- shutdown.run() }()

	waitForClosedAdmissionGates(t, automationService, deviceService)
	if got := drainCount.Load(); got != 0 {
		t.Fatalf("transport withdrawn %d times before the admitted worker joined", got)
	}
	close(seam.release)
	select {
	case shutdownErr := <-shutdownErrors:
		if shutdownErr != nil {
			t.Fatalf("shutdown error = %v", shutdownErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join the admitted worker")
	}
	if got := drainCount.Load(); got != 1 {
		t.Fatalf("transport withdrawal count = %d, want 1", got)
	}
	if got := cancelCount.Load(); got != 1 {
		t.Fatalf("dependency cancellation count = %d, want 1", got)
	}
	entry, err := automationService.GetHistoryEntry(ctx, record.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Run == nil || entry.Run.Status == automations.RunRunning {
		t.Fatalf("drained Run = %#v, want a terminal outcome", entry.Run)
	}
}

// A database-only startup must clean up without touching uninitialized resources.
func TestCoreShutdownSkipsResourcesThatNeverStarted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openOrderingDatabase(t)
	shutdown := &coreShutdown{
		runContext: ctx, logger: slog.New(slog.DiscardHandler), database: database,
	}
	if err := shutdown.run(); err != nil {
		t.Fatalf("partial shutdown error = %v, want nil", err)
	}
	if pingErr := database.Ping(); pingErr == nil {
		t.Fatal("partial shutdown left the database open")
	}
}

// A failed transport drain must be logged and returned without preventing later
// transports and SQLite from closing.
func TestCoreShutdownContinuesAfterAnIndividualCleanupFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openOrderingDatabase(t)
	logger, recorder := withRecording(slog.LevelWarn)
	var withdrawn []string
	shutdown := &coreShutdown{
		runContext: ctx, logger: logger, database: database,
		// Transports drain in reverse start order, so the failing entry must
		// come last in this slice to be withdrawn first.
		transports: []registeredDrain{
			{stage: "drain_second_server", drain: drainFunc(func() error {
				withdrawn = append(withdrawn, "second")
				return nil
			})},
			{stage: "drain_first_server", drain: drainFunc(func() error {
				withdrawn = append(withdrawn, "first")
				return errors.New("first drain failed")
			})},
		},
	}
	err := shutdown.run()
	if err == nil {
		t.Fatal("shutdown reported no error after a failed transport drain")
	}
	if !slices.Equal(withdrawn, []string{"first", "second"}) {
		t.Fatalf("withdrawal order after a failure = %v, want [first second]", withdrawn)
	}
	if pingErr := database.Ping(); pingErr == nil {
		t.Fatal("shutdown stopped before closing the database")
	}
	failures := recordsWithEvent(recorder.snapshot(), "process.cleanup_failed")
	if len(failures) != 1 {
		t.Fatalf("process.cleanup_failed records = %d, want 1", len(failures))
	}
	requireRecordAttr(t, failures[0], "stage", "drain_first_server")
}

// drainFunc lets tests record transport drains.
type drainFunc func() error

func (fn drainFunc) Drain() error { return fn() }

// startBlockedAutomationRun starts a database-backed Run blocked on its first Command.
func startBlockedAutomationRun(
	ctx context.Context,
	t *testing.T,
) (*automations.Service, *devices.Service, *blockingAutomationDevices, automations.Record, automations.Run) {
	t.Helper()
	database := openOrderingDatabase(t)
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	deviceService := devices.NewService(
		devicessqlite.DeviceStores(devicessqlite.NewDeviceRepository(database, catalog)),
		nil, catalog, devices.Dependencies{},
	)
	seam := newBlockingAutomationDevices()
	automationService := automations.NewService(
		automationssqlite.NewAutomationRepository(database, automations.Dependencies{}),
		seam,
		automations.Dependencies{Logger: slog.New(slog.DiscardHandler)},
	)
	record := createOrderingAutomation(ctx, t, automationService)
	run, err := automationService.StartManualRun(ctx, automations.ManualRunInput{AutomationID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	return automationService, deviceService, seam, record, run
}

// waitForClosedAdmissionGates observes shutdown while the admitted worker is blocked.
func waitForClosedAdmissionGates(
	t *testing.T,
	automationService *automations.Service,
	deviceService *devices.Service,
) {
	t.Helper()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return !automationService.AdmissionOpen() && !deviceService.CommandAdmissionOpen(), nil
	})
}

// An occupied HTTP address forces a late startup failure. Run must preserve
// the failing stage and release its NATS connection without hanging.
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

// openOrderingDatabase opens one throwaway SQLite database from the shared migrated template.
func openOrderingDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	return database
}

// createOrderingAutomation creates a single-Step Automation for the blocking seam.
func createOrderingAutomation(
	ctx context.Context,
	t *testing.T,
	service *automations.Service,
) automations.Record {
	t.Helper()
	entityID, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.CreateAutomation(ctx, automations.Definition{
		Name:    "Drain order",
		Enabled: true,
		Triggers: []automations.Trigger{{
			ID:   "trigger",
			Kind: automations.TriggerKindObservation,
			Observation: &automations.ObservationTrigger{
				EntityID:     entityID,
				Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
			},
		}},
		Steps: []automations.Step{{
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
