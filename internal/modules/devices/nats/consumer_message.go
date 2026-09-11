package nats

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
)

// consumerClass is the wire vocabulary one durable consumer owns, so shared
// transport code can emit its diagnostics without knowing its domain. Each
// consumer defines its class with whole event literals, so searching an event
// name finds the consumer that owns it.
type consumerClass struct {
	kind         string // names the consumer in error and discard text
	invalidEvent string // permanent wire-invalid input
	failureEvent string // processing failures, including metadata and ack
	idKey        string // structured field carrying the report ID
}

// consumerMessage is the shared prologue of one durable consumer message: the
// operation context extracted from headers, the JetStream metadata, and the
// closure that acknowledges permanently invalid input.
type consumerMessage struct {
	operation context.Context
	metadata  *jetstream.MsgMetadata
	reject    func(errorCode, id string)
}

// openConsumerMessage extracts the operation context and metadata that every
// durable consumer message needs. It reports false after logging when the
// message carries no metadata, and otherwise returns the closure that
// acknowledges wire-invalid input for this message.
func openConsumerMessage(
	baseContext context.Context,
	message jetstream.Msg,
	logger *slog.Logger,
	class consumerClass,
) (consumerMessage, bool) {
	// Extract the operation context from headers before decoding so every
	// emission below, including permanent invalid input, preserves it.
	ctx := natswire.ExtractTrace(baseContext, message.Headers())
	metadata, metadataErr := message.Metadata()
	if metadataErr != nil {
		logger.ErrorContext(ctx, fmt.Sprintf("cannot read %s metadata", class.kind),
			slog.String(transportEventKey, class.failureEvent),
			slog.String("stage", "metadata"),
			slog.String(transportErrorCodeKey, "metadata_unavailable"),
		)
		return consumerMessage{}, false
	}
	return consumerMessage{
		operation: ctx,
		metadata:  metadata,
		reject: func(errorCode, id string) {
			acknowledgeInvalidInput(ctx, logger, message, metadata, class, errorCode, id)
		},
	}, true
}

// acknowledgeInvalidInput acknowledges permanent wire-invalid input before
// recording it at Warn with a fixed validation class and safe sizes and IDs.
// Raw payloads and decode errors are never logged. The warning is retained
// regardless of the Ack outcome, with Ack failures recorded.
func acknowledgeInvalidInput(
	ctx context.Context,
	logger *slog.Logger,
	message jetstream.Msg,
	metadata *jetstream.MsgMetadata,
	class consumerClass,
	errorCode, id string,
) {
	ackErr := message.Ack()
	scoped := logger.With(slog.Uint64("stream_sequence", metadata.Sequence.Stream))
	attributes := []slog.Attr{
		slog.Int("payload_size", len(message.Data())),
		slog.String(transportEventKey, class.invalidEvent),
		slog.String(transportErrorCodeKey, errorCode),
	}
	if id != "" {
		attributes = append(attributes, slog.String(class.idKey, id))
	}
	scoped.LogAttrs(ctx, slog.LevelWarn, fmt.Sprintf("acknowledging invalid %s", class.kind), attributes...)
	if ackErr != nil {
		ackAttributes := []slog.Attr{
			slog.String(transportEventKey, class.failureEvent),
			slog.String("stage", "ack"),
			slog.String(transportErrorCodeKey, "ack_failed"),
		}
		if id != "" {
			ackAttributes = append(ackAttributes, slog.String(class.idKey, id))
		}
		scoped.LogAttrs(ctx, slog.LevelError, fmt.Sprintf("acknowledge invalid %s", class.kind), ackAttributes...)
	}
}
