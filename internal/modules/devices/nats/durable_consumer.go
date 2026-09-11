package nats

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/nats-io/nats.go/jetstream"
)

// durableConsumer is the shared lifecycle of one JetStream durable Core
// consumer: subscription, activity reporting, and shutdown. Entity Event and
// Observation consumers embed it and supply only their own message handler, so
// neither restates subscribe, activity, or drain mechanics.
type durableConsumer struct {
	consume jetstream.ConsumeContext
	active  atomic.Bool
}

// startDurableConsumer subscribes handler to one durable consumer and starts
// reporting activity. class names the consumer in error and diagnostic text,
// and handler receives the resolved base context and logger so no message can
// lose the operation context or log through a nil logger.
func startDurableConsumer(
	baseContext context.Context,
	consumer jetstream.Consumer,
	class consumerClass,
	logger *slog.Logger,
	handler func(context.Context, *slog.Logger, jetstream.Msg),
) (*durableConsumer, error) {
	logger = defaultLogger(logger)
	if baseContext == nil {
		baseContext = context.Background()
	}

	running := &durableConsumer{}
	consume, err := consumer.Consume(
		func(message jetstream.Msg) {
			handler(baseContext, logger, message)
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, _ error) {
			logger.ErrorContext(baseContext, fmt.Sprintf("%s consume error", class.kind),
				slog.String(transportEventKey, class.failureEvent),
				slog.String("stage", "consume"),
				slog.String(transportErrorCodeKey, "consumer_error"),
			)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("start %s consumer: %w", class.kind, err)
	}
	running.consume = consume
	running.active.Store(true)
	go func() {
		<-consume.Closed()
		running.active.Store(false)
	}()
	return running, nil
}

// Active reports whether the subscription is still consuming. Stop and Drain
// clear it before teardown begins, so readiness fails as soon as shutdown
// starts rather than after it finishes.
func (consumer *durableConsumer) Active() bool {
	return consumer != nil && consumer.active.Load()
}

// Stop ends consumption immediately.
func (consumer *durableConsumer) Stop() {
	if consumer == nil || consumer.consume == nil {
		return
	}
	consumer.active.Store(false)
	consumer.consume.Stop()
}

// Drain ends consumption after in-flight callbacks finish.
func (consumer *durableConsumer) Drain() {
	if consumer == nil || consumer.consume == nil {
		return
	}
	consumer.active.Store(false)
	consumer.consume.Drain()
}

// Closed reports subscription termination. A consumer that never subscribed is
// already closed.
func (consumer *durableConsumer) Closed() <-chan struct{} {
	if consumer == nil || consumer.consume == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return consumer.consume.Closed()
}
