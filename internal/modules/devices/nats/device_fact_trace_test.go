package nats //nolint:testpackage // Tests exercise package-private transport trace capture.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// TestDeviceFactTraceFromHeadersCapturesBoundedPrintableASCIIOnly pins the
// capture rule: exactly the two W3C propagation headers are read, a bounded
// printable value is carried unchanged, and any field devices could not persist
// is dropped instead of being stored, truncated or failed.
func TestDeviceFactTraceFromHeadersCapturesBoundedPrintableASCIIOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		traceparent string
		tracestate  string
		want        devices.DeviceFactTraceContext
	}{
		{
			name:        "canonical context is carried unchanged",
			traceparent: testFactTraceparent,
			tracestate:  testFactTracestate,
			want: devices.DeviceFactTraceContext{
				Traceparent: testFactTraceparent,
				Tracestate:  testFactTracestate,
			},
		},
		{
			name:        "absent headers capture nothing",
			traceparent: "",
			tracestate:  "",
			want:        devices.DeviceFactTraceContext{},
		},
		{
			name:        "oversized traceparent is dropped and tracestate kept",
			traceparent: strings.Repeat("a", 129),
			tracestate:  testFactTracestate,
			want:        devices.DeviceFactTraceContext{Tracestate: testFactTracestate},
		},
		{
			name:        "oversized tracestate is dropped and traceparent kept",
			traceparent: testFactTraceparent,
			tracestate:  strings.Repeat("b", 513),
			want:        devices.DeviceFactTraceContext{Traceparent: testFactTraceparent},
		},
		{
			name:        "non-printable traceparent is dropped",
			traceparent: "00-\x00-trace",
			tracestate:  testFactTracestate,
			want:        devices.DeviceFactTraceContext{Tracestate: testFactTracestate},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			headers := natsgo.Header{}
			if test.traceparent != "" {
				headers.Set(traceparentHeaderKey, test.traceparent)
			}
			if test.tracestate != "" {
				headers.Set(tracestateHeaderKey, test.tracestate)
			}
			// An unrelated header must never be captured.
			headers.Set("x-arbitrary-header", "must-not-be-persisted")
			if captured := deviceFactTraceFromHeaders(headers); captured != test.want {
				t.Fatalf("captured trace = %#v, want %#v", captured, test.want)
			}
		})
	}
}

// TestObservationConsumerCapturesTraceContextFromHeaders proves the transport
// reads the inbound traceparent and tracestate off the message and hands them to
// the domain Observation, which is the only path the durable Device Fact can
// continue.
func TestObservationConsumerCapturesTraceContextFromHeaders(t *testing.T) {
	t.Parallel()
	_, _, js := startJetStream(t)
	consumer, err := ProvisionObservationResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	captured := make(chan devices.DeviceFactTraceContext, 1)
	running, err := StartObservationConsumer(
		context.Background(),
		consumer,
		testDeviceFactValidator(t),
		projectorFunc(func(
			_ context.Context,
			_ string,
			_ devices.RuntimeID,
			observation devices.Observation,
			_ time.Time,
		) (devices.ProjectionResult, error) {
			captured <- observation.Trace
			return devices.ProjectionResult{}, nil
		}),
		discardLogger(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)

	publishObservationWithTraceHeaders(t, js)
	select {
	case trace := <-captured:
		want := devices.DeviceFactTraceContext{
			Traceparent: testFactTraceparent,
			Tracestate:  testFactTracestate,
		}
		if trace != want {
			t.Fatalf("captured observation trace = %#v, want %#v", trace, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the observation projection")
	}
}

// TestEntityEventConsumerCapturesTraceContextFromHeaders proves the same
// capture on the Entity Event transport mapping.
func TestEntityEventConsumerCapturesTraceContextFromHeaders(t *testing.T) {
	t.Parallel()
	entityID := mustEntityID(t)
	_, _, js := startJetStream(t)
	consumer, err := ProvisionEntityEventResources(context.Background(), js)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &testEntityEventRecorder{}
	running, err := StartEntityEventConsumer(
		context.Background(), consumer, testDeviceFactValidator(t), recorder, discardLogger(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(running.Stop)

	eventID := mustEntityEventID(t)
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{string(eventID)}}
	headers.Set(traceparentHeaderKey, testFactTraceparent)
	headers.Set(tracestateHeaderKey, testFactTracestate)
	publishEntityEventMessage(
		t,
		js,
		mustEntityEventSubject(t, entityID),
		headers,
		entityEventEnvelope(t, string(eventID), entityID, "single_press", time.Now().UTC()),
	)
	waitForConsumer(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Consumer >= 1 && info.NumAckPending == 0
	})
	recorded := recorder.recorded()
	if len(recorded) != 1 {
		t.Fatalf("recorded events = %d, want 1", len(recorded))
	}
	want := devices.DeviceFactTraceContext{
		Traceparent: testFactTraceparent,
		Tracestate:  testFactTracestate,
	}
	if recorded[0].Trace != want {
		t.Fatalf("captured entity event trace = %#v, want %#v", recorded[0].Trace, want)
	}
}

// publishObservationWithTraceHeaders publishes one schema-valid Observation
// envelope carrying an explicit durable trace context, exactly as the SDK does
// when its publication context holds a sampled span.
func publishObservationWithTraceHeaders(t *testing.T, js jetstream.JetStream) {
	t.Helper()
	validator := testDeviceFactValidator(t)
	emittedAt := time.Now().UTC()
	envelope := natswire.Envelope[observation]{
		ID: testObservationID, Schema: contractsv1.ObservationSchemaID,
		EmittedAt: emittedAt.Format(time.RFC3339Nano), CorrelationID: testCorrelationID,
		Data: observation{
			EntityID:          testEntityID,
			Value:             json.RawMessage(`true`),
			AdapterReceivedAt: emittedAt.Format(time.RFC3339Nano),
		},
	}
	payload, err := natswire.Encode(validator, contractsv1.ObservationSchemaID, envelope)
	if err != nil {
		t.Fatal(err)
	}
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{testObservationID}}
	headers.Set(traceparentHeaderKey, testFactTraceparent)
	headers.Set(tracestateHeaderKey, testFactTracestate)
	if _, publishErr := js.PublishMsg(context.Background(), &natsgo.Msg{
		Subject: mustObservationSubject(t), Header: headers, Data: payload,
	}); publishErr != nil {
		t.Fatal(publishErr)
	}
}
