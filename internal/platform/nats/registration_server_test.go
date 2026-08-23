package nats

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	natsgo "github.com/nats-io/nats.go"
)

func TestRegistrationServerValidatesRoutesAndReturnsCorrelatedResponse(t *testing.T) {
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	handled := make(chan string, 1)
	server, err := StartRegistrationServer(connection, validator, func(_ context.Context, adapterID string, registration Registration) (RegistrationResponse, error) {
		handled <- adapterID
		return RegistrationResponse{Status: "accepted", Binding: &Binding{
			BindingKey: registration.BindingKey,
			DeviceID:   "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab",
			Entities:   []EntityBinding{{Key: "power", EntityID: testEntityID}},
		}}, nil
	}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	requestID := "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	request := Envelope[Registration]{
		ID: requestID, Schema: contractsv1.RegistrationRequestSchemaID,
		EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: testCorrelationID,
		Data: Registration{
			BindingKey: "office-light",
			Device:     DeviceDescriptor{Name: "Office light", Kind: "light"},
			Entities: []EntityDescriptor{{
				Key: "power", ExternalID: "light.office", Name: "Power", Type: "hearth.power/v1",
				Support: []byte(`{"state":{},"operations":{"set":{}}}`),
			}},
		},
	}
	payload, err := Encode(validator, contractsv1.RegistrationRequestSchemaID, request)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := RegistrationSubject("simulator")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := connection.RequestMsgWithContext(context.Background(), &natsgo.Msg{
		Subject: subject, Header: make(natsgo.Header), Data: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := Decode[RegistrationResponse](validator, contractsv1.RegistrationResponseSchemaID, reply.Data)
	if err != nil {
		t.Fatal(err)
	}
	if response.CausationID == nil || *response.CausationID != requestID ||
		response.CorrelationID != testCorrelationID || response.Data.Binding == nil ||
		response.Data.Binding.Entities[0].EntityID != testEntityID {
		t.Fatalf("response = %#v", response)
	}
	select {
	case adapterID := <-handled:
		if adapterID != "simulator" {
			t.Fatalf("adapter ID = %q", adapterID)
		}
	default:
		t.Fatal("registration handler was not called")
	}
}
