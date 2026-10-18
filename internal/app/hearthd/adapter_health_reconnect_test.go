package hearthd //nolint:testpackage // Exercise actual Core delivery recovery across a broker restart.

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

//nolint:gocognit // One broker restart timeline checks retained outbox delivery without snapshot resynchronization.
func TestCoreReconnectDeliversPendingHealthWithoutQueuingSnapshots(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "reconnect.db")
	database := dbtest.OpenMigrated(t, path)
	repository := adapterssqlite.NewAdapterRepository(database)
	service := adapters.NewService(adapterssqlite.AdapterStores(repository), adapters.Dependencies{})
	runtimeID := adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if err := service.ClaimAdapterRuntime(
		ctx,
		adapters.ClaimAdapterRuntimeParams{
			AdapterID:       "simulator",
			RuntimeID:       runtimeID,
			SoftwareName:    "hearth-simulator",
			SoftwareVersion: "1",
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecordAdapterHeartbeat(
		ctx,
		adapters.AdapterHeartbeat{
			AdapterID:        "simulator",
			RuntimeID:        runtimeID,
			ExternalStatus:   adapters.AdapterHealthHealthy,
			SourceObservedAt: time.Now().UTC(),
		},
	); err != nil {
		t.Fatal(err)
	}
	heartbeats := startReconnectHealthHeartbeats(t, service, runtimeID)
	store := t.TempDir()
	broker := startReconnectHealthBroker(t, -1, store)
	port := broker.Addr().(*net.TCPAddr).Port
	url := broker.ClientURL()
	t.Cleanup(func() { broker.Shutdown(); broker.WaitForShutdown() })
	connection, err := natsgo.Connect(url, natsgo.MaxReconnects(-1), natsgo.ReconnectWait(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	address, options := reserveLoopbackListener(t)
	runContext, stop := context.WithCancel(ctx)
	finished := make(chan error, 1)
	completed := make(chan struct{})
	var coreErr error
	config := Config{
		HouseholdTimezone: "UTC",
		HTTPAddr:          address,
		NATSURL:           url,
		SQLitePath:        path,
		Agent:             requiredAgentConfig(t),
	}
	go func() {
		coreErr = runWithOptions(runContext, config, slog.New(slog.DiscardHandler), options)
		finished <- coreErr
		close(completed)
	}()
	t.Cleanup(func() {
		stop()
		select {
		case <-completed:
		case <-time.After(10 * time.Second):
			t.Error("Core did not join after reconnect")
			<-completed
		}
		if coreErr != nil {
			t.Errorf("Core shutdown: %v", coreErr)
		}
	})
	t.Cleanup(func() {
		if !broker.Running() {
			broker = startReconnectHealthBroker(t, port, store)
		}
	})
	waitForCoreHealthz(ctx, t, address, finished)
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		var count int
		queryErr := database.QueryRow("SELECT count(*) FROM adapter_health_outbox").Scan(&count)
		return count == 0, queryErr
	})
	var beforeOrder int64
	if err = database.QueryRow("SELECT seq FROM sqlite_sequence WHERE name = 'adapter_health_outbox'").
		Scan(&beforeOrder); err != nil {
		t.Fatal(err)
	}
	if err = heartbeats.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	broker.Shutdown()
	broker.WaitForShutdown()
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		return connection.IsReconnecting(), nil
	})
	// Release while NATS is down, so recovery must deliver the retained outbox
	// notification without depending on a future heartbeat or lease expiry.
	if err = service.ReleaseAdapterRuntime(ctx, "simulator", runtimeID); err != nil {
		t.Fatal(err)
	}
	var pendingID, pendingBody string
	if err = database.QueryRow("SELECT message_id, snapshot_json FROM adapter_health_outbox WHERE revision = 3").
		Scan(&pendingID, &pendingBody); err != nil {
		t.Fatal(err)
	}
	broker = startReconnectHealthBroker(t, port, store)
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		var pending int
		if queryErr := database.QueryRow("SELECT count(*) FROM adapter_health_outbox").Scan(&pending); queryErr != nil {
			return false, queryErr
		}
		if !connection.IsConnected() {
			return false, nil
		}
		durable, consumerErr := js.Consumer(ctx, adaptersnats.HealthStreamName, devicesnats.AdapterHealthConsumerName)
		if consumerErr != nil {
			return false, consumerErr
		}
		info, infoErr := durable.Info(ctx)
		return pending == 0 && info != nil && info.NumPending == 0 && info.NumAckPending == 0, infoErr
	})
	var revision, history int64
	var projectedRevision int64
	var afterBody, appliedID string
	var afterOrder int64
	if err = database.QueryRow("SELECT revision, snapshot_json FROM adapter_health_heads WHERE adapter_id = 'simulator'").
		Scan(&revision, &afterBody); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow("SELECT count(*) FROM health_transitions WHERE resource_kind = 'adapter'").
		Scan(&history); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow("SELECT revision, last_message_id FROM device_adapter_health_projection WHERE adapter_id = 'simulator'").
		Scan(&projectedRevision, &appliedID); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow("SELECT seq FROM sqlite_sequence WHERE name = 'adapter_health_outbox'").
		Scan(&afterOrder); err != nil {
		t.Fatal(err)
	}
	if revision != 3 || projectedRevision != 3 || history != 3 || afterBody != pendingBody || appliedID != pendingID {
		t.Fatalf(
			"reconnect evidence: revision=%d projected=%d history=%d body_equal=%t original_notification=%t",
			revision,
			projectedRevision,
			history,
			afterBody == pendingBody,
			appliedID == pendingID,
		)
	}
	if afterOrder != beforeOrder+1 {
		t.Fatalf("reconnect queued extra snapshots: enqueue order advanced from %d to %d, want one health transition",
			beforeOrder, afterOrder)
	}
}

func startReconnectHealthHeartbeats(
	t *testing.T,
	service *adapters.Service,
	runtimeID adapters.RuntimeID,
) *lifecycle.WorkerHandle {
	t.Helper()
	heartbeats := lifecycle.StartWorker(context.Background(), func(workerContext context.Context) error {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workerContext.Done():
				return nil
			case at := <-ticker.C:
				if _, heartbeatErr := service.RecordAdapterHeartbeat(
					workerContext,
					adapters.AdapterHeartbeat{
						AdapterID:        "simulator",
						RuntimeID:        runtimeID,
						ExternalStatus:   adapters.AdapterHealthHealthy,
						SourceObservedAt: at.UTC(),
					},
				); heartbeatErr != nil {
					if errors.Is(heartbeatErr, context.Canceled) && workerContext.Err() != nil {
						return nil
					}
					return heartbeatErr
				}
			}
		}
	})
	t.Cleanup(func() {
		if heartbeatErr := heartbeats.Stop(context.Background()); heartbeatErr != nil {
			t.Errorf("heartbeat worker: %v", heartbeatErr)
		}
	})
	return heartbeats
}

func startReconnectHealthBroker(t *testing.T, port int, store string) *natsserver.Server {
	t.Helper()
	broker, err := natsserver.NewServer(
		&natsserver.Options{Host: "127.0.0.1", Port: port, JetStream: true, StoreDir: store, NoSigs: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	go broker.Start()
	if !broker.ReadyForConnections(10 * time.Second) {
		broker.Shutdown()
		broker.WaitForShutdown()
		t.Fatal("health reconnect broker did not start")
	}
	return broker
}
