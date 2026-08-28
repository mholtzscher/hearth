package nats

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type entityEnablementSetterFunc func(context.Context, string, devices.EntityID, bool) (bool, error)

func (setter entityEnablementSetterFunc) SetOwnedEntityEnabled(
	ctx context.Context,
	adapterID string,
	entityID devices.EntityID,
	enabled bool,
) (bool, error) {
	return setter(ctx, adapterID, entityID, enabled)
}

func TestEntityEnablementServerReturnsCorrelatedAcceptedAndRejectedResponses(t *testing.T) {
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	server, err := StartEntityEnablementServer(connection, validator, entityEnablementSetterFunc(func(
		_ context.Context,
		adapterID string,
		entityID devices.EntityID,
		enabled bool,
	) (bool, error) {
		if adapterID != "simulator" || entityID != devices.EntityID(testEntityID) {
			t.Fatalf("setter identity = %q/%q", adapterID, entityID)
		}
		if enabled {
			return false, devices.ErrEntityWrongAdapter
		}
		return false, nil
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	accepted := requestEntityEnablement(t, connection, validator, false)
	if accepted.Data.Status != "accepted" || accepted.Data.EntityID != testEntityID ||
		accepted.Data.Enabled == nil || *accepted.Data.Enabled || accepted.CausationID == nil ||
		*accepted.CausationID != "ena_01890f47-7a6b-7c4d-8e9f-0123456789ab" ||
		accepted.CorrelationID != testCorrelationID {
		t.Fatalf("accepted response = %#v", accepted)
	}
	rejected := requestEntityEnablement(t, connection, validator, true)
	if rejected.Data.Status != "rejected" || rejected.Data.Error == nil ||
		rejected.Data.Error.Code != "wrong_adapter" || rejected.Data.Enabled != nil || rejected.Data.EntityID != "" {
		t.Fatalf("rejected response = %#v", rejected)
	}
}

func TestEntityEnablementServerDiscardsRoutePayloadMismatchWithoutInvokingSetter(t *testing.T) {
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server, err := StartEntityEnablementServer(connection, validator, entityEnablementSetterFunc(func(
		context.Context,
		string,
		devices.EntityID,
		bool,
	) (bool, error) {
		calls.Add(1)
		return false, nil
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	request := validEntityEnablementEnvelope(false)
	payload, err := natswire.Encode(validator, contractsv1.EntityEnablementRequestSchemaID, request)
	if err != nil {
		t.Fatal(err)
	}
	otherEntity := "ent_01890f47-7a6c-7c4d-8e9f-0123456789ab"
	subject, err := natswire.EntityEnablementSubject("simulator", otherEntity)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = connection.RequestMsgWithContext(ctx, &natsgo.Msg{
		Subject: subject, Header: make(natsgo.Header), Data: payload,
	})
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 0 {
		t.Fatalf("mismatched request = %v, calls = %d", err, calls.Load())
	}
}

func requestEntityEnablement(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	enabled bool,
) natswire.Envelope[entityEnablementResponse] {
	t.Helper()
	request := validEntityEnablementEnvelope(enabled)
	payload, err := natswire.Encode(validator, contractsv1.EntityEnablementRequestSchemaID, request)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.EntityEnablementSubject("simulator", testEntityID)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := connection.RequestMsgWithContext(context.Background(), &natsgo.Msg{
		Subject: subject, Header: make(natsgo.Header), Data: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[entityEnablementResponse](
		validator, contractsv1.EntityEnablementResponseSchemaID, reply.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func validEntityEnablementEnvelope(enabled bool) natswire.Envelope[entityEnablementRequest] {
	return natswire.Envelope[entityEnablementRequest]{
		ID:     "ena_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Schema: contractsv1.EntityEnablementRequestSchemaID, EmittedAt: "2026-08-26T12:00:00Z",
		CorrelationID: testCorrelationID,
		Data:          entityEnablementRequest{EntityID: testEntityID, Enabled: enabled},
	}
}
