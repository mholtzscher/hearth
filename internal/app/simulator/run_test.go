package simulator //nolint:testpackage // Tests exercise package-private fault publication helpers.

import (
	"context"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
)

const simulatorTestEntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"

//nolint:govet // Sequential integration setup intentionally reuses short error variables.
func TestFaultPublishersProduceDuplicateAndMalformedStreamEvidence(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(5 * time.Second) {
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
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := devicesnats.ProvisionObservationResources(ctx, js); err != nil {
		t.Fatal(err)
	}
	config := Config{
		AdapterID:  "simulator",
		NATSURL:    server.ClientURL(),
		BindingKey: "simulated-light",
		Scenario:   "duplicate",
	}
	if err := publishFaultObservation(ctx, config, simulatorTestEntityID); err != nil {
		t.Fatal(err)
	}
	stream, err := js.Stream(ctx, devicesnats.ObservationStreamName)
	if err != nil {
		t.Fatal(err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("messages after duplicate = %d, want 1", info.State.Msgs)
	}
	if err := publishMalformedObservation(ctx, config, simulatorTestEntityID); err != nil {
		t.Fatal(err)
	}
	info, err = stream.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 2 {
		t.Fatalf("messages after malformed = %d, want 2", info.State.Msgs)
	}
}
