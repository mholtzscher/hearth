package typed_test

import (
	"context"
	"encoding/json"
	"errors"
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

func TestOperationRejectsMissingDecoderAndHandler(t *testing.T) {
	t.Parallel()
	decode := func(json.RawMessage) (parameters, error) { return parameters{}, nil }
	handle := func(context.Context, typed.Command[parameters], adapter.Responder) error { return nil }
	if _, err := typed.Operation("ent_one", "set", nil, handle); err == nil ||
		!strings.Contains(err.Error(), "no parameter decoder") {
		t.Fatalf("nil decoder error = %v", err)
	}
	if _, err := typed.Operation("ent_one", "set", decode, nil); err == nil ||
		!strings.Contains(err.Error(), "no handler") {
		t.Fatalf("nil handler error = %v", err)
	}
	if _, err := typed.Operation("ent_one", "", decode, handle); err == nil {
		t.Fatal("empty operation name unexpectedly accepted")
	}
}

func TestCommandHandlerSkipsHandlerAfterDecodeAndDeadlineFailures(t *testing.T) {
	t.Parallel()
	called := false
	route, err := typed.Operation(
		"ent_one",
		"set",
		func(raw json.RawMessage) (parameters, error) {
			if string(raw) == "bad" {
				return parameters{}, errors.New("cannot decode parameters")
			}
			return parameters{}, nil
		},
		func(context.Context, typed.Command[parameters], adapter.Responder) error {
			called = true
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
	if invokeErr := handler(context.Background(), adapter.Command{
		EntityID: "ent_one", OperationName: "set", Parameters: json.RawMessage("bad"),
		Deadline: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil); invokeErr == nil || !strings.Contains(invokeErr.Error(), "decode") {
		t.Fatalf("decode error = %v", invokeErr)
	}
	if invokeErr := handler(context.Background(), adapter.Command{
		EntityID: "ent_one", OperationName: "set", Parameters: json.RawMessage(`{}`),
		Deadline: "not-a-deadline",
	}, nil); invokeErr == nil || !strings.Contains(invokeErr.Error(), "deadline") {
		t.Fatalf("deadline error = %v", invokeErr)
	}
	if called {
		t.Fatal("handler invoked after failed command decoding")
	}
}

func TestCommandHandlerRejectsUnknownOperationForKnownEntity(t *testing.T) {
	t.Parallel()
	called := false
	route, err := typed.Operation(
		"ent_one", "set",
		func(json.RawMessage) (parameters, error) { return parameters{}, nil },
		func(context.Context, typed.Command[parameters], adapter.Responder) error {
			called = true
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
	if invokeErr := handler(context.Background(), adapter.Command{
		EntityID: "ent_one", OperationName: "unset", Parameters: json.RawMessage(`{}`),
		Deadline: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil); invokeErr == nil || !strings.Contains(invokeErr.Error(), "no typed command route") {
		t.Fatalf("unknown operation error = %v", invokeErr)
	}
	if called {
		t.Fatal("handler invoked for unknown operation")
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
