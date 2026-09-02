package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and cursor validation.

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

type ownedMappingListerFunc func(
	context.Context,
	string,
	devices.RuntimeID,
	devices.OwnedMappingPageParams,
) (devices.Page[devices.OwnedMapping], error)

func (lister ownedMappingListerFunc) ListOwnedMappings(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	page devices.OwnedMappingPageParams,
) (devices.Page[devices.OwnedMapping], error) {
	return lister(ctx, adapterID, runtimeID, page)
}

func TestOwnedMappingsCursorRoundTripAndStrictValidation(t *testing.T) {
	t.Parallel()
	position := devices.OwnedMappingPosition{BindingKey: "office-light", EntityKey: "power"}
	valid, err := encodeOwnedMappingCursor("simulator", position)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeOwnedMappingCursor(valid, "simulator")
	if err != nil || !reflect.DeepEqual(*decoded, position) {
		t.Fatalf("decoded cursor = %#v, error = %v", decoded, err)
	}

	malformedJSON := encodeOwnedMappingCursorDocument(`{"v":`)
	trailingJSON := encodeOwnedMappingCursorDocument(
		`{"v":1,"resource":"adapter_owned_mappings","adapter_id":"simulator","binding_key":"office-light","entity_key":"power"}{}`,
	)
	wrongVersion := encodeOwnedMappingCursorDocument(
		`{"v":2,"resource":"adapter_owned_mappings","adapter_id":"simulator","binding_key":"office-light","entity_key":"power"}`,
	)
	wrongResource := encodeOwnedMappingCursorDocument(
		`{"v":1,"resource":"devices","adapter_id":"simulator","binding_key":"office-light","entity_key":"power"}`,
	)
	wrongAdapter := encodeOwnedMappingCursorDocument(
		`{"v":1,"resource":"adapter_owned_mappings","adapter_id":"other","binding_key":"office-light","entity_key":"power"}`,
	)
	invalidBindingKey := encodeOwnedMappingCursorDocument(
		`{"v":1,"resource":"adapter_owned_mappings","adapter_id":"simulator","binding_key":"Office Light","entity_key":"power"}`,
	)
	invalidEntityKey := encodeOwnedMappingCursorDocument(
		`{"v":1,"resource":"adapter_owned_mappings","adapter_id":"simulator","binding_key":"office-light","entity_key":""}`,
	)

	for name, value := range map[string]string{
		"empty":               "",
		"malformed base64url": "not base64!",
		"padded base64url":    valid + "=",
		"noncanonical":        noncanonicalBase64URL(t, valid),
		"malformed JSON":      malformedJSON,
		"trailing JSON":       trailingJSON,
		"wrong version":       wrongVersion,
		"wrong resource":      wrongResource,
		"wrong Adapter scope": wrongAdapter,
		"invalid Binding key": invalidBindingKey,
		"invalid Entity key":  invalidEntityKey,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, decodeErr := decodeOwnedMappingCursor(value, "simulator"); decodeErr == nil {
				t.Fatalf("decodeOwnedMappingCursor(%q) succeeded", value)
			}
		})
	}
}

func TestOwnedMappingsServerEnforcesCursorByteLimitBeforeListing(t *testing.T) {
	t.Parallel()
	subject, err := natswire.OwnedMappingsSubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	var called atomic.Bool
	lister := ownedMappingListerFunc(func(
		context.Context,
		string,
		devices.RuntimeID,
		devices.OwnedMappingPageParams,
	) (devices.Page[devices.OwnedMapping], error) {
		called.Store(true)
		return devices.Page[devices.OwnedMapping]{}, nil
	})
	logger := slog.New(slog.DiscardHandler)
	atLimit, handled := handleOwnedMappings(
		context.Background(), subject,
		natswire.Envelope[ownedMappingsRequest]{
			ID: ownedMappingsRequestID,
			Data: ownedMappingsRequest{
				Cursor: strings.Repeat("a", maximumOwnedMappingsCursorBytes),
			},
		},
		lister, logger,
	)
	if !handled || atLimit.Error == nil || atLimit.Error.Code != ownedMappingsInvalidCursorCode {
		t.Fatalf("cursor at byte limit response = %#v, handled = %t", atLimit, handled)
	}

	oversized, handled := handleOwnedMappings(
		context.Background(), subject,
		natswire.Envelope[ownedMappingsRequest]{
			ID: ownedMappingsRequestID,
			Data: ownedMappingsRequest{
				Cursor: strings.Repeat("é", maximumOwnedMappingsCursorBytes/2+1),
			},
		},
		lister, logger,
	)
	if handled || called.Load() || !reflect.DeepEqual(oversized, ownedMappingsResponse{}) {
		t.Fatalf("oversized cursor response = %#v, handled = %t, called = %t", oversized, handled, called.Load())
	}
}

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
		runtimeID devices.RuntimeID,
		page devices.OwnedMappingPageParams,
	) (devices.Page[devices.OwnedMapping], error) {
		if adapterID != "subject-adapter" || runtimeID != devices.RuntimeID(testRuntimeID) {
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
		runtimeID devices.RuntimeID,
		page devices.OwnedMappingPageParams,
	) (devices.Page[devices.OwnedMapping], error) {
		if adapterID != "simulator" || runtimeID != devices.RuntimeID(testRuntimeID) || page.Limit != 2 ||
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
		devices.RuntimeID,
		devices.OwnedMappingPageParams,
	) (devices.Page[devices.OwnedMapping], error) {
		switch calls.Add(1) {
		case 1:
			return devices.Page[devices.OwnedMapping]{}, devices.ErrRuntimeFenced
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
			devices.RuntimeID,
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
		devices.RuntimeID,
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

const ownedMappingsRequestID = "map_01890f47-7a6b-7c4d-8e9f-0123456789ab"

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

func validOwnedMappingsEnvelope(data ownedMappingsRequest) natswire.Envelope[ownedMappingsRequest] {
	return natswire.Envelope[ownedMappingsRequest]{
		ID: ownedMappingsRequestID, Schema: contractsv1.OwnedMappingsRequestSchemaID,
		EmittedAt: "2026-09-01T12:00:00Z", CorrelationID: testCorrelationID, Data: data,
	}
}

func encodeOwnedMappingCursorDocument(document string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(document))
}

func noncanonicalBase64URL(t *testing.T, canonical string) string {
	t.Helper()
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	remainder := len(canonical) % 4
	if remainder != 2 && remainder != 3 {
		t.Fatalf("fixture has no unused base64 bits: %q", canonical)
	}
	index := -1
	for candidate := range len(alphabet) {
		if alphabet[candidate] == canonical[len(canonical)-1] {
			index = candidate
			break
		}
	}
	if index < 0 || index&1 != 0 {
		t.Fatalf("unexpected canonical final base64 character %q", canonical[len(canonical)-1])
	}
	return canonical[:len(canonical)-1] + string(alphabet[index|1])
}
