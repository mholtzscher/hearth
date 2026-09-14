// Package nats manages JetStream consumer lifecycle. Modules own message
// handling, diagnostics, and admission policy.
package nats

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

// ConsumerOptions supplies module-owned lifecycle hooks. Hooks must not wait on
// this consumer's Drain or Closed, which join their execution.
type ConsumerOptions struct {
	// OnConsumeError runs on nats.go's error-reporting goroutine. Reporting an
	// error does not itself terminate delivery; message handling is separate.
	OnConsumeError func(err error)
	// OnUnexpectedTermination runs once on the termination watcher unless
	// intentional shutdown was observed first. Closed waits for its return.
	// The hook must not wait on this consumer's Drain or Closed.
	OnUnexpectedTermination func()
}

// Consumer manages one JetStream subscription without restarting it. Do not
// copy it after use. Its zero value is inert.
//
// nats.go v1.53.1 makes the first Stop or Drain request win. Both eventually
// stop the underlying subscription; Drain also processes buffered messages.
// Callbacks are tracked separately because a late Closed call on an invalid
// subscription can report completion before its callback returns.
// Broker deletion between pull requests may
// not close the consume context, even though heartbeat errors are reported.
// Active therefore reports lifecycle state, not broker health.
type Consumer struct {
	consume jetstream.ConsumeContext
	options ConsumerOptions
	// callbacks protects the join when nats.go reports an invalid subscription
	// as closed before a running callback has returned.
	callbacks *lifecycle.AdmissionGroup

	// active clears when shutdown begins or termination is observed.
	active atomic.Bool
	// The watcher's single Load decides shutdown-versus-fault races. A later
	// Stop or Drain cannot retract an already-selected termination hook.
	intentional atomic.Bool
	// closed joins the watcher and its hook; nil means never subscribed.
	closed chan struct{}
}

// StartConsumer subscribes to an already-provisioned consumer and watches its
// termination. Nil inputs and subscription failures return no managed consumer.
func StartConsumer(
	consumer jetstream.Consumer,
	handler func(jetstream.Msg),
	options ConsumerOptions,
) (*Consumer, error) {
	if consumer == nil {
		return nil, errors.New("managed consumer requires a jetstream consumer")
	}
	if handler == nil {
		return nil, errors.New("managed consumer requires a message handler")
	}
	managed := &Consumer{
		options: options, closed: make(chan struct{}),
		callbacks: lifecycle.NewAdmissionGroup(),
	}
	consume, err := consumer.Consume(
		func(message jetstream.Msg) {
			reservation, admitted := managed.callbacks.TryAcquire()
			if !admitted {
				return
			}
			defer reservation.Release()
			handler(message)
		},
		jetstream.ConsumeErrHandler(func(_ jetstream.ConsumeContext, consumeErr error) {
			reservation, admitted := managed.callbacks.TryAcquire()
			if !admitted {
				return
			}
			defer reservation.Release()
			if managed.options.OnConsumeError != nil {
				managed.options.OnConsumeError(consumeErr)
			}
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("start managed durable consumer: %w", err)
	}
	if consume == nil {
		return nil, errors.New("managed consumer started without a consume context")
	}
	managed.consume = consume
	managed.active.Store(true)
	// Register closure observation before callers can request shutdown.
	closed := consume.Closed()
	go managed.watchTermination(closed)
	return managed, nil
}

// watchTermination joins delivery and completes the fault decision before Closed.
func (consumer *Consumer) watchTermination(closed <-chan struct{}) {
	defer close(consumer.closed)
	<-closed
	consumer.active.Store(false)
	consumer.callbacks.CloseAdmission()
	_ = consumer.callbacks.Wait(context.Background())
	if consumer.intentional.Load() {
		return
	}
	if consumer.options.OnUnexpectedTermination != nil {
		consumer.options.OnUnexpectedTermination()
	}
}

// Active reports subscription activity, not broker health. Shutdown clears it
// before waiting for callbacks.
func (consumer *Consumer) Active() bool {
	return consumer != nil && consumer.active.Load()
}

// Stop requests shutdown without waiting for the current callback. If it wins
// against Drain, buffered messages are discarded. Repeated and concurrent calls
// are safe; Stop cannot interrupt a Drain that already started.
func (consumer *Consumer) Stop() {
	if consumer == nil || consumer.consume == nil {
		return
	}
	consumer.intentional.Store(true)
	consumer.active.Store(false)
	consumer.consume.Stop()
}

// Drain requests graceful shutdown and joins callbacks and the termination
// watcher. Repeated and concurrent calls are safe. If Stop already won, Drain
// only joins that shutdown; it cannot restore discarded buffered messages.
//
// A context error ends only this wait. Shutdown continues and can be joined
// again. Dependencies must remain alive while callbacks still use them.
func (consumer *Consumer) Drain(ctx context.Context) error {
	if consumer == nil || consumer.consume == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	consumer.intentional.Store(true)
	consumer.active.Store(false)
	consumer.consume.Drain()
	select {
	case <-consumer.closed:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("drain managed durable consumer: %w", ctx.Err())
	}
}

// Closed reports lifecycle completion. It closes only after delivery stopped,
// every callback returned, and the termination watcher finished its lifecycle
// handling. A consumer that never subscribed is already closed.
func (consumer *Consumer) Closed() <-chan struct{} {
	if consumer == nil || consumer.closed == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return consumer.closed
}
