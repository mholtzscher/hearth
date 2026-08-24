package nats

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	commandClientCommandID     = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	commandClientCorrelationID = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

func TestCommandClientDispatchesValidatedCorrelatedRequests(t *testing.T) {
	server, connection, _ := startJetStream(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := adapter.Connect(ctx, adapter.Config{AdapterID: "simulator", NATSURL: server.ClientURL()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	served := make(chan adapter.Command, 2)
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- session.ServeCommands(ctx, func(_ context.Context, command adapter.Command, responder adapter.Responder) error {
			served <- command
			if string(command.Parameters) == `{"value":true}` {
				return responder.Accept()
			}
			return responder.Reject("simulated rejection")
		})
	}()
	waitForSubscriptions(t, server, 1)

	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	client := NewCommandClient(connection, validator)
	request := CommandRequest{
		ID: commandClientCommandID, CorrelationID: commandClientCorrelationID,
		EntityID: testEntityID, OperationName: "set", Parameters: json.RawMessage(`{"value":true}`),
		Deadline: time.Now().Add(time.Second),
	}
	acceptance, err := client.Send(ctx, "simulator", request)
	if err != nil || !acceptance.Accepted {
		t.Fatalf("accepted response = %#v, %v", acceptance, err)
	}
	command := <-served
	if command.ID != request.ID || command.CorrelationID != request.CorrelationID || command.EntityID != request.EntityID || command.OperationName != request.OperationName {
		t.Fatalf("received command = %#v", command)
	}

	request.Parameters = json.RawMessage(`{"value":false}`)
	acceptance, err = client.Send(ctx, "simulator", request)
	if err != nil || acceptance.Accepted {
		t.Fatalf("rejected response = %#v, %v", acceptance, err)
	}
	cancel()
	select {
	case err := <-serveErrors:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("serve error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeCommands did not stop")
	}
}

func TestCommandClientClassifiesMissingAdapterAsUnavailable(t *testing.T) {
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	client := NewCommandClient(connection, validator)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = client.Send(ctx, "missing-adapter", CommandRequest{
		ID: commandClientCommandID, CorrelationID: commandClientCorrelationID,
		EntityID: testEntityID, OperationName: "set", Parameters: json.RawMessage(`{"value":true}`),
		Deadline: time.Now().Add(time.Second),
	})
	if !errors.Is(err, ErrCommandUnavailable) {
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
