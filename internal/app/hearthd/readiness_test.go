package hearthd

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestRuntimeReadinessChecksEveryRequiredDependency(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		fixture := newReadinessFixture(t)
		if err := fixture.readiness.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("SQLite unavailable", func(t *testing.T) {
		fixture := newReadinessFixture(t)
		if err := fixture.database.Close(); err != nil {
			t.Fatal(err)
		}
		if err := fixture.readiness.Check(context.Background()); err == nil {
			t.Fatal("readiness passed with closed SQLite")
		}
	})
	t.Run("NATS unavailable", func(t *testing.T) {
		fixture := newReadinessFixture(t)
		fixture.connection.Close()
		if err := fixture.readiness.Check(context.Background()); err == nil {
			t.Fatal("readiness passed with closed NATS connection")
		}
	})
	t.Run("resources mismatched", func(t *testing.T) {
		fixture := newReadinessFixture(t)
		stream, err := fixture.jetstream.Stream(context.Background(), platformnats.ObservationStreamName)
		if err != nil {
			t.Fatal(err)
		}
		info, err := stream.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		config := info.Config
		config.MaxBytes = 42
		if _, err := fixture.jetstream.UpdateStream(context.Background(), config); err != nil {
			t.Fatal(err)
		}
		if err := fixture.readiness.Check(context.Background()); err == nil {
			t.Fatal("readiness passed with mismatched stream")
		}
	})
	t.Run("consumer inactive", func(t *testing.T) {
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
}

type readinessFixture struct {
	database   interface{ Close() error }
	connection *natsgo.Conn
	jetstream  jetstream.JetStream
	consumer   *platformnats.ObservationConsumer
	readiness  *RuntimeReadiness
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
	durable, err := platformnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := platformnats.StartObservationConsumer(ctx, durable, validator, func(context.Context, platformnats.ObservationDelivery) error {
		return nil
	}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(consumer.Stop)
	return readinessFixture{
		database: database, connection: connection, jetstream: js, consumer: consumer,
		readiness: NewRuntimeReadiness(database, connection, js, consumer),
	}
}
