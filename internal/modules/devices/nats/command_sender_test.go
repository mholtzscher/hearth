package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"
	"errors"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	commandClientCommandID     = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	commandClientCorrelationID = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

func TestCommandSenderDispatchesValidatedCorrelatedRequests(t *testing.T) {
	t.Parallel()
	server, connection, _ := startJetStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := startAdapterSessionServer(t, connection, validator)
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "hearth-simulator",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	served := make(chan adapter.Command, 2)
	serveErrors := make(chan error, 1)
	baselineSubscriptions := server.NumSubscriptions()
	go func() {
		serveErrors <- session.ServeCommands(ctx, func(_ context.Context, command adapter.Command, responder adapter.Responder) error {
			served <- command
			if string(command.Parameters) == `{"value":true}` {
				return responder.Accept()
			}
			return responder.Reject("simulated rejection")
		})
	}()
	waitForSubscriptions(t, server, baselineSubscriptions+1)

	sender := NewCommandSender(connection, validator)
	request := devices.CommandRequest{
		ID: devices.CommandID(commandClientCommandID), CorrelationID: devices.CorrelationID(commandClientCorrelationID),
		EntityID: devices.EntityID(testEntityID), OperationName: devices.OperationNameSet,
		Parameters: devices.CommandParameters(`{"value":true}`), Deadline: time.Now().Add(time.Second),
	}
	acceptance, err := sender.Send(ctx, "simulator", lifecycle.claimedRuntimeID(), request)
	if err != nil || !acceptance.Accepted {
		t.Fatalf("accepted response = %#v, %v", acceptance, err)
	}
	command := <-served
	if command.ID != string(request.ID) || command.CorrelationID != string(request.CorrelationID) ||
		command.EntityID != string(request.EntityID) || command.OperationName != string(request.OperationName) {
		t.Fatalf("received command = %#v", command)
	}

	request.Parameters = devices.CommandParameters(`{"value":false}`)
	acceptance, err = sender.Send(ctx, "simulator", lifecycle.claimedRuntimeID(), request)
	if err != nil || acceptance.Accepted {
		t.Fatalf("rejected response = %#v, %v", acceptance, err)
	}
	cancel()
	select {
	case serveErr := <-serveErrors:
		if !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("serve error = %v", serveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeCommands did not stop")
	}
}

func TestCommandSenderClassifiesEntityUnavailableRejection(t *testing.T) {
	t.Parallel()
	server, connection, _ := startJetStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := startAdapterSessionServer(t, connection, validator)
	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "hearth-simulator",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	serveErrors := make(chan error, 1)
	baselineSubscriptions := server.NumSubscriptions()
	go func() {
		serveErrors <- session.ServeCommands(ctx, func(
			_ context.Context,
			_ adapter.Command,
			responder adapter.Responder,
		) error {
			return responder.RejectUnavailable("resource unavailable")
		})
	}()
	waitForSubscriptions(t, server, baselineSubscriptions+1)

	sender := NewCommandSender(connection, validator)
	_, err = sender.Send(ctx, "simulator", lifecycle.claimedRuntimeID(), devices.CommandRequest{
		ID: devices.CommandID(commandClientCommandID), CorrelationID: devices.CorrelationID(commandClientCorrelationID),
		EntityID: devices.EntityID(testEntityID), OperationName: devices.OperationNameSet,
		Parameters: devices.CommandParameters(`{"value":false}`), Deadline: time.Now().Add(time.Second),
	})
	if !errors.Is(err, devices.ErrEntityUnavailable) {
		t.Fatalf("error = %v", err)
	}
	cancel()
	if serveErr := <-serveErrors; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("ServeCommands error = %v", serveErr)
	}
}

func TestCommandSenderClassifiesMissingAdapterAsUnavailable(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	sender := NewCommandSender(connection, validator)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = sender.Send(ctx, "missing-adapter", devices.RuntimeID(testRuntimeID), devices.CommandRequest{
		ID: devices.CommandID(commandClientCommandID), CorrelationID: devices.CorrelationID(commandClientCorrelationID),
		EntityID: devices.EntityID(testEntityID), OperationName: devices.OperationNameSet,
		Parameters: devices.CommandParameters(`{"value":true}`), Deadline: time.Now().Add(time.Second),
	})
	if !errors.Is(err, devices.ErrAdapterUnhealthy) {
		t.Fatalf("error = %v", err)
	}
}

func waitForSubscriptions(t *testing.T, server interface{ NumSubscriptions() uint32 }, minimum uint32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if server.NumSubscriptions() >= minimum {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("command subscription did not become active")
}
