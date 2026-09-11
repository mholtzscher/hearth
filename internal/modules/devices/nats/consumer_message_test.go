package nats //nolint:testpackage // Tests exercise package-private NATS message handling.

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// Core cannot place a report whose JetStream metadata it cannot read: without a
// stream sequence and receive time the shared prologue must drop the message
// without acknowledging it, so the stream redelivers it, and must log the
// consumer's own failure class rather than the invalid-input class.
func TestOpenConsumerMessageDropsUnreadableMetadata(t *testing.T) {
	t.Parallel()
	logs, logger := newTestLogSink()
	message := &ackOrderingTestMessage{metadataErr: errors.New("metadata unavailable")}

	if _, ok := openConsumerMessage(context.Background(), message, logger, observationClass()); ok {
		t.Fatal("unreadable metadata was accepted")
	}
	if message.ackCount() != 0 {
		t.Fatal("unreadable metadata was acknowledged")
	}
	failures := logEvents(logs.records(t), "observation.processing_failed")
	if len(failures) != 1 {
		t.Fatalf("observation.processing_failed events = %d, want 1:\n%s", len(failures), logs.output())
	}
	record := failures[0]
	if record["level"] != "ERROR" || record["stage"] != "metadata" ||
		record["error_code"] != "metadata_unavailable" {
		t.Fatalf("metadata failure record = %#v", record)
	}
	if invalid := logEvents(logs.records(t), "observation.invalid"); len(invalid) != 0 {
		t.Fatalf("observation.invalid events = %d, want 0:\n%s", len(invalid), logs.output())
	}
}

// A readable message keeps the whole prologue result: the trace extracted from
// headers, the stream metadata, and a rejection closure that acknowledges the
// wire-invalid report and records it under the consumer's invalid-input class.
func TestOpenConsumerMessageCarriesTraceAndRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	logs, logger := newTestLogSink()
	// The fake message already carries headers with an injected trace.
	message := newAckOrderingTestMessage(t, []byte(`{"value":true}`), testSecondObservationID, nil)

	opened, ok := openConsumerMessage(context.Background(), message, logger, observationClass())
	if !ok {
		t.Fatal("readable metadata was rejected")
	}
	if !trace.SpanContextFromContext(opened.operation).IsValid() {
		t.Fatal("prologue lost the extracted operation context")
	}
	if opened.metadata.Timestamp.IsZero() {
		t.Fatal("prologue lost the stream metadata")
	}
	opened.reject("observation_route_failed", testSecondObservationID)
	if message.ackCount() != 1 {
		t.Fatalf("wire-invalid report ack count = %d, want 1", message.ackCount())
	}
	invalid := logEvents(logs.records(t), "observation.invalid")
	if len(invalid) != 1 {
		t.Fatalf("observation.invalid events = %d, want 1:\n%s", len(invalid), logs.output())
	}
	if invalid[0]["level"] != "WARN" || invalid[0]["error_code"] != "observation_route_failed" ||
		invalid[0]["observation_id"] != testSecondObservationID {
		t.Fatalf("invalid record = %#v", invalid[0])
	}
}
