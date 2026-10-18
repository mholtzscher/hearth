package hearthd //nolint:testpackage // Shared real-boundary fixture for health delivery.

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

type healthDeliveryFixture struct {
	database          *sql.DB
	adapterRepository *adapterssqlite.AdapterRepository
	deviceService     *devices.Service
	js                jetstream.JetStream
	durable           jetstream.Consumer
	validator         *contractsv1.Validator
	relay             *adaptersnats.HealthRelay
	consumer          *platformnats.Consumer
	entityID          devices.EntityID
}

func newHealthDeliveryFixture(t *testing.T) *healthDeliveryFixture {
	t.Helper()
	ctx := t.Context()
	f := &healthDeliveryFixture{database: dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "delivery.db"))}
	f.adapterRepository = adapterssqlite.NewAdapterRepository(f.database)
	adapterService := adapters.NewService(adapterssqlite.AdapterStores(f.adapterRepository), adapters.Dependencies{})
	runtimeID := adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
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
			SourceObservedAt: time.Now().UTC(),
		},
	); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	deviceRepository := devicessqlite.NewDeviceRepository(f.database, catalog)
	stores := devicessqlite.DeviceStores(deviceRepository)
	stores.AdapterReads = adapterService
	f.deviceService = devices.NewService(stores, nil, catalog, devices.Dependencies{})
	binding, err := f.deviceService.Register(
		ctx,
		"simulator",
		runtimeID,
		devices.Registration{
			BindingKey: "delivery",
			Device:     devices.DeviceDescriptor{Name: "Delivery", Kind: devices.DeviceKindLight},
			Entities: []devices.EntityDescriptor{
				{
					Key:        "power",
					ExternalID: "delivery.power",
					Name:       "Power",
					TypeID:     devices.EntityTypePowerV1,
					Support:    devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.entityID = binding.Entities[0].EntityID
	reportID, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.deviceService.ReportEntityAvailability(
		ctx,
		"avl_"+string(reportID)[4:],
		"simulator",
		runtimeID,
		[]devices.EntityAvailabilityReport{
			{EntityID: f.entityID, Status: devices.EntityAvailabilityAvailable, SourceObservedAt: time.Now().UTC()},
		},
	); err != nil {
		t.Fatal(err)
	}
	server := natstest.StartServer(t)
	connection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	f.js, err = jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	f.validator, err = contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if err = adaptersnats.ProvisionHealthStream(ctx, f.js); err != nil {
		t.Fatal(err)
	}
	f.durable, err = devicesnats.ProvisionAdapterHealthConsumer(ctx, f.js, adaptersnats.HealthStreamName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f.relay != nil {
			_ = f.relay.Stop(context.Background())
		}
		if f.consumer != nil {
			_ = f.consumer.Drain(context.Background())
		}
	})
	return f
}

func (f *healthDeliveryFixture) start(t *testing.T) {
	t.Helper()
	var err error
	f.consumer, err = devicesnats.StartAdapterHealthConsumer(
		context.Background(),
		f.durable,
		f.validator,
		f.deviceService,
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.startRelay(t)
}

func (f *healthDeliveryFixture) startRelay(t *testing.T) {
	t.Helper()
	var err error
	f.relay, err = adaptersnats.StartHealthRelay(f.js, f.adapterRepository, f.validator, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
}

func (f *healthDeliveryFixture) waitRevision(t *testing.T, revision int64) {
	t.Helper()
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		var current int64
		err := f.database.QueryRow("SELECT revision FROM device_adapter_health_projection WHERE adapter_id = 'simulator'").
			Scan(&current)
		if err == sql.ErrNoRows {
			return false, nil
		}
		return current == revision, err
	})
}

func (f *healthDeliveryFixture) waitDrained(t *testing.T) {
	t.Helper()
	waitForMatrixCondition(t, 10*time.Second, func() (bool, error) {
		var count int
		if err := f.database.QueryRow("SELECT count(*) FROM adapter_health_outbox").Scan(&count); err != nil {
			return false, err
		}
		info, err := f.durable.Info(t.Context())
		return count == 0 && info != nil && info.NumAckPending == 0 && info.NumPending == 0, err
	})
}

func (f *healthDeliveryFixture) historyCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := f.database.QueryRow("SELECT count(*) FROM health_transitions WHERE resource_kind = 'entity' AND entity_id = ?", f.entityID).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
