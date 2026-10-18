package hearthd //nolint:testpackage // Shared fixtures support package-private module tests.

import (
	"context"
	"database/sql"
	"sync"
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
)

func adapterServiceForTest(database *sql.DB) *adapters.Service {
	repository := adapterssqlite.NewAdapterRepository(database)
	return adapters.NewService(adapterssqlite.AdapterStores(repository), adapters.Dependencies{})
}
func testDeviceStores(repository *devicessqlite.DeviceRepository, database *sql.DB) devices.Stores {
	stores := devicessqlite.DeviceStores(repository)
	stores.AdapterReads = adapterServiceForTest(database)
	return stores
}

func startFixtureHealthDelivery(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	service *devices.Service,
	database *sql.DB,
) func() {
	t.Helper()
	runtimeRepository := adapterssqlite.NewAdapterRepository(database)
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
	if err = runtimeRepository.QueueCurrentHealthSnapshots(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	consumer, err := devicesnats.StartAdapterHealthConsumer(ctx, durable, validator, service, nil)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := adaptersnats.StartHealthRelay(js, runtimeRepository, validator, nil)
	if err != nil {
		t.Fatal(err)
	}
	var closeOnce sync.Once
	closeDelivery := func() {
		closeOnce.Do(func() {
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
	t.Cleanup(closeDelivery)
	return closeDelivery
}
