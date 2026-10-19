package adapter //nolint:testpackage // Reuses the embedded-NATS lifecycle harness; exercises public SDK entry points.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

type functionFixture struct {
	session       *Session
	core          *natsgo.Conn
	registrations chan *natsgo.Msg
}

func newFunctionFixture(t *testing.T, accept bool) *functionFixture {
	t.Helper()
	broker := startServer(t, -1, t.TempDir())
	fixture := &functionFixture{
		session:       connectSession(t, broker.ClientURL()),
		core:          connectNATS(t, broker.ClientURL()),
		registrations: make(chan *natsgo.Msg, 16),
	}
	_, err := fixture.core.Subscribe(natswire.FunctionRegistrationWildcard(), func(message *natsgo.Msg) {
		fixture.registrations <- message
		if accept {
			fixture.accept(t, message)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.core.Flush(); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *functionFixture) registration(
	t *testing.T,
	message *natsgo.Msg,
) natswire.Envelope[functionRegistrationRequest] {
	t.Helper()
	request, err := natswire.Decode[functionRegistrationRequest](fixture.session.validator,
		contractsv1.AdapterFunctionRegistrationRequestSchemaID, message.Data)
	if err != nil {
		t.Fatal(err)
	}
	if request.ID != request.Data.CatalogRevision {
		t.Fatal("registration ID differs from revision")
	}
	return request
}

func (fixture *functionFixture) accept(t *testing.T, message *natsgo.Msg) {
	t.Helper()
	request := fixture.registration(t, message)
	respondTestLifecycle(t, fixture.session.validator, message, request.ID, request.CorrelationID,
		contractsv1.AdapterFunctionRegistrationResponseSchemaID,
		functionRegistrationResponse{Status: "accepted", CatalogRevision: request.ID})
}

func functionForTest(handler FunctionHandler) Function {
	return Function{
		Descriptor: FunctionDescriptor{
			Key:         "refresh",
			Description: "Refresh configured data",
			TimeoutMS:   1000,
			InputSchema: json.RawMessage(
				`{"type":"object"}`,
			),
			OutputSchema: json.RawMessage(
				`{"type":"object","required":["value"],"properties":{"value":{"type":"integer"}},"additionalProperties":false}`,
			),
		},
		Handler: handler,
	}
}

func successfulFunction(context.Context, FunctionRequest) FunctionResult {
	return FunctionSucceeded{Result: json.RawMessage(`{"value":9007199254740993}`)}
}

func (fixture *functionFixture) payload(t *testing.T, revision, key, arguments string, deadline time.Time) []byte {
	t.Helper()
	id := mustID(t, "afi")
	payload, err := natswire.Encode(fixture.session.validator, contractsv1.AdapterFunctionInvokeRequestSchemaID,
		natswire.Envelope[functionInvokeRequest]{ID: id, Schema: contractsv1.AdapterFunctionInvokeRequestSchemaID,
			EmittedAt: nowString(), CorrelationID: mustID(t, "cor"), Data: functionInvokeRequest{
				InvocationID: id, CatalogRevision: revision, FunctionKey: key, Arguments: json.RawMessage(arguments),
				Deadline: deadline.UTC().Format(time.RFC3339Nano),
			}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func (fixture *functionFixture) invoke(t *testing.T, payload []byte) functionInvokeResponse {
	t.Helper()
	return fixture.invokeWithHeaders(t, payload, nil)
}

func (fixture *functionFixture) invokeWithHeaders(
	t *testing.T,
	payload []byte,
	headers natsgo.Header,
) functionInvokeResponse {
	t.Helper()
	subject, err := natswire.FunctionInvokeSubject(fixture.session.adapterID, fixture.session.runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := fixture.core.RequestMsgWithContext(
		testContext(t),
		&natsgo.Msg{Subject: subject, Data: payload, Header: headers},
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := natswire.Decode[functionInvokeRequest](fixture.session.validator,
		contractsv1.AdapterFunctionInvokeRequestSchemaID, payload)
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[functionInvokeResponse](fixture.session.validator,
		contractsv1.AdapterFunctionInvokeResponseSchemaID, message.Data)
	if err != nil {
		t.Fatal(err)
	}
	if response.ID == request.ID || response.CausationID == nil || *response.CausationID != request.ID ||
		response.CorrelationID != request.CorrelationID || response.Data.InvocationID != request.ID {
		t.Fatalf("response identity mismatch: %#v", response)
	}
	return response.Data
}

func receiveFunctionValue[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("function test barrier timed out")
		var zero T
		return zero
	}
}
