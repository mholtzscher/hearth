//go:build integration

package nats_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestFunctionInvokerPersistsWireFailuresWithoutPromotingUncertainEffects(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		name string
		want adapters.Evidence
	}{
		{"succeeded", adapters.Succeeded{Result: json.RawMessage(`{"n":9007199254740993}`)}},
		{"failed", adapters.Failed{ReasonCode: "adapter_busy"}},
		{"unknown", adapters.Unknown{ReasonCode: "adapter_busy"}},
		{"not_executed", adapters.NotExecuted{ReasonCode: "adapter_busy"}},
		{"no_responders", adapters.NotExecuted{ReasonCode: "adapter_unavailable"}},
		{"invalid_schema", adapters.Unknown{ReasonCode: "invalid_response"}},
		{"wrong_correlation", adapters.Unknown{ReasonCode: "invalid_response"}},
		{"wrong_causation", adapters.Unknown{ReasonCode: "invalid_response"}},
		{"wrong_identity", adapters.Unknown{ReasonCode: "invalid_response"}},
		{"missing_reply", adapters.Unknown{ReasonCode: "outcome_timeout"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			checkFunctionWireOutcome(t, scenario.name, scenario.want)
		})
	}
}

func TestFunctionCallerCancellationAndDrainDoNotAbandonAdmittedWork(t *testing.T) {
	t.Parallel()
	fixture := newFunctionWireFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	releaseHandlers := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseHandlers)
	_, err := fixture.connection.Subscribe(fixture.subject, func(message *natsgo.Msg) {
		close(entered)
		<-release
		if reply, replyErr := functionTestReply(message, "succeeded"); replyErr == nil {
			_ = message.Respond(reply)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.connection.Flush(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, invokeErr := fixture.service.Invoke(ctx, fixture.input); result <- invokeErr }()
	select {
	case <-entered:
	case invokeErr := <-result:
		t.Fatalf("invocation returned before handler: %v", invokeErr)
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not enter")
	}
	cancel()
	fixture.service.StopAdmission()
	join, stopJoin := context.WithCancel(context.Background())
	stopJoin()
	if err = fixture.service.Drain(join); !errors.Is(err, context.Canceled) {
		t.Fatalf("unfinished drain=%v", err)
	}
	releaseHandlers()
	if err = <-result; err != nil {
		t.Fatalf("caller cancellation abandoned evidence: %v", err)
	}
	if err = fixture.service.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := fixture.repository.ListInvocations(
		context.Background(),
		adapters.InvocationFilter{AdapterID: "bridge"},
	)
	if err != nil || len(page.Items) != 1 || page.Items[0].Completion == nil {
		t.Fatalf("history=%+v error=%v", page, err)
	}
	if _, ok := page.Items[0].Completion.Evidence.(adapters.Succeeded); !ok {
		t.Fatalf("admitted success lost: %+v", page)
	}
}
