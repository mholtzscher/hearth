package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
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
}

// This test protects fixed readiness reason codes and fails if a dependency
// outage loses its code, changes its preserved text, or breaks the error chain.
func TestRuntimeReadinessCarriesReasonCodes(t *testing.T) {
	t.Parallel()
	t.Run("SQLite unavailable", func(t *testing.T) {
		t.Parallel()
		checkErr := closedDatabaseReadinessCheck(t)
		if checkErr == nil {
			t.Fatal("readiness passed with closed SQLite")
		}
		var coded *readinessCheckError
		if !errors.As(checkErr, &coded) || coded.reasonCode != "sqlite_unavailable" {
			t.Fatalf("readiness error = %#v, want sqlite_unavailable", checkErr)
		}
		if !strings.HasPrefix(checkErr.Error(), "SQLite is unavailable") {
			t.Fatalf("readiness error text = %q, want SQLite prefix", checkErr.Error())
		}
		requireReadinessReason(t, checkErr, "sqlite_unavailable")
	})
	t.Run("NATS disconnected", func(t *testing.T) {
		t.Parallel()
		fixture := newReadinessFixture(t)
		fixture.connection.Close()
		if checkErr := fixture.readiness.Check(context.Background()); checkErr == nil {
			t.Fatal("readiness passed with closed NATS connection")
		} else {
			requireReadinessReason(t, checkErr, "nats_disconnected")
		}
	})
	t.Run("JetStream mismatch", func(t *testing.T) {
		t.Parallel()
		requireReadinessReason(t, mismatchedStreamReadinessCheck(t), "jetstream_unavailable")
	})
	t.Run("consumer inactive", func(t *testing.T) {
		t.Parallel()
		requireReadinessReason(t, inactiveConsumerReadinessCheck(t), "observation_consumer_inactive")
	})
	t.Run("uninitialized", func(t *testing.T) {
		t.Parallel()
		var uninitialized *RuntimeReadiness
		requireReadinessReason(t, uninitialized.Check(context.Background()), "readiness_check_failed")
	})
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		requireReadinessReason(t, errors.New("token=secret"), "readiness_check_failed")
	})
}

func requireReadinessReason(t *testing.T, checkErr error, want string) {
	t.Helper()
	if checkErr == nil {
		t.Fatalf("readiness passed, want reason %q", want)
	}
	if reason := readinessFailureReason(checkErr); reason != want {
		t.Fatalf("readiness reason = %q, want %q", reason, want)
	}
}

func closedDatabaseReadinessCheck(t *testing.T) error {
	t.Helper()
	fixture := newReadinessFixture(t)
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	return fixture.readiness.Check(context.Background())
}

func mismatchedStreamReadinessCheck(t *testing.T) error {
	t.Helper()
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
	return fixture.readiness.Check(context.Background())
}

func inactiveConsumerReadinessCheck(t *testing.T) error {
	t.Helper()
	fixture := newReadinessFixture(t)
	fixture.consumer.Stop()
	select {
	case <-fixture.consumer.Closed():
		return fixture.readiness.Check(context.Background())
	case <-time.After(3 * time.Second):
		t.Fatal("consumer did not stop")
		return nil
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

type readinessFixture struct {
	database   interface{ Close() error }
	connection *natsgo.Conn
	jetstream  jetstream.JetStream
	consumer   *devicesnats.ObservationConsumer
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
	durable, err := devicesnats.ProvisionObservationResources(ctx, js)
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
	return readinessFixture{
		database: database, connection: connection, jetstream: js, consumer: consumer,
		readiness: NewRuntimeReadiness(database, connection, js, consumer),
	}
}
