package zwavejs //nolint:testpackage // Shared fixtures support package-private module tests.

import (
	"context"
	"database/sql"
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
)

type runtimeService interface {
	ClaimAdapterRuntime(context.Context, adapters.ClaimAdapterRuntimeParams) error
	RecordAdapterHeartbeat(context.Context, adapters.AdapterHeartbeat) (adapters.HeartbeatResult, error)
	ReleaseAdapterRuntime(context.Context, string, adapters.RuntimeID) error
	ExpireAdapterLeases(context.Context, time.Time) error
	GetAdapter(context.Context, string) (adapters.AdapterInstance, error)
}
type testCoreService struct {
	*devices.Service
	runtimeService
	healthStore *adapterssqlite.AdapterRepository
}

func newCoreService(
	database *sql.DB,
	stores devices.Stores,
	sender devices.CommandSender,
	catalog *devices.TypeCatalog,
	dependencies devices.Dependencies,
) *testCoreService {
	runtimeRepository := adapterssqlite.NewAdapterRepository(database)
	runtimes := adapters.NewService(
		adapterssqlite.AdapterStores(runtimeRepository),
		adapters.Dependencies{Now: dependencies.Now, Logger: dependencies.Logger},
	)
	stores.AdapterReads = runtimes
	return &testCoreService{devices.NewService(stores, sender, catalog, dependencies), runtimes, runtimeRepository}
}

func startFixtureHealthDelivery(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	service *testCoreService,
) {
	t.Helper()
	ctx := context.Background()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	if err = adaptersnats.ProvisionHealthStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	durable, err := devicesnats.ProvisionAdapterHealthConsumer(ctx, js, adaptersnats.HealthStreamName)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.healthStore.QueueCurrentHealthSnapshots(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	consumer, err := devicesnats.StartAdapterHealthConsumer(ctx, durable, validator, service.Service, nil)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := adaptersnats.StartHealthRelay(js, service.healthStore, validator, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := relay.Drain(shutdown); err != nil {
			t.Error(err)
		}
		if err := consumer.Drain(shutdown); err != nil {
			t.Error(err)
		}
	})
}
