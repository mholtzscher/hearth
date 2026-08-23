package hearthd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
	"github.com/mholtzscher/hearth/sdk/adapter"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestCoreNATSTransportRegistersAndProjectsDurableObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devices.NewSQLiteRepository(database, catalog)
	service := devices.NewService(repository, catalog, devices.Dependencies{})

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
	coreConnection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coreConnection.Close)
	js, err := jetstream.New(coreConnection)
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
	registrations, err := platformnats.StartRegistrationServer(coreConnection, validator, registrationHandler(service), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Drain() })
	observations, err := platformnats.StartObservationConsumer(ctx, durable, validator, observationHandler(service), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observations.Stop)

	session, err := adapter.Connect(ctx, adapter.Config{AdapterID: "simulator", NATSURL: server.ClientURL()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: "office-light",
		Device:     adapter.DeviceDescriptor{Name: "Office light", Kind: "light"},
		Entities: []adapter.EntityDescriptor{{
			Key: "power", ExternalID: "sim.light", Name: "Power", Type: "hearth.power/v1",
			Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entityID, err := devices.ParseEntityID(binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := session.PublishObservation(ctx, adapter.Observation{
		EntityID: binding.Entities[0].EntityID, Value: json.RawMessage(`true`),
		AdapterReceivedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, getErr := service.GetEntity(ctx, entityID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if view.State != nil {
			if view.State.ObservationID != devices.ObservationID(observationID) || string(view.State.Value) != "true" {
				t.Fatalf("projected state = %#v", view.State)
			}
			if err := NewRuntimeReadiness(database, coreConnection, js, observations).Check(ctx); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("observation was not projected")
}
