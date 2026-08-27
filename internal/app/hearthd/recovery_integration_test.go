package hearthd

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
)

func TestCoreStartupInterruptsActiveCommandsWithoutRedispatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devices.NewSQLiteRepository(database, catalog)
	service := devices.NewService(repository, nil, catalog, devices.Dependencies{})
	binding, err := service.Register(ctx, "simulator", devices.Registration{
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
	if _, err := repository.CreateCommand(ctx, requested); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateCommand(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkCommandAccepted(ctx, accepted.ID, accepted.RequestedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
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
	subscription, err := observer.Subscribe("hearth.v1.adapter.simulator.command.>", func(*natsgo.Msg) {
		dispatches.Add(1)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Drain() })
	if err := observer.Flush(); err != nil {
		t.Fatal(err)
	}

	httpAddress := unusedLoopbackAddress(t)
	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{
			HTTPAddr: httpAddress, NATSURL: server.ClientURL(), SQLitePath: databasePath,
		}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	}()

	observerDatabase, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observerDatabase.Close() })
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		select {
		case err := <-runErrors:
			if err != nil {
				return false, err
			}
			return false, context.Canceled
		default:
		}
		var interrupted, restarted int
		err := observerDatabase.QueryRowContext(ctx, `
			SELECT count(*), coalesce(sum(CASE WHEN failure_code = 'core_restarted' THEN 1 ELSE 0 END), 0)
			FROM commands WHERE status = 'interrupted'`,
		).Scan(&interrupted, &restarted)
		return interrupted == 2 && restarted == 2, err
	})
	time.Sleep(100 * time.Millisecond)
	if got := dispatches.Load(); got != 0 {
		t.Fatalf("startup redispatched %d persisted commands", got)
	}

	stopCore()
	select {
	case err := <-runErrors:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hearthd did not stop")
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
