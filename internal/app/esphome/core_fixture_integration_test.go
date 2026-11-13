//go:build integration

package esphome //nolint:testpackage // Assembled fixtures exercise the process entry point.

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// coreFixture assembles the real ownership, health, availability, observation, and Command boundaries. Like the
// Z-Wave process fixture, it uses real NATS/JetStream and migrated SQLite, not a fake SDK Session.
func coreFixture(t *testing.T) (string, *devices.Service) {
	t.Helper()
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, NoSigs: true, NoLog: true, JetStream: true, StoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	t.Cleanup(func() { server.Shutdown(); server.WaitForShutdown() })
	if !server.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS not ready")
	}
	connection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := devicesnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	repository := devicessqlite.NewDeviceRepository(database, catalog)
	runtimeRepository := adapterssqlite.NewAdapterRepository(database)
	runtime := adapters.NewService(
		adapterssqlite.AdapterStores(runtimeRepository),
		adapters.Dependencies{Logger: logger},
	)
	stores := devicessqlite.DeviceStores(repository)
	stores.AdapterReads = runtime
	service := devices.NewService(
		stores,
		devicesnats.NewCommandSender(connection, validator),
		catalog,
		devices.Dependencies{Logger: logger},
	)
	startHealth(t, js, runtimeRepository, validator, service)
	sessions, err := adaptersnats.StartSessionServer(connection, validator, runtime, runtime, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Close(context.Background()) })
	registrations, err := devicesnats.StartRegistrationServer(connection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Drain() })
	mappings, err := devicesnats.StartOwnedMappingsServer(connection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mappings.Drain() })
	availability, err := devicesnats.StartEntityAvailabilityServer(connection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = availability.Drain() })
	consumer, err := devicesnats.StartObservationConsumer(ctx, observations, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(consumer.Stop)
	return server.ClientURL(), service
}

func startHealth(
	t *testing.T,
	js jetstream.JetStream,
	repository *adapterssqlite.AdapterRepository,
	validator *contractsv1.Validator,
	service *devices.Service,
) {
	t.Helper()
	ctx := context.Background()
	if err := adaptersnats.ProvisionHealthStream(ctx, js); err != nil {
		t.Fatal(err)
	}
	durable, err := devicesnats.ProvisionAdapterHealthConsumer(ctx, js, adaptersnats.HealthStreamName)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.QueueCurrentHealthSnapshots(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	consumer, err := devicesnats.StartAdapterHealthConsumer(ctx, durable, validator, service, nil)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := adaptersnats.StartHealthRelay(js, repository, validator, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if drainErr := relay.Drain(shutdown); drainErr != nil {
			t.Error(drainErr)
			_ = relay.Stop(context.Background())
		}
		if drainErr := consumer.Drain(shutdown); drainErr != nil {
			t.Error(drainErr)
			consumer.Stop()
			_ = consumer.Drain(context.Background())
		}
	})
}
