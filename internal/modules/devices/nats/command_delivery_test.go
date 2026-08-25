package nats

import (
	"context"
	"errors"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
)

func TestCommandDeliveryMapsUnavailableAdapter(t *testing.T) {
	server, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1, NoSigs: true})
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
	t.Cleanup(connection.Close)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	delivery := NewCommandDelivery(platformnats.NewCommandClient(connection, validator))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = delivery.Deliver(ctx, "simulator", devices.CommandDispatch{
		ID:            "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		CorrelationID: "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		EntityID:      "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		OperationName: devices.OperationNameSet,
		Parameters:    devices.CommandParameters(`{"value":true}`),
		Deadline:      time.Now().Add(time.Second),
	})
	if !errors.Is(err, devices.ErrAdapterUnavailable) {
		t.Fatalf("delivery error = %v", err)
	}
}
