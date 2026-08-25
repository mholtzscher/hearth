package natswire

import (
	"context"
	"testing"

	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceContextRoundTrip(t *testing.T) {
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	})
	headers := make(natsgo.Header)
	InjectTrace(trace.ContextWithSpanContext(context.Background(), spanContext), headers)
	if headers.Get("traceparent") == "" {
		t.Fatal("traceparent was not injected")
	}
	extracted := trace.SpanContextFromContext(ExtractTrace(context.Background(), headers))
	if extracted.TraceID() != spanContext.TraceID() || extracted.SpanID() != spanContext.SpanID() {
		t.Fatalf("extracted span context = %v, want %v", extracted, spanContext)
	}
}
