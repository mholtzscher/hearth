package nats

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go/jetstream"

	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

// startDurableConsumer adds class-specific diagnostics to the shared lifecycle
// and supplies the handler with a non-nil context and logger.
func startDurableConsumer(
	baseContext context.Context,
	consumer jetstream.Consumer,
	class consumerClass,
	logger *slog.Logger,
	handler func(context.Context, *slog.Logger, jetstream.Msg),
) (*platformnats.Consumer, error) {
	logger = defaultLogger(logger)
	if baseContext == nil {
		baseContext = context.Background()
	}
	managed, err := platformnats.StartConsumer(
		consumer,
		func(message jetstream.Msg) {
			handler(baseContext, logger, message)
		},
		platformnats.ConsumerOptions{
			OnConsumeError: func(_ error) {
				logger.ErrorContext(baseContext, fmt.Sprintf("%s consume error", class.kind),
					slog.String(transportEventKey, class.failureEvent),
					slog.String("stage", "consume"),
					slog.String(transportErrorCodeKey, "consumer_error"),
				)
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("start %s consumer: %w", class.kind, err)
	}
	return managed, nil
}
