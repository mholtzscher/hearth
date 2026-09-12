package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

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
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

//nolint:gocognit // The required-dependency matrix is clearer as one table of subtests.
func TestRuntimeReadinessChecksEveryRequiredDependency(t *testing.T) {
	t.Parallel()
	t.Run("ready", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		if err := fixture.readiness.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("SQLite unavailable", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		if err := fixture.database.Close(); err != nil {
			t.Fatal(err)
		}
		if err := fixture.readiness.Check(context.Background()); err == nil {
			t.Fatal("readiness passed with closed SQLite")
		}
	})
	t.Run("NATS unavailable", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		fixture.connection.Close()
		if err := fixture.readiness.Check(context.Background()); err == nil {
			t.Fatal("readiness passed with closed NATS connection")
		}
	})
	t.Run("resources mismatched", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		stream, err := fixture.jetstream.Stream(context.Background(), devicesnats.ObservationStreamName)
		if err != nil {
			t.Fatal(err)
		}
		info, err := stream.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		config := info.Config
		config.MaxBytes = 42
		if _, updateErr := fixture.jetstream.UpdateStream(context.Background(), config); updateErr != nil {
			t.Fatal(updateErr)
		}
		if checkErr := fixture.readiness.Check(context.Background()); checkErr == nil {
			t.Fatal("readiness passed with mismatched stream")
		}
	})
	t.Run("consumer inactive", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		fixture.consumer.Stop()
		select {
		case <-fixture.consumer.Closed():
		case <-time.After(3 * time.Second):
			t.Fatal("consumer did not stop")
		}
		if err := fixture.readiness.Check(context.Background()); err == nil {
			t.Fatal("readiness passed with inactive consumer")
		}
	})
	t.Run("entity event resources mismatched", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		stream, err := fixture.jetstream.Stream(context.Background(), devicesnats.EntityEventStreamName)
		if err != nil {
			t.Fatal(err)
		}
		info, err := stream.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		config := info.Config
		config.MaxMsgSize = 1024
		if _, updateErr := fixture.jetstream.UpdateStream(context.Background(), config); updateErr != nil {
			t.Fatal(updateErr)
		}
		if checkErr := fixture.readiness.Check(context.Background()); checkErr == nil {
			t.Fatal("readiness passed with mismatched entity event stream")
		}
	})
	t.Run("entity event consumer inactive", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		fixture.entityEventConsumer.Stop()
		select {
		case <-fixture.entityEventConsumer.Closed():
		case <-time.After(3 * time.Second):
			t.Fatal("entity event consumer did not stop")
		}
		if err := fixture.readiness.Check(context.Background()); err == nil {
			t.Fatal("readiness passed with inactive entity event consumer")
		}
	})
	t.Run("device fact NATS unavailable", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		fixture.factConnection.Close()
		if err := fixture.readiness.Check(context.Background()); err == nil {
			t.Fatal("readiness passed with a closed device fact NATS connection")
		}
	})
	t.Run("dispatcher inactive", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		fixture.dispatcher.StopAdmission()
		if err := fixture.readiness.Check(context.Background()); err == nil {
			t.Fatal("readiness passed with an inactive device fact dispatcher")
		}
	})
}

// TestRuntimeReadinessDoesNotRequireASubscriber proves the fact path needs no
// subscriber, no fact stream and no received publication: an active dispatcher
// on both connected connections is the whole requirement.
func TestRuntimeReadinessDoesNotRequireASubscriber(t *testing.T) {
	t.Parallel()
	fixture := newReadinessFixture(t)
	if err := fixture.readiness.Check(context.Background()); err != nil {
		t.Fatalf("readiness without any fact subscriber = %v", err)
	}
}

type discardObservationProjector struct{}

func (discardObservationProjector) ProjectObservation(
	context.Context,
	string,
	devices.RuntimeID,
	devices.Observation,
	time.Time,
) (devices.ProjectionResult, error) {
	return devices.ProjectionResult{}, nil
}

type discardEntityEventRecorder struct{}

func (discardEntityEventRecorder) RecordEntityEvent(
	context.Context,
	string,
	devices.RuntimeID,
	devices.EntityEvent,
	time.Time,
) (devices.EntityEventRecordResult, error) {
	return devices.EntityEventRecordResult{}, nil
}

type readinessFixture struct {
	database            interface{ Close() error }
	connection          *natsgo.Conn
	factConnection      *natsgo.Conn
	jetstream           jetstream.JetStream
	consumer            *devicesnats.ObservationConsumer
	entityEventConsumer *devicesnats.EntityEventConsumer
	dispatcher          *devicesnats.DeviceFactDispatcher
	readiness           *RuntimeReadiness
}

func newReadinessFixture(t *testing.T) readinessFixture {
	t.Helper()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	connection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := devicesnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	entityEventResource, err := devicesnats.ProvisionEntityEventResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := devicesnats.StartObservationConsumer(
		ctx, durable, validator, discardObservationProjector{}, slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(consumer.Stop)
	entityEventConsumer, err := devicesnats.StartEntityEventConsumer(
		ctx, entityEventResource, validator, discardEntityEventRecorder{}, slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(entityEventConsumer.Stop)
	factConnection, dispatcher := startDeviceFactTransport(
		t, connection, server.ClientURL(), slog.New(slog.DiscardHandler),
	)
	return readinessFixture{
		database: database, connection: connection, factConnection: factConnection, jetstream: js,
		consumer: consumer, entityEventConsumer: entityEventConsumer, dispatcher: dispatcher,
		readiness: NewRuntimeReadiness(
			database, connection, factConnection, js, consumer, entityEventConsumer, dispatcher,
		),
	}
}
