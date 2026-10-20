//go:build integration

package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and cursor validation.

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestOwnedMappingsServerUsesSubjectIdentityAndReturnsEmptyDefaultPage(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	server, err := StartOwnedMappingsServer(connection, validator, ownedMappingListerFunc(func(
		_ context.Context,
		adapterID string,
		runtimeID adapters.RuntimeID,
		page devices.OwnedMappingPageParams,
	) (devices.Page[devices.OwnedMapping], error) {
		if adapterID != "subject-adapter" || runtimeID != adapters.RuntimeID(testRuntimeID) {
			t.Fatalf("lister identity = %q/%q", adapterID, runtimeID)
		}
		if page.Limit != 50 || page.After != nil {
			t.Fatalf("page = %#v, want default terminal page", page)
		}
		return devices.Page[devices.OwnedMapping]{}, nil
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	response := requestOwnedMappings(
		t, connection, validator, "subject-adapter", ownedMappingsRequest{},
	)
	if response.CausationID == nil || *response.CausationID != ownedMappingsRequestID ||
		response.CorrelationID != testCorrelationID || response.Data.Status != statusAccepted ||
		response.Data.Items == nil || len(*response.Data.Items) != 0 || response.Data.NextCursor != "" ||
		response.Data.Error != nil {
		t.Fatalf("response = %#v", response)
	}
}

func TestOwnedMappingsServerMapsLimitAndCursorAndUsesLastReturnedRowForNextCursor(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	firstPosition := devices.OwnedMappingPosition{BindingKey: "hall-light", EntityKey: "power"}
	firstCursor, err := encodeOwnedMappingCursor("simulator", firstPosition)
	if err != nil {
		t.Fatal(err)
	}
	firstPage := []devices.OwnedMapping{
		{
			BindingKey: "office-light", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			EntityKey: "brightness", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789aa",
		},
		{
			BindingKey: "office-light", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			EntityKey: "power", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		},
	}
	var calls atomic.Int32
	server, err := StartOwnedMappingsServer(connection, validator, ownedMappingListerFunc(func(
		_ context.Context,
		adapterID string,
		runtimeID adapters.RuntimeID,
		page devices.OwnedMappingPageParams,
	) (devices.Page[devices.OwnedMapping], error) {
		if adapterID != "simulator" || runtimeID != adapters.RuntimeID(testRuntimeID) || page.Limit != 2 ||
			page.After == nil {
			t.Fatalf("lister input = %q/%q/%#v", adapterID, runtimeID, page)
		}
		switch calls.Add(1) {
		case 1:
			if !reflect.DeepEqual(*page.After, firstPosition) {
				t.Fatalf("first position = %#v", page.After)
			}
			return devices.Page[devices.OwnedMapping]{Items: firstPage, HasMore: true}, nil
		case 2:
			want := devices.OwnedMappingPosition{BindingKey: "office-light", EntityKey: "power"}
			if !reflect.DeepEqual(*page.After, want) {
				t.Fatalf("second position = %#v, want %#v", page.After, want)
			}
			return devices.Page[devices.OwnedMapping]{Items: []devices.OwnedMapping{}, HasMore: false}, nil
		default:
			t.Fatalf("unexpected lister call")
			return devices.Page[devices.OwnedMapping]{}, nil
		}
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	limit := 2
	first := requestOwnedMappings(t, connection, validator, "simulator", ownedMappingsRequest{
		Limit: &limit, Cursor: firstCursor,
	})
	wantItems := []ownedMapping{
		{
			BindingKey: "office-light", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			EntityKey: "brightness", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789aa",
		},
		{
			BindingKey: "office-light", DeviceID: "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			EntityKey: "power", EntityID: "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		},
	}
	if first.Data.Items == nil || !reflect.DeepEqual(*first.Data.Items, wantItems) ||
		first.Data.NextCursor == "" {
		t.Fatalf("first response = %#v", first.Data)
	}
	nextPosition, err := decodeOwnedMappingCursor(first.Data.NextCursor, "simulator")
	if err != nil {
		t.Fatal(err)
	}
	wantNext := devices.OwnedMappingPosition{BindingKey: "office-light", EntityKey: "power"}
	if !reflect.DeepEqual(*nextPosition, wantNext) {
		t.Fatalf("next position = %#v, want %#v", nextPosition, wantNext)
	}

	terminal := requestOwnedMappings(t, connection, validator, "simulator", ownedMappingsRequest{
		Limit: &limit, Cursor: first.Data.NextCursor,
	})
	if terminal.Data.Items == nil || len(*terminal.Data.Items) != 0 || terminal.Data.NextCursor != "" {
		t.Fatalf("terminal response = %#v", terminal.Data)
	}
}

func TestOwnedMappingsServerMapsOnlyCursorAndFencingRejections(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server, err := StartOwnedMappingsServer(connection, validator, ownedMappingListerFunc(func(
		context.Context,
		string,
		adapters.RuntimeID,
		devices.OwnedMappingPageParams,
	) (devices.Page[devices.OwnedMapping], error) {
		switch calls.Add(1) {
		case 1:
			return devices.Page[devices.OwnedMapping]{}, adapters.ErrRuntimeFenced
		case 2:
			return devices.Page[devices.OwnedMapping]{}, devices.ErrInvalidPage
		default:
			return devices.Page[devices.OwnedMapping]{}, errors.New("unexpected call")
		}
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	invalid := requestOwnedMappings(t, connection, validator, "simulator", ownedMappingsRequest{
		Cursor: "not-base64!",
	})
	if invalid.Data.Status != statusRejected || invalid.Data.Error == nil ||
		invalid.Data.Error.Code != ownedMappingsInvalidCursorCode || calls.Load() != 0 {
		t.Fatalf("invalid cursor response = %#v, calls = %d", invalid.Data, calls.Load())
	}
	fenced := requestOwnedMappings(t, connection, validator, "simulator", ownedMappingsRequest{})
	if fenced.Data.Status != statusRejected || fenced.Data.Error == nil ||
		fenced.Data.Error.Code != runtimeFencedCode {
		t.Fatalf("fenced response = %#v", fenced.Data)
	}
	invalidPage := requestOwnedMappings(t, connection, validator, "simulator", ownedMappingsRequest{})
	if invalidPage.Data.Status != statusRejected || invalidPage.Data.Error == nil ||
		invalidPage.Data.Error.Code != ownedMappingsInvalidCursorCode {
		t.Fatalf("invalid page response = %#v", invalidPage.Data)
	}
}

func TestOwnedMappingsPublicationFailureDoesNotReply(t *testing.T) {
	t.Parallel()
	broker, serverConnection, _ := startJetStream(t)
	clientConnection, err := natsgo.Connect(broker.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(clientConnection.Close)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	endpoint, err := StartOwnedMappingsServer(
		serverConnection,
		validator,
		ownedMappingListerFunc(func(
			context.Context,
			string,
			adapters.RuntimeID,
			devices.OwnedMappingPageParams,
		) (devices.Page[devices.OwnedMapping], error) {
			close(entered)
			<-release
			return devices.Page[devices.OwnedMapping]{Items: []devices.OwnedMapping{}}, nil
		}),
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = endpoint.Drain() })

	payload, err := natswire.Encode(
		validator, contractsv1.OwnedMappingsRequestSchemaID, validOwnedMappingsEnvelope(ownedMappingsRequest{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.OwnedMappingsSubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		_, requestErr := clientConnection.RequestMsgWithContext(ctx, &natsgo.Msg{
			Subject: subject, Header: make(natsgo.Header), Data: payload,
		})
		result <- requestErr
	}()
	<-entered
	serverConnection.Close()
	close(release)
	if requestErr := <-result; !errors.Is(requestErr, context.DeadlineExceeded) {
		t.Fatalf("request error = %v, want no response before deadline", requestErr)
	}
}

func TestOwnedMappingsRepositoryFailureDoesNotReply(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	server, err := StartOwnedMappingsServer(connection, validator, ownedMappingListerFunc(func(
		context.Context,
		string,
		adapters.RuntimeID,
		devices.OwnedMappingPageParams,
	) (devices.Page[devices.OwnedMapping], error) {
		return devices.Page[devices.OwnedMapping]{}, errors.New("SQLite unavailable")
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	payload, err := natswire.Encode(
		validator, contractsv1.OwnedMappingsRequestSchemaID, validOwnedMappingsEnvelope(ownedMappingsRequest{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.OwnedMappingsSubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = connection.RequestMsgWithContext(ctx, &natsgo.Msg{
		Subject: subject, Header: make(natsgo.Header), Data: payload,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request error = %v", err)
	}
}

func requestOwnedMappings(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	adapterID string,
	data ownedMappingsRequest,
) natswire.Envelope[ownedMappingsResponse] {
	t.Helper()
	payload, err := natswire.Encode(
		validator, contractsv1.OwnedMappingsRequestSchemaID, validOwnedMappingsEnvelope(data),
	)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.OwnedMappingsSubject(adapterID, testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reply, err := connection.RequestMsgWithContext(ctx, &natsgo.Msg{
		Subject: subject, Header: make(natsgo.Header), Data: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[ownedMappingsResponse](
		validator, contractsv1.OwnedMappingsResponseSchemaID, reply.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
