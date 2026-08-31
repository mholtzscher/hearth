package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	testRuntimeID           = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testEntityID            = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testObservationID       = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	testSecondObservationID = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	testThirdObservationID  = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ad"
	testFourthObservationID = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ae"
	testCorrelationID       = "cor_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

type projectorFunc func(
	context.Context,
	string,
	devices.RuntimeID,
	devices.Observation,
	time.Time,
) (devices.ProjectionResult, error)

func (projector projectorFunc) ProjectObservation(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	observation devices.Observation,
	observedAt time.Time,
) (devices.ProjectionResult, error) {
	return projector(ctx, adapterID, runtimeID, observation, observedAt)
}

type projectedObservation struct {
	adapterID   string
	runtimeID   devices.RuntimeID
	observation devices.Observation
	observedAt  time.Time
}

//nolint:gocognit,gocyclo,cyclop // The acknowledgement failure matrix is clearer as one consumer test.
func TestObservationConsumerMapsProjectsAndAcknowledgesByFailureClass(t *testing.T) {
	t.Parallel()
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
	projections := make(chan projectedObservation, 3)
	running, err := StartObservationConsumer(context.Background(), consumer, validator, projectorFunc(func(
		_ context.Context,
		adapterID string,
		runtimeID devices.RuntimeID,
		observation devices.Observation,
		observedAt time.Time,
	) (devices.ProjectionResult, error) {
		projections <- projectedObservation{
			adapterID: adapterID, runtimeID: runtimeID, observation: observation, observedAt: observedAt,
		}
		if observation.ID == devices.ObservationID(testFourthObservationID) {
			return devices.ProjectionResult{}, errors.New("temporary SQLite failure")
		}
		return devices.ProjectionResult{}, nil
	}), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)
	if !running.Active() {
		t.Fatal("consumer is not active")
	}

	publishObservationEnvelope(t, js, testObservationID, `true`)
	select {
	case projection := <-projections:
		if projection.observation.ID != devices.ObservationID(testObservationID) ||
			projection.observation.EntityID != devices.EntityID(testEntityID) ||
			string(projection.observation.Value) != "true" || projection.adapterID != "simulator" ||
			projection.runtimeID != devices.RuntimeID(testRuntimeID) ||
			projection.observedAt.IsZero() || projection.observedAt.Location() != time.UTC {
			t.Fatalf("projection = %#v", projection)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for observation projection")
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
	if _, publishErr := js.PublishMsg(context.Background(), message); publishErr != nil {
		t.Fatal(publishErr)
	}
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 2 && info.NumAckPending == 0
	})
	select {
	case unexpected := <-projections:
		t.Fatalf("malformed message reached projector: %#v", unexpected)
	default:
	}

	invalidSourceUpdatedAt := "2026-08-22t12:34:56Z"
	publishObservationEnvelopeWithSource(t, js, testThirdObservationID, `false`, &invalidSourceUpdatedAt)
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.NumAckPending == 0 && info.AckFloor.Consumer >= 3
	})
	select {
	case unexpected := <-projections:
		t.Fatalf("unparseable source_updated_at reached projector: %#v", unexpected)
	default:
	}
	if output := logs.String(); !strings.Contains(output, "parse source_updated_at") ||
		!strings.Contains(output, testThirdObservationID) {
		t.Fatalf("source_updated_at log = %s", output)
	}

	publishObservationEnvelope(t, js, testFourthObservationID, `false`)
	select {
	case <-projections:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for transiently failed observation projection")
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

func TestDomainObservationCopiesWireDataAndPointers(t *testing.T) {
	t.Parallel()
	commandID := "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	wire := natswire.Envelope[observation]{
		ID: testObservationID,
		Data: observation{
			EntityID: testEntityID, Value: json.RawMessage(`true`), RefreshForCommand: &commandID,
		},
	}
	sourceUpdatedAt := time.Date(2026, 8, 20, 12, 34, 56, 0, time.UTC)
	mapped, err := domainObservation(wire, sourceUpdatedAt.Add(time.Second), &sourceUpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	wire.Data.Value[0] = 'x'
	commandID = "changed"
	sourceUpdatedAt = time.Time{}
	if string(mapped.Value) != "true" || mapped.RefreshForCommand == nil ||
		*mapped.RefreshForCommand != devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab") ||
		mapped.SourceUpdatedAt == nil || mapped.SourceUpdatedAt.IsZero() {
		t.Fatalf("mapped observation = %#v", mapped)
	}
}

func publishObservationEnvelope(t *testing.T, js jetstream.JetStream, observationID, value string) {
	t.Helper()
	publishObservationEnvelopeWithSource(t, js, observationID, value, nil)
}

func publishObservationEnvelopeWithSource(
	t *testing.T,
	js jetstream.JetStream,
	observationID, value string,
	sourceUpdatedAt *string,
) {
	t.Helper()
	emittedAt := time.Now().UTC()
	adapterReceivedAt := emittedAt.Add(2 * time.Minute)
	envelope := natswire.Envelope[observation]{
		ID: observationID, Schema: contractsv1.ObservationSchemaID,
		EmittedAt: emittedAt.Format(time.RFC3339Nano), CorrelationID: testCorrelationID,
		Data: observation{
			EntityID: testEntityID, Value: json.RawMessage(value),
			AdapterReceivedAt: adapterReceivedAt.Format(time.RFC3339Nano), SourceUpdatedAt: sourceUpdatedAt,
		},
	}
	validator, compileErr := contractsv1.Compile()
	if compileErr != nil {
		t.Fatal(compileErr)
	}
	payload, encodeErr := natswire.Encode(validator, contractsv1.ObservationSchemaID, envelope)
	if encodeErr != nil {
		t.Fatal(encodeErr)
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
	subject, err := natswire.ObservationSubject("simulator", testRuntimeID, testEntityID)
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
