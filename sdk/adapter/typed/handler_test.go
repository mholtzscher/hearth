package typed_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter/typed"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type parameters struct {
	Value bool `json:"value"`
}

func TestCommandHandlerRoutesAndDecodesTypedCommands(t *testing.T) {
	t.Parallel()
	deadline := time.Date(2026, 8, 22, 12, 0, 0, 123, time.UTC)
	received := make(chan typed.Command[parameters], 1)
	route, err := typed.Operation(
		"ent_one",
		"set",
		func(raw json.RawMessage) (parameters, error) {
			var value parameters
			err := json.Unmarshal(raw, &value)
			return value, err
		},
		func(_ context.Context, command typed.Command[parameters], _ adapter.Responder) error {
			received <- command
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := typed.NewCommandHandler(route)
	if err != nil {
		t.Fatal(err)
	}
	err = handler(context.Background(), adapter.Command{
		ID: "cmd_one", CorrelationID: "cor_one", EntityID: "ent_one", OperationName: "set",
		Parameters: json.RawMessage(`{"value":true}`), Deadline: deadline.Format(time.RFC3339Nano),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	command := <-received
	if command.ID != "cmd_one" || command.CorrelationID != "cor_one" || command.EntityID != "ent_one" ||
		!command.Parameters.Value || !command.Deadline.Equal(deadline) {
		t.Fatalf("typed command = %#v", command)
	}
}

func TestCommandHandlerMatchesEntityAndOperationTogether(t *testing.T) {
	t.Parallel()
	route, err := typed.Operation(
		"ent_one", "set",
		func(json.RawMessage) (parameters, error) { return parameters{}, nil },
		func(context.Context, typed.Command[parameters], adapter.Responder) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := typed.NewCommandHandler(route)
	if err != nil {
		t.Fatal(err)
	}
	command := adapter.Command{
		EntityID:      "ent_two",
		OperationName: "set",
		Deadline:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	if handlerErr := handler(
		context.Background(),
		command,
		nil,
	); handlerErr == nil ||
		!strings.Contains(handlerErr.Error(), "no typed command route") {
		t.Fatalf("route error = %v", handlerErr)
	}
}

func TestCommandHandlerRejectsDuplicateAndInvalidRoutes(t *testing.T) {
	t.Parallel()
	newRoute := func() typed.Route {
		route, err := typed.Operation(
			"ent_one", "set",
			func(json.RawMessage) (parameters, error) { return parameters{}, nil },
			func(context.Context, typed.Command[parameters], adapter.Responder) error { return nil },
		)
		if err != nil {
			t.Fatal(err)
		}
		return route
	}
	if _, err := typed.NewCommandHandler(newRoute(), newRoute()); err == nil {
		t.Fatal("duplicate route unexpectedly accepted")
	}
	if _, err := typed.NewCommandHandler(); err == nil {
		t.Fatal("empty routes unexpectedly accepted")
	}
	if _, err := typed.Operation[parameters](
		"",
		"set",
		func(json.RawMessage) (parameters, error) { return parameters{}, nil },
		func(context.Context, typed.Command[parameters], adapter.Responder) error { return nil },
	); err == nil {
		t.Fatal("empty entity ID unexpectedly accepted")
	}
	if _, err := typed.Operation[parameters](
		"ent_one",
		"bad.name",
		func(json.RawMessage) (parameters, error) { return parameters{}, nil },
		func(context.Context, typed.Command[parameters], adapter.Responder) error { return nil },
	); err == nil {
		t.Fatal("unsafe operation unexpectedly accepted")
	}
}
