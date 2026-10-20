//go:build integration

package hearthd //nolint:testpackage // Exercise actual application assembly and supervisor wiring.

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

// Deleting only the health durable must fail public readiness without stopping
// canonical lease expiry. This fails if assembly passes derived checks to expiry.
func TestCoreHealthDeliveryFailureDoesNotSuspendLeaseExpiry(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "health.db")
	database := dbtest.OpenMigrated(t, path)
	adapterRepository := adapterssqlite.NewAdapterRepository(database)
	old := time.Now().UTC().Add(-time.Minute)
	runtimeID := adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	adapterService := adapters.NewService(
		adapterssqlite.AdapterStores(adapterRepository),
		adapters.Dependencies{Now: func() time.Time { return old }},
	)
	if err := adapterService.ClaimAdapterRuntime(
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
	if _, err := adapterService.RecordAdapterHeartbeat(
		ctx,
		adapters.AdapterHeartbeat{
			AdapterID:        "simulator",
			RuntimeID:        runtimeID,
			ExternalStatus:   adapters.AdapterHealthHealthy,
			SourceObservedAt: old,
		},
	); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	deviceRepository := devicessqlite.NewDeviceRepository(database, catalog)
	stores := devicessqlite.DeviceStores(deviceRepository)
	stores.AdapterReads = adapterService
	deviceService := devices.NewService(stores, nil, catalog, devices.Dependencies{})
	binding, err := deviceService.Register(ctx, "simulator", runtimeID, devices.Registration{
		BindingKey: "health-test",
		Device:     devices.DeviceDescriptor{Name: "Health test", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{
			{
				Key:        "power",
				ExternalID: "health-test.power",
				Name:       "Power",
				TypeID:     devices.EntityTypePowerV1,
				Support:    devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := natstest.StartServer(t)
	connection, err := natsgo.Connect(server.ClientURL())
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
		NATSURL:           server.ClientURL(),
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
			t.Error("Core shutdown did not join")
			<-completed
		}
		if coreErr != nil {
			t.Errorf("Core shutdown: %v", coreErr)
		}
	})
	waitForCoreHealthz(ctx, t, address, finished)
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) {
		var count int
		queryErr := database.QueryRowContext(ctx, "SELECT count(*) FROM device_adapter_health_projection WHERE adapter_id = 'simulator'").
			Scan(&count)
		return count == 1, queryErr
	})
	if err = js.DeleteConsumer(ctx, adaptersnats.HealthStreamName, devicesnats.AdapterHealthConsumerName); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/readyz", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readiness after health durable loss = %d", response.StatusCode)
	}
	waitForMatrixCondition(t, 25*time.Second, func() (bool, error) {
		instance, readErr := adapterService.GetAdapter(ctx, "simulator")
		return readErr == nil && instance.Health.Status == adapters.AdapterHealthUnhealthy &&
			instance.Health.Reason != nil &&
			instance.Health.Reason.Code == "hearth.heartbeat_expired", readErr
	})
	id, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlation, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	command, err := deviceRepository.CreateCommand(
		ctx,
		devices.CommandRecord{
			ID:            id,
			EntityID:      binding.Entities[0].EntityID,
			CorrelationID: correlation,
			OperationName: devices.OperationNameSet,
			Parameters:    devices.CommandParameters(`{"power":true}`),
			Status:        devices.CommandStatusRequested,
			RequestedAt:   now,
			DeadlineAt:    now.Add(time.Second),
		},
	)
	if err != nil || command.Status != devices.CommandStatusAdapterUnhealthy || command.RuntimeID != nil {
		t.Fatalf("expired canonical admission = %#v, %v", command, err)
	}
}

func TestHealthRelayRestartAndFreshSynchronizationApplyOneSemanticRevision(t *testing.T) {
	t.Parallel()
	f := newHealthDeliveryFixture(t)
	if _, err := f.database.Exec(
		`CREATE TRIGGER fail_health_delete BEFORE DELETE ON adapter_health_outbox BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	f.waitRevision(t, 1)
	waitForMatrixCondition(t, 5*time.Second, func() (bool, error) { return f.relay.Fault() != nil, nil })
	var pending int
	if err := f.database.QueryRow("SELECT count(*) FROM adapter_health_outbox").
		Scan(&pending); err != nil ||
		pending != 2 {
		t.Fatalf("unconfirmed publication work = %d, %v", pending, err)
	}
	_ = f.relay.Stop(context.Background())
	if _, err := f.database.Exec("DROP TRIGGER fail_health_delete"); err != nil {
		t.Fatal(err)
	}
	f.startRelay(t)
	f.waitRevision(t, 2)
	f.waitDrained(t)
	before := f.historyCount(t)
	view, err := f.deviceService.GetEntity(t.Context(), f.entityID)
	if err != nil || view.Availability.Status != devices.EntityAvailabilityAvailable {
		t.Fatalf("recovered projection = %#v, %v", view.Availability, err)
	}
	if err = f.adapterRepository.QueueCurrentHealthSnapshots(t.Context(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	f.relay.NotifyPendingHealthUpdates()
	f.waitDrained(t)
	if f.historyCount(t) != before {
		t.Fatal("fresh synchronization identity duplicated Entity history")
	}
	var revision int64
	if err = f.database.QueryRow("SELECT revision FROM adapter_health_heads WHERE adapter_id = 'simulator'").
		Scan(&revision); err != nil ||
		revision != 2 {
		t.Fatalf("synchronization changed authoritative revision = %d, %v", revision, err)
	}
}

func TestHealthConsumerRedeliveryAfterSQLiteFailureCommitsEffectiveHistoryOnce(t *testing.T) {
	t.Parallel()
	f := newHealthDeliveryFixture(t)
	if _, err := f.database.Exec(
		`CREATE TRIGGER fail_health_projection BEFORE UPDATE ON entity_availability_effective BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	); err != nil {
		t.Fatal(err)
	}
	before := f.historyCount(t)
	f.start(t)
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		info, err := f.durable.Info(t.Context())
		return info != nil && info.NumRedelivered > 0, err
	})
	var projected int
	if err := f.database.QueryRow("SELECT count(*) FROM device_adapter_health_projection").
		Scan(&projected); err != nil ||
		projected != 0 {
		t.Fatalf("failed consumer transaction advanced projection = %d, %v", projected, err)
	}
	if f.historyCount(t) != before {
		t.Fatal("failed consumer transaction committed history")
	}
	if _, err := f.database.Exec("DROP TRIGGER fail_health_projection"); err != nil {
		t.Fatal(err)
	}
	f.waitRevision(t, 2)
	f.waitDrained(t)
	if f.historyCount(t) != before+2 {
		t.Fatalf("replayed revisions committed %d transitions, want two effective changes", f.historyCount(t)-before)
	}
	view, err := f.deviceService.GetEntity(t.Context(), f.entityID)
	if err != nil || view.Availability.Status != devices.EntityAvailabilityAvailable {
		t.Fatalf("replayed effective projection = %#v, %v", view.Availability, err)
	}
}
