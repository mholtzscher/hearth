package natswire

import (
	"context"
	"errors"
	"strings"

	natsgo "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"
)

// ValidateAdapterHealthHeaders rejects ambiguous identities and malformed W3C
// propagation rather than silently losing the retained publication metadata.
func ValidateAdapterHealthHeaders(headers natsgo.Header, id string) error {
	if err := validateHealthHeaderKeys(headers); err != nil {
		return err
	}
	if headers.Get(natsgo.MsgIdHdr) != id {
		return errors.New("health message identity mismatch")
	}
	parent, state := headers.Get("traceparent"), headers.Get("tracestate")
	if len(parent) > 512 || len(state) > 512 {
		return errors.New("health trace metadata too large")
	}
	if parent != "" && !trace.SpanContextFromContext(ExtractTrace(context.Background(), headers)).IsValid() {
		return errors.New("invalid health trace parent")
	}
	if state != "" {
		if parent == "" {
			return errors.New("health trace state without parent")
		}
		if _, err := trace.ParseTraceState(state); err != nil {
			return errors.New("invalid health trace state")
		}
	}
	return nil
}

func validateHealthHeaderKeys(headers natsgo.Header) error {
	for _, key := range []string{natsgo.MsgIdHdr, "traceparent", "tracestate"} {
		for supplied := range headers {
			if supplied != key && strings.EqualFold(supplied, key) {
				return errors.New("noncanonical health metadata header")
			}
		}
		if len(headers.Values(key)) > 1 {
			return errors.New("duplicate health metadata header")
		}
		if len(headers.Values(key)) == 1 && headers.Get(key) == "" {
			return errors.New("empty health metadata header")
		}
	}
	return nil
}
