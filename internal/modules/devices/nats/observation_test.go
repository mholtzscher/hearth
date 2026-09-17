package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
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
	logs, observer, logger := newContextObservingSink(observedSpanContext)
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
			projection.observation.CorrelationID != devices.CorrelationID(testCorrelationID) ||
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
	if output := logs.output(); !strings.Contains(output, "adapter observation clock is ahead") ||
		!strings.Contains(output, testObservationID) || !strings.Contains(output, testEntityID) {
		t.Fatalf("clock-skew log = %s", output)
	}
	clockSkew := logEvents(logs.records(t), "observation.clock_skew")
	if len(clockSkew) != 1 {
		t.Fatalf("observation.clock_skew events = %d, want 1:\n%s", len(clockSkew), logs.output())
	}
	if clockSkew[0]["level"] != "WARN" {
		t.Fatalf("clock-skew level = %#v, want WARN", clockSkew[0]["level"])
	}
	if clockSkew[0]["observation_id"] != testObservationID || clockSkew[0]["entity_id"] != testEntityID ||
		clockSkew[0]["adapter_id"] != "simulator" {
		t.Fatalf("clock-skew record = %#v", clockSkew[0])
	}

	maliciousHeaders := natsgo.Header{natsgo.MsgIdHdr: []string{testSecondObservationID}}
	natswire.InjectTrace(testTraceContext(t), maliciousHeaders)
	message := &natsgo.Msg{
		Subject: mustObservationSubject(t),
		Header:  maliciousHeaders,
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
	if output := logs.output(); !strings.Contains(output, "source_updated_at_parse_failed") ||
		!strings.Contains(output, testThirdObservationID) {
		t.Fatalf("source_updated_at log = %s", output)
	}
	invalid := logEvents(logs.records(t), "observation.invalid")
	if len(invalid) == 0 {
		t.Fatalf("observation.invalid events = 0, want at least 2:\n%s", logs.output())
	}
	for _, record := range invalid {
		if record["level"] != "WARN" {
			t.Fatalf("invalid level = %#v, want WARN (record = %#v)", record["level"], record)
		}
		if _, ok := record["error_code"]; !ok {
			t.Fatalf("invalid record lacks error_code (record = %#v)", record)
		}
		if _, ok := record["subject"]; ok {
			t.Fatalf("invalid record logs full subject (record = %#v)", record)
		}
		if _, ok := record["error"]; ok {
			t.Fatalf("invalid record logs raw error (record = %#v)", record)
		}
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
	// The projection channel above fires inside the projector, before the
	// handler logs the failure, so poll for the log record explicitly.
	commitDeadline := time.Now().Add(3 * time.Second)
	var commitFailed []map[string]any
	for time.Now().Before(commitDeadline) {
		commitFailed = logEvents(logs.records(t), "observation.processing_failed")
		if len(commitFailed) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(commitFailed) != 1 {
		t.Fatalf("observation.processing_failed events = %d, want 1:\n%s", len(commitFailed), logs.output())
	}
	if commitFailed[0]["level"] != "ERROR" || commitFailed[0]["stage"] != "commit" ||
		commitFailed[0]["error_code"] != "projection_failed" ||
		commitFailed[0]["observation_id"] != testFourthObservationID {
		t.Fatalf("commit failure record = %#v", commitFailed[0])
	}
	for _, key := range []string{"subject", "error"} {
		if _, ok := commitFailed[0][key]; ok {
			t.Fatalf("commit failure record logs unsafe %q (record = %#v)", key, commitFailed[0])
		}
	}

	if !observer.allObserved() {
		t.Fatal("observation log emission lost the extracted operation context")
	}
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
		ID: testObservationID, CorrelationID: testCorrelationID,
		Data: observation{
			EntityID: testEntityID, Value: json.RawMessage(`true`), RefreshForCommand: &commandID,
		},
	}
	sourceUpdatedAt := time.Date(2026, 8, 20, 12, 34, 56, 0, time.UTC)
	trace := devices.DeviceFactTraceContext{Traceparent: testFactTraceparent, Tracestate: testFactTracestate}
	mapped, err := domainObservation(wire, sourceUpdatedAt.Add(time.Second), &sourceUpdatedAt, trace)
	if err != nil {
		t.Fatal(err)
	}
	wire.Data.Value[0] = 'x'
	commandID = "changed"
	sourceUpdatedAt = time.Time{}
	if string(mapped.Value) != "true" || mapped.RefreshForCommand == nil ||
		*mapped.RefreshForCommand != devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab") ||
		mapped.CorrelationID != devices.CorrelationID(testCorrelationID) ||
		mapped.SourceUpdatedAt == nil || mapped.SourceUpdatedAt.IsZero() ||
		mapped.Trace != trace {
		t.Fatalf("mapped observation = %#v", mapped)
	}

	// A wire correlation is a canonical identity: mapping must reject a value
	// that is not one, because the accepted Observation fact carries it.
	malformed := wire
	malformed.CorrelationID = "not-a-correlation"
	malformed.Data.RefreshForCommand = nil
	if mapped2, malformedErr := domainObservation(
		malformed, sourceUpdatedAt.Add(time.Second), nil, devices.DeviceFactTraceContext{},
	); malformedErr == nil {
		t.Fatalf("malformed wire correlation was mapped as %q", mapped2.CorrelationID)
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
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{observationID}}
	natswire.InjectTrace(testTraceContext(t), headers)
	message := &natsgo.Msg{
		Subject: mustObservationSubject(t),
		Header:  headers,
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
	server := natstest.StartServer(t)
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

const testLinkedCommandID = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"

// This test protects observation disposition diagnostics and fails if a
// committed applied/unchanged/duplicate/rejected result is missing, uses the
// wrong level, or omits disposition, rejection, or command linkage.
func TestObservationConsumerLogsProjectedDispositions(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, err := ProvisionObservationResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	logs, logger := newTestLogSink()
	rejectedObservationID := "obs_01890f47-7a6b-7c4d-8e9f-0123456789af"
	running, err := StartObservationConsumer(context.Background(), consumer, validator, projectorFunc(func(
		_ context.Context,
		_ string,
		_ devices.RuntimeID,
		observation devices.Observation,
		_ time.Time,
	) (devices.ProjectionResult, error) {
		switch observation.ID {
		case devices.ObservationID(testObservationID):
			return devices.ProjectionResult{Disposition: devices.DispositionApplied}, nil
		case devices.ObservationID(testSecondObservationID):
			return devices.ProjectionResult{Disposition: devices.DispositionUnchanged}, nil
		case devices.ObservationID(testThirdObservationID):
			return devices.ProjectionResult{Disposition: devices.DispositionDuplicate}, nil
		case devices.ObservationID(testFourthObservationID):
			return devices.ProjectionResult{Disposition: devices.DispositionApplied}, nil
		default:
			rejection := devices.RejectionEntityDisabled
			return devices.ProjectionResult{Disposition: devices.DispositionRejected, Rejection: &rejection}, nil
		}
	}), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)

	publishObservationEnvelope(t, js, testObservationID, `"sentinel-projection-value"`)
	publishObservationEnvelope(t, js, testSecondObservationID, `"sentinel-projection-value"`)
	publishObservationEnvelope(t, js, testThirdObservationID, `"sentinel-projection-value"`)
	publishLinkedObservationEnvelope(t, js, testFourthObservationID, `"sentinel-projection-value"`)
	publishObservationEnvelope(t, js, rejectedObservationID, `"sentinel-projection-value"`)
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 5 && info.NumAckPending == 0
	})

	deadline := time.Now().Add(3 * time.Second)
	var projected []map[string]any
	for time.Now().Before(deadline) {
		projected = logEvents(logs.records(t), "observation.projected")
		if len(projected) == 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(projected) != 5 {
		t.Fatalf("observation.projected events = %d, want 5:\n%s", len(projected), logs.output())
	}
	byID := indexProjectedByObservationID(t, projected)
	for observationID, disposition := range map[string]string{
		testObservationID:       string(devices.DispositionApplied),
		testSecondObservationID: string(devices.DispositionUnchanged),
		testThirdObservationID:  string(devices.DispositionDuplicate),
		testFourthObservationID: string(devices.DispositionApplied),
		rejectedObservationID:   string(devices.DispositionRejected),
	} {
		requireProjectedDisposition(t, byID, observationID, disposition)
	}
	requireLinkedProjection(t, byID[testFourthObservationID])
	if rejected := byID[rejectedObservationID]; rejected["rejection_code"] != string(devices.RejectionEntityDisabled) {
		t.Fatalf("rejected projected record = %#v", rejected)
	}
	if plain := byID[testObservationID]; plain["command_id"] != nil || plain["rejection_code"] != nil {
		t.Fatalf("unlinked projected record carries linkage (record = %#v)", plain)
	}
	if failures := logEvents(logs.records(t), "observation.processing_failed"); len(failures) != 0 {
		t.Fatalf("observation.processing_failed events = %d, want 0:\n%s", len(failures), logs.output())
	}
	if output := logs.output(); strings.Contains(output, "sentinel-projection-value") {
		t.Fatalf("observation logs contain State values:\n%s", output)
	}
}

// This test protects sensitive-data handling and fails if State values,
// malicious protocol bodies, or raw error text reach any log record while
// still requiring useful error codes and safe IDs.
func TestObservationConsumerNeverLogsSensitivePayloads(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, err := ProvisionObservationResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	logs, logger := newTestLogSink()
	projectedValues := make(chan string, 1)
	running, err := StartObservationConsumer(context.Background(), consumer, validator, projectorFunc(func(
		_ context.Context,
		_ string,
		_ devices.RuntimeID,
		observation devices.Observation,
		_ time.Time,
	) (devices.ProjectionResult, error) {
		projectedValues <- string(observation.Value)
		return devices.ProjectionResult{Disposition: devices.DispositionApplied}, nil
	}), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)

	publishObservationEnvelope(t, js, testObservationID, `"s3cr3t-state-value"`)
	malicious := &natsgo.Msg{
		Subject: mustObservationSubject(t),
		Header:  natsgo.Header{natsgo.MsgIdHdr: []string{testSecondObservationID}},
		Data:    []byte(`{"token":"s3cr3t-bearer-token","value":"s3cr3t-body"}`),
	}
	if _, publishErr := js.PublishMsg(context.Background(), malicious); publishErr != nil {
		t.Fatal(publishErr)
	}
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 2 && info.NumAckPending == 0
	})

	select {
	case value := <-projectedValues:
		if value != `"s3cr3t-state-value"` {
			t.Fatalf("projected value = %q", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for observation projection")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(logEvents(logs.records(t), "observation.invalid")) == 1 &&
			len(logEvents(logs.records(t), "observation.projected")) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	invalid := logEvents(logs.records(t), "observation.invalid")
	if len(invalid) != 1 {
		t.Fatalf("observation.invalid events = %d, want 1:\n%s", len(invalid), logs.output())
	}
	if invalid[0]["error_code"] != "observation_decode_failed" {
		t.Fatalf("invalid record = %#v", invalid[0])
	}
	if _, ok := invalid[0]["payload_size"]; !ok {
		t.Fatalf("invalid record lacks payload_size (record = %#v)", invalid[0])
	}
	for _, key := range []string{"subject", "error"} {
		if _, ok := invalid[0][key]; ok {
			t.Fatalf("invalid record logs unsafe %q (record = %#v)", key, invalid[0])
		}
	}
	if output := logs.output(); strings.Contains(output, "s3cr3t-state-value") ||
		strings.Contains(output, "s3cr3t-bearer-token") || strings.Contains(output, "s3cr3t-body") {
		t.Fatalf("observation logs contain sensitive payloads:\n%s", output)
	}
}

func indexProjectedByObservationID(
	t *testing.T,
	projected []map[string]any,
) map[string]map[string]any {
	t.Helper()
	byID := make(map[string]map[string]any)
	for _, record := range projected {
		if record["level"] != "DEBUG" {
			t.Fatalf("projected level = %#v, want DEBUG (record = %#v)", record["level"], record)
		}
		id, _ := record["observation_id"].(string)
		byID[id] = record
		if record["adapter_id"] != "simulator" || record["entity_id"] != testEntityID {
			t.Fatalf("projected record = %#v", record)
		}
	}
	return byID
}

func requireProjectedDisposition(
	t *testing.T,
	byID map[string]map[string]any,
	observationID, disposition string,
) {
	t.Helper()
	record := byID[observationID]
	if record == nil {
		t.Fatalf("missing projected record for %s: %#v", observationID, byID)
	}
	if record["disposition"] != disposition {
		t.Fatalf("disposition for %s = %#v, want %q", observationID, record["disposition"], disposition)
	}
}

func requireLinkedProjection(t *testing.T, linked map[string]any) {
	t.Helper()
	if linked["command_id"] != testLinkedCommandID || linked["correlation_id"] != testCorrelationID {
		t.Fatalf("linked projected record = %#v", linked)
	}
}

func publishLinkedObservationEnvelope(t *testing.T, js jetstream.JetStream, observationID, value string) {
	t.Helper()
	emittedAt := time.Now().UTC()
	commandID := testLinkedCommandID
	envelope := natswire.Envelope[observation]{
		ID: observationID, Schema: contractsv1.ObservationSchemaID,
		EmittedAt: emittedAt.Format(time.RFC3339Nano), CorrelationID: testCorrelationID,
		CausationID: &commandID,
		Data: observation{
			EntityID: testEntityID, Value: json.RawMessage(value),
			AdapterReceivedAt: emittedAt.Format(time.RFC3339Nano), RefreshForCommand: &commandID,
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
	linkedHeaders := natsgo.Header{natsgo.MsgIdHdr: []string{observationID}}
	natswire.InjectTrace(testTraceContext(t), linkedHeaders)
	message := &natsgo.Msg{
		Subject: mustObservationSubject(t),
		Header:  linkedHeaders,
		Data:    payload,
	}
	if _, err := js.PublishMsg(context.Background(), message); err != nil {
		t.Fatal(err)
	}
}
