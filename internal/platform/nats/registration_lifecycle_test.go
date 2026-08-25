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

func TestRegistrationServerClosedWaitsForHandler(t *testing.T) {
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	server, err := StartRegistrationServer(connection, validator, func(context.Context, string, Registration) (RegistrationResponse, error) {
		close(started)
		<-release
		return RegistrationResponse{Status: "rejected", Error: &RegistrationError{Code: "invalid_descriptor", Message: "test"}}, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	closed := server.Closed()
	request := Envelope[Registration]{
		ID:            "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Schema:        contractsv1.RegistrationRequestSchemaID,
		EmittedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		CorrelationID: testCorrelationID,
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
	if err := connection.PublishMsg(&natsgo.Msg{Subject: subject, Reply: natsgo.NewInbox(), Data: payload}); err != nil {
		t.Fatal(err)
	}
	if err := connection.Flush(); err != nil {
		t.Fatal(err)
	}
	<-started
	drained := make(chan error, 1)
	go func() { drained <- server.Drain() }()
	select {
	case <-closed:
		t.Fatal("Closed returned while registration handler was blocked")
	default:
	}
	close(release)
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Closed did not report drained registration handler")
	}
}
