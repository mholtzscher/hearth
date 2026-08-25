package nats

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	natsgo "github.com/nats-io/nats.go"
)

type registrarFunc func(context.Context, string, devices.Registration) (devices.Binding, error)

func (registrar registrarFunc) Register(
	ctx context.Context,
	adapterID string,
	registration devices.Registration,
) (devices.Binding, error) {
	return registrar(ctx, adapterID, registration)
}

func TestRegistrationServerMapsDomainRegistrationAndReturnsCorrelatedResponse(t *testing.T) {
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	externalID := "device.office"
	handled := make(chan devices.Registration, 1)
	server, err := StartRegistrationServer(connection, validator, registrarFunc(func(
		_ context.Context,
		adapterID string,
		registration devices.Registration,
	) (devices.Binding, error) {
		if adapterID != "simulator" {
			t.Errorf("adapter ID = %q", adapterID)
		}
		handled <- registration
		return devices.Binding{
			BindingKey: registration.BindingKey,
			DeviceID:   devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab"),
			Entities:   []devices.EntityBinding{{Key: "power", EntityID: devices.EntityID(testEntityID)}},
		}, nil
	}), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	requestID := "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	request := natswire.Envelope[registration]{
		ID: requestID, Schema: contractsv1.RegistrationRequestSchemaID,
		EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: testCorrelationID,
		Data: registration{
			BindingKey: "office-light",
			Device:     deviceDescriptor{ExternalID: &externalID, Name: "Office light", Kind: "light"},
			Entities: []entityDescriptor{{
				Key: "power", ExternalID: "light.office", Name: "Power", Type: "hearth.power/v1",
				Support: []byte(`{"state":{},"operations":{"set":{}}}`),
			}},
		},
	}
	response := requestRegistration(t, connection, validator, request)
	if response.CausationID == nil || *response.CausationID != requestID ||
		response.CorrelationID != testCorrelationID || response.Data.Binding == nil ||
		response.Data.Binding.Entities[0].EntityID != testEntityID {
		t.Fatalf("response = %#v", response)
	}
	mapped := <-handled
	if mapped.Device.Kind != devices.DeviceKindLight || mapped.Device.ExternalID == nil ||
		*mapped.Device.ExternalID != externalID || mapped.Entities[0].TypeID != devices.EntityTypeID("hearth.power/v1") ||
		string(mapped.Entities[0].Support) != `{"state":{},"operations":{"set":{}}}` {
		t.Fatalf("mapped registration = %#v", mapped)
	}
	externalID = "mutated"
	request.Data.Entities[0].Support[0] = 'x'
	if *mapped.Device.ExternalID != "device.office" || string(mapped.Entities[0].Support) != `{"state":{},"operations":{"set":{}}}` {
		t.Fatalf("registrar inputs were not copied: %#v", mapped)
	}
}

func TestRegistrationServerMapsDomainRejection(t *testing.T) {
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	server, err := StartRegistrationServer(connection, validator, registrarFunc(func(
		context.Context,
		string,
		devices.Registration,
	) (devices.Binding, error) {
		return devices.Binding{}, &devices.RegistrationRejectedError{
			Code: devices.RegistrationIdentityConflict, Message: "binding is already owned",
		}
	}), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	response := requestRegistration(t, connection, validator, validRegistrationEnvelope())
	if response.Data.Status != "rejected" || response.Data.Error == nil ||
		response.Data.Error.Code != "identity_conflict" || response.Data.Error.Message != "binding is already owned" {
		t.Fatalf("response = %#v", response)
	}
}

func TestRegistrationInfrastructureFailureDoesNotReply(t *testing.T) {
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	server, err := StartRegistrationServer(connection, validator, registrarFunc(func(
		context.Context,
		string,
		devices.Registration,
	) (devices.Binding, error) {
		return devices.Binding{}, errors.New("SQLite unavailable")
	}), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	request := validRegistrationEnvelope()
	payload, err := natswire.Encode(validator, contractsv1.RegistrationRequestSchemaID, request)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.RegistrationSubject("simulator")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = connection.RequestMsgWithContext(ctx, &natsgo.Msg{Subject: subject, Header: make(natsgo.Header), Data: payload})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request error = %v", err)
	}
}

func validRegistrationEnvelope() natswire.Envelope[registration] {
	return natswire.Envelope[registration]{
		ID:            "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Schema:        contractsv1.RegistrationRequestSchemaID,
		EmittedAt:     "2026-08-20T12:34:56Z",
		CorrelationID: testCorrelationID,
		Data: registration{
			BindingKey: "office-light",
			Device:     deviceDescriptor{Name: "Office light", Kind: "light"},
			Entities: []entityDescriptor{{
				Key: "power", ExternalID: "light.office", Name: "Power", Type: "hearth.power/v1",
				Support: []byte(`{"state":{},"operations":{"set":{}}}`),
			}},
		},
	}
}

func requestRegistration(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	request natswire.Envelope[registration],
) natswire.Envelope[registrationResponse] {
	t.Helper()
	payload, err := natswire.Encode(validator, contractsv1.RegistrationRequestSchemaID, request)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.RegistrationSubject("simulator")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := connection.RequestMsgWithContext(context.Background(), &natsgo.Msg{
		Subject: subject, Header: make(natsgo.Header), Data: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[registrationResponse](validator, contractsv1.RegistrationResponseSchemaID, reply.Data)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
