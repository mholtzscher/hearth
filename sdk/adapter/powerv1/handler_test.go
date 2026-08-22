package powerv1

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestCommandHandlerDecodesSetParameters(t *testing.T) {
	deadline := time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)
	received := make(chan SetCommand, 1)
	handler, err := NewCommandHandler("ent_power", Support{}, Handlers{
		Set: func(_ context.Context, command SetCommand, _ adapter.Responder) error {
			received <- command
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler(context.Background(), adapter.Command{
		ID: "cmd_one", CorrelationID: "cor_one", EntityID: "ent_power", OperationName: "set",
		Parameters: json.RawMessage(`{"value":true}`), Deadline: deadline.Format(time.RFC3339Nano),
	}, nil); err != nil {
		t.Fatal(err)
	}
	command := <-received
	if command.ID != "cmd_one" || command.CorrelationID != "cor_one" || !command.Parameters.Value || !command.Deadline.Equal(deadline) {
		t.Fatalf("typed command = %#v", command)
	}
	if err := handler(context.Background(), adapter.Command{
		EntityID: "ent_power", OperationName: "set",
		Parameters: json.RawMessage(`{"value":"on"}`), Deadline: deadline.Format(time.RFC3339Nano),
	}, nil); err == nil {
		t.Fatal("invalid typed parameters unexpectedly accepted")
	}
}
