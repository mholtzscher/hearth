package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

//nolint:gocognit // The recovery lifecycle is clearer as one end-to-end integration test.
func TestCoreStartupInterruptsActiveCommandsWithoutRedispatch(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger, recorder := withRecording(slog.LevelInfo)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if migrateErr := platformdb.Migrate(ctx, database); migrateErr != nil {
		t.Fatal(migrateErr)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devices.NewSQLiteRepository(database, catalog)
	service := devices.NewService(devices.SQLiteStores(repository), nil, catalog, devices.Dependencies{})
	runtimeID := devices.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if claimErr := service.ClaimAdapterRuntime(ctx, devices.ClaimAdapterRuntimeParams{
		AdapterID: "simulator", RuntimeID: runtimeID,
		SoftwareName: "hearth-simulator", SoftwareVersion: "0.1.0",
	}); claimErr != nil {
		t.Fatal(claimErr)
	}
	binding, err := service.Register(ctx, "simulator", runtimeID, devices.Registration{
		BindingKey: "recovery-light",
		Device:     devices.DeviceDescriptor{Name: "Recovery light", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{{
			Key: "power", ExternalID: "recovery.light", Name: "Power",
			TypeID:  devices.EntityTypePowerV1,
			Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	requested := recoveryCommandRecord(t, binding.Entities[0].EntityID, time.Now().UTC())
	accepted := recoveryCommandRecord(t, binding.Entities[0].EntityID, requested.RequestedAt.Add(time.Second))
	if _, createErr := repository.CreateCommand(ctx, requested); createErr != nil {
		t.Fatal(createErr)
	}
	if _, createErr := repository.CreateCommand(ctx, accepted); createErr != nil {
		t.Fatal(createErr)
	}
	if _, acceptErr := repository.MarkCommandAccepted(
		ctx,
		accepted.ID,
		accepted.RequestedAt.Add(time.Millisecond),
	); acceptErr != nil {
		t.Fatal(acceptErr)
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	observer, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observer.Close)
	var dispatches atomic.Int64
	subscription, err := observer.Subscribe("hearth.v1.adapter.simulator.runtime.*.command.>", func(*natsgo.Msg) {
		dispatches.Add(1)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Drain() })
	if flushErr := observer.Flush(); flushErr != nil {
		t.Fatal(flushErr)
	}

	httpAddress := unusedLoopbackAddress(t)
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{HouseholdTimezone: "UTC",
			HTTPAddr: httpAddress, NATSURL: server.ClientURL(), SQLitePath: databasePath,
		}, logger)
	}()

	observerDatabase, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observerDatabase.Close() })
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		select {
		case runErr := <-runErrors:
			if runErr != nil {
				return false, runErr
			}
			return false, context.Canceled
		default:
		}
		var interrupted, restarted int
		queryErr := observerDatabase.QueryRowContext(ctx, `
			SELECT count(*), coalesce(sum(CASE WHEN failure_code = 'core_restarted' THEN 1 ELSE 0 END), 0)
			FROM commands WHERE status = 'interrupted'`,
		).Scan(&interrupted, &restarted)
		return interrupted == 2 && restarted == 2, queryErr
	})
	// /healthz serves only after startup completes, so reaching it proves the
	// full startup window passed without redispatching persisted commands.
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)
	if got := dispatches.Load(); got != 0 {
		t.Fatalf("startup redispatched %d persisted commands", got)
	}

	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hearthd did not stop")
	}

	// Restart recovery emits one successful interruption stage and fabricates
	// no per-command outcome for the interrupted commands.
	records := recorder.snapshot()
	interrupted := false
	for _, record := range recordsWithEvent(records, "core.startup_stage_completed") {
		if value, ok := recordAttr(record, "stage"); ok && value.String() == "active_commands_interrupted" {
			requireRecordAttr(t, record, "component", "core")
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatal("missing core.startup_stage_completed for stage active_commands_interrupted")
	}
	if completed := recordsWithEvent(records, "command.completed"); len(completed) != 0 {
		t.Fatalf("startup recovery fabricated command outcomes: %#v", completed)
	}
}

// This test protects clean shutdown when the startup context is cancelled
// while the core NATS connection is blocked, and fails if cancellation
// surfaces as the unrelated NATS connection error.
func TestRunReturnsCancellationWhenStartupNATSConnectCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := blocker.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- connection
	}()
	httpAddress := unusedLoopbackAddress(t)
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{HouseholdTimezone: "UTC",
			HTTPAddr: httpAddress, NATSURL: "nats://" + blocker.Addr().String(), SQLitePath: databasePath,
		}, slog.New(slog.DiscardHandler))
	}()
	select {
	case connection := <-accepted:
		// Fail the pending NATS handshake after the cancellation below, so the
		// connect error deterministically follows the cancel instead of racing it.
		time.AfterFunc(500*time.Millisecond, func() { _ = connection.Close() })
		t.Cleanup(func() { _ = connection.Close() })
		stopCore()
	case runErr := <-runErrors:
		t.Fatalf("Run returned before startup NATS connect blocked: %v", runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not block in NATS connect")
	}
	select {
	case runErr := <-runErrors:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("cancelled startup NATS connect returned %v, want context.Canceled", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hearthd did not stop after startup cancellation")
	}
}

func recoveryCommandRecord(t *testing.T, entityID devices.EntityID, requestedAt time.Time) devices.CommandRecord {
	t.Helper()
	commandID, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	return devices.CommandRecord{
		ID: commandID, EntityID: entityID, AdapterID: "simulator",
		OperationName: devices.OperationNameSet, Parameters: devices.CommandParameters(`{"value":true}`),
		CorrelationID: correlationID, Status: devices.CommandStatusRequested,
		RequestedAt: requestedAt, DeadlineAt: requestedAt.Add(10 * time.Second),
	}
}

func unusedLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
