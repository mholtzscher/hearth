package nats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	testObservationID       = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testSecondObservationID = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	testThirdObservationID  = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ad"
	testFourthObservationID = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ae"
	testCorrelationID       = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

func TestProvisionObservationResourcesCreatesAndValidatesRequiredConfiguration(t *testing.T) {
	_, _, js := startJetStream(t)
	consumer, err := ProvisionObservationResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	if consumer.CachedInfo().Config.Name != ObservationConsumerName {
		t.Fatalf("consumer = %#v", consumer.CachedInfo().Config)
	}
	if _, err := ProvisionObservationResources(context.Background(), js); err != nil {
		t.Fatalf("second provisioning: %v", err)
	}
	if err := ValidateObservationResources(context.Background(), js); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionObservationResourcesRejectsMismatchedExistingConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*jetstream.StreamConfig)
	}{
		{"max bytes", func(config *jetstream.StreamConfig) { config.MaxBytes = 42 }},
		{"max messages", func(config *jetstream.StreamConfig) { config.MaxMsgs = 1 }},
		{"max messages per subject", func(config *jetstream.StreamConfig) { config.MaxMsgsPerSubject = 1 }},
		{"max message size", func(config *jetstream.StreamConfig) { config.MaxMsgSize = 1024 }},
		{"no acknowledgements", func(config *jetstream.StreamConfig) { config.NoAck = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, js := startJetStream(t)
			config := observationStreamConfig()
			test.mutate(&config)
			if _, err := js.CreateStream(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			_, err := ProvisionObservationResources(context.Background(), js)
			if err == nil || !strings.Contains(err.Error(), "does not match") {
				t.Fatalf("provisioning error = %v", err)
			}
		})
	}
}

func TestObservationConsumerAcknowledgesCommittedAndMalformedMessages(t *testing.T) {
	_, connection, js := startJetStream(t)
	consumer, err := ProvisionObservationResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	deliveries := make(chan ObservationDelivery, 3)
	running, err := StartObservationConsumer(context.Background(), consumer, validator, func(_ context.Context, delivery ObservationDelivery) error {
		deliveries <- delivery
		if delivery.Envelope.ID == testFourthObservationID {
			return errors.New("temporary SQLite failure")
		}
		return nil
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)
	if !running.Active() {
		t.Fatal("consumer is not active")
	}

	publishObservationEnvelope(t, js, testObservationID, `true`)
	select {
	case delivery := <-deliveries:
		if delivery.Envelope.ID != testObservationID || delivery.Route.AdapterID != "simulator" ||
			delivery.Route.EntityID != testEntityID || delivery.StreamSequence != 1 || delivery.ObservedAt.IsZero() {
			t.Fatalf("delivery = %#v", delivery)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for observation delivery")
	}
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 1 && info.NumAckPending == 0
	})
	if output := logs.String(); !strings.Contains(output, "adapter observation clock is ahead") ||
		!strings.Contains(output, testObservationID) || !strings.Contains(output, testEntityID) {
		t.Fatalf("clock-skew log = %s", output)
	}

	message := &natsgo.Msg{
		Subject: mustObservationSubject(t),
		Header:  natsgo.Header{natsgo.MsgIdHdr: []string{testSecondObservationID}},
		Data:    []byte(`{}`),
	}
	if _, err := js.PublishMsg(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 2 && info.NumAckPending == 0
	})
	select {
	case unexpected := <-deliveries:
		t.Fatalf("malformed message reached handler: %#v", unexpected)
	default:
	}

	invalidSourceUpdatedAt := "2026-08-22t12:34:56Z"
	publishObservationEnvelopeWithSource(t, js, testThirdObservationID, `false`, &invalidSourceUpdatedAt)
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.NumAckPending == 0 && info.AckFloor.Consumer >= 3
	})
	select {
	case unexpected := <-deliveries:
		t.Fatalf("unparseable source_updated_at reached handler: %#v", unexpected)
	default:
	}
	if output := logs.String(); !strings.Contains(output, "parse source_updated_at") || !strings.Contains(output, testThirdObservationID) {
		t.Fatalf("source_updated_at log = %s", output)
	}

	publishObservationEnvelope(t, js, testFourthObservationID, `false`)
	select {
	case <-deliveries:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for transiently failed observation delivery")
	}
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.NumAckPending == 1 && info.AckFloor.Consumer == 3
	})

	running.Stop()
	select {
	case <-running.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("consumer did not stop")
	}
	if running.Active() {
		t.Fatal("stopped consumer remains active")
	}
	_ = connection
}

func publishObservationEnvelope(t *testing.T, js jetstream.JetStream, observationID, value string) {
	t.Helper()
	publishObservationEnvelopeWithSource(t, js, observationID, value, nil)
}

func publishObservationEnvelopeWithSource(t *testing.T, js jetstream.JetStream, observationID, value string, sourceUpdatedAt *string) {
	t.Helper()
	emittedAt := time.Now().UTC()
	adapterReceivedAt := emittedAt.Add(2 * time.Minute)
	envelope := Envelope[Observation]{
		ID: observationID, Schema: contractsv1.ObservationSchemaID, EmittedAt: emittedAt.Format(time.RFC3339Nano), CorrelationID: testCorrelationID,
		Data: Observation{
			EntityID: testEntityID, Value: json.RawMessage(value), AdapterReceivedAt: adapterReceivedAt.Format(time.RFC3339Nano),
			SourceUpdatedAt: sourceUpdatedAt,
		},
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := Encode(validator, contractsv1.ObservationSchemaID, envelope)
	if err != nil {
		t.Fatal(err)
	}
	message := &natsgo.Msg{
		Subject: mustObservationSubject(t),
		Header:  natsgo.Header{natsgo.MsgIdHdr: []string{observationID}},
		Data:    payload,
	}
	if _, err := js.PublishMsg(context.Background(), message); err != nil {
		t.Fatal(err)
	}
}

func mustObservationSubject(t *testing.T) string {
	t.Helper()
	subject, err := ObservationSubject("simulator", testEntityID)
	if err != nil {
		t.Fatal(err)
	}
	return subject
}

func waitForConsumer(t *testing.T, consumer jetstream.Consumer, condition func(*jetstream.ConsumerInfo) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, err := consumer.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if condition(info) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	info, _ := consumer.Info(context.Background())
	t.Fatalf("consumer condition not met: %#v", info)
}

func startJetStream(t *testing.T) (*natsserver.Server, *natsgo.Conn, jetstream.JetStream) {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	connection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	return server, connection, js
}
