// Package outbox publishes module-owned durable work over JetStream. It owns
// delivery and lifecycle mechanics, never enqueue transactions or wire schemas.
package outbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/platform/lifecycle"
)

const (
	defaultBatchSize        = 64
	defaultPollInterval     = 5 * time.Second
	defaultOperationTimeout = 5 * time.Second
)

// Source supplies ordered, immutable pending work. Read may return a valid older
// prefix with a permanent error; any transient read error must return no prefix.
// Prepare is deterministic and every preparation error is permanent. Delete is
// idempotent and is called only after publication into the configured stream.
type Source[T any] interface {
	Read(context.Context, int) ([]T, error)
	Prepare(T) (*natsgo.Msg, error)
	Delete(context.Context, T) error
}

// Publisher is the acknowledged publication capability the relay requires.
// A JetStream client implements it directly.
type Publisher interface {
	PublishMsg(context.Context, *natsgo.Msg, ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// Timing bounds each I/O operation, retry delay, and authoritative rediscovery.
// Both production consumers use DefaultTiming.
type Timing struct {
	BatchSize        int
	PollInterval     time.Duration
	RetryBackoff     time.Duration
	OperationTimeout time.Duration
}

func DefaultTiming() Timing {
	return Timing{
		BatchSize:        defaultBatchSize,
		PollInterval:     defaultPollInterval,
		RetryBackoff:     time.Second,
		OperationTimeout: defaultOperationTimeout,
	}
}

// DeliveryError describes one safe diagnostic. Cause is for classification, not raw
// logging. Item is nil for a read failure; Permanent failures stop publication.
type DeliveryError[T any] struct {
	Stage     string
	Code      string
	Item      *T
	Cause     error
	Permanent bool
}

func (failure *DeliveryError[T]) Error() string {
	return "outbox " + failure.Stage + ": " + failure.Code
}
func (failure *DeliveryError[T]) Unwrap() error { return failure.Cause }

// Config supplies the module boundary. IsPoison classifies permanent read errors;
// Report retains module-specific, payload-free diagnostics. The stream must
// already be provisioned.
type Config[T any] struct {
	Source   Source[T]
	Stream   string
	IsPoison func(error) bool
	Report   func(context.Context, DeliveryError[T])
	Timing   Timing
}

// Relay owns exactly one publisher through normal operation and drain. Active
// reports lifecycle activity; Fault reports the current recoverable delivery
// failure or permanent poison. Readiness requires Active and a nil Fault.
type Relay[T any] struct {
	publisher Publisher
	config    Config[T]
	worker    *lifecycle.WorkerHandle
	wake      chan struct{}
	drain     chan struct{}
	drainOnce sync.Once
	active    atomic.Bool
	faultMu   sync.Mutex
	fault     error
}

func Start[T any](publisher Publisher, config Config[T]) (*Relay[T], error) {
	if config.Source == nil || config.Stream == "" || publisher == nil || config.IsPoison == nil ||
		config.Report == nil {
		return nil, errors.New("outbox source, stream, publisher, poison classifier, and reporter required")
	}
	timing := config.Timing
	if timing.BatchSize < 1 || timing.PollInterval <= 0 || timing.RetryBackoff <= 0 || timing.OperationTimeout <= 0 {
		return nil, errors.New("outbox requires positive batch size and timing bounds")
	}
	relay := &Relay[T]{publisher: publisher, config: config, wake: make(chan struct{}, 1), drain: make(chan struct{})}
	relay.active.Store(true)
	relay.worker = lifecycle.StartWorker(context.Background(), relay.run)
	return relay, nil
}

// Notify is a nonblocking, lossy hint. The durable source and poll are authoritative.
func (relay *Relay[T]) Notify() {
	select {
	case relay.wake <- struct{}{}:
	default:
	}
}

func (relay *Relay[T]) Active() bool { return relay.active.Load() }
func (relay *Relay[T]) Fault() error {
	relay.faultMu.Lock()
	defer relay.faultMu.Unlock()
	return relay.fault
}
func (relay *Relay[T]) Closed() <-chan struct{} { return relay.worker.Closed() }

// Stop cancels and joins without attempting to empty the outbox. A wait timeout
// is not completion: dependencies must stay alive until Closed closes.
func (relay *Relay[T]) Stop(ctx context.Context) error {
	relay.active.Store(false)
	return relay.worker.Stop(ctx)
}

// Drain withdraws readiness and asks the original worker to publish until empty.
// It wakes idle/backoff waits but does not replace an in-flight operation. If the
// caller's context expires, it cancels the worker; a later Stop can finish joining.
// Concurrent calls never start another publisher or revive a stopped worker.
func (relay *Relay[T]) Drain(ctx context.Context) error {
	relay.active.Store(false)
	relay.drainOnce.Do(func() { close(relay.drain) })
	if err := relay.worker.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			_ = relay.worker.Stop(ctx)
		}
		return err
	}
	return nil
}

func (relay *Relay[T]) setFault(err error) {
	relay.faultMu.Lock()
	relay.fault = err
	relay.faultMu.Unlock()
}

func (relay *Relay[T]) run(ctx context.Context) error {
	defer relay.active.Store(false)
	draining := false
	for ctx.Err() == nil {
		err := relay.publishPending(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if err.Permanent {
				return err
			}
			// Only the first drain request interrupts a backoff; later failures
			// still wait, rather than spinning on a closed drain channel.
			draining = relay.wait(ctx, relay.config.Timing.RetryBackoff, draining, false)
			continue
		}
		relay.setFault(nil)
		select {
		case <-relay.drain:
			return nil
		default:
		}
		draining = relay.wait(ctx, relay.config.Timing.PollInterval, draining, true)
	}
	return ctx.Err()
}

func (relay *Relay[T]) wait(ctx context.Context, duration time.Duration, draining, hints bool) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	var wake, drain <-chan struct{}
	if hints {
		wake = relay.wake
	}
	if !draining {
		drain = relay.drain
	}
	select {
	case <-ctx.Done():
	case <-timer.C:
	case <-wake:
	case <-drain:
		draining = true
	}
	return draining
}

func (relay *Relay[T]) publishPending(ctx context.Context) *DeliveryError[T] {
	for ctx.Err() == nil {
		op, cancel := context.WithTimeout(ctx, relay.config.Timing.OperationTimeout)
		pending, err := relay.config.Source.Read(op, relay.config.Timing.BatchSize)
		cancel()
		if err != nil && !relay.config.IsPoison(err) {
			return relay.failure(ctx, "list", "list_failed", nil, err, false)
		}
		for _, item := range pending {
			if failure := relay.publishOne(ctx, item); failure != nil {
				return failure
			}
		}
		if err != nil {
			return relay.failure(ctx, "list", "invalid_row", nil, err, true)
		}
		if len(pending) == 0 {
			return nil
		}
	}
	return nil
}

func (relay *Relay[T]) publishOne(ctx context.Context, item T) *DeliveryError[T] {
	if err := ctx.Err(); err != nil {
		return &DeliveryError[T]{Stage: "publish", Code: "canceled", Cause: err}
	}
	message, err := relay.config.Source.Prepare(item)
	if err != nil {
		return relay.failure(ctx, "map", "invalid_message", &item, err, true)
	}
	if message == nil {
		return relay.failure(ctx, "map", "invalid_message", &item, errors.New("nil publication message"), true)
	}
	op, cancel := context.WithTimeout(ctx, relay.config.Timing.OperationTimeout)
	ack, err := relay.publisher.PublishMsg(op, message)
	cancel()
	if err != nil {
		return relay.failure(ctx, "publish", "publish_failed", &item, err, false)
	}
	if ack == nil {
		return relay.failure(ctx, "ack", "ack_missing", &item, errors.New("missing PubAck"), false)
	}
	if ack.Stream != relay.config.Stream {
		return relay.failure(ctx, "ack", "unexpected_stream", &item, errors.New("unexpected PubAck stream"), false)
	}
	op, cancel = context.WithTimeout(ctx, relay.config.Timing.OperationTimeout)
	err = relay.config.Source.Delete(op, item)
	cancel()
	if err != nil {
		return relay.failure(ctx, "delete", "delete_failed", &item, err, false)
	}
	relay.setFault(nil)
	return nil
}

func (relay *Relay[T]) failure(
	ctx context.Context,
	stage, code string,
	item *T,
	cause error,
	permanent bool,
) *DeliveryError[T] {
	failure := &DeliveryError[T]{Stage: stage, Code: code, Item: item, Cause: cause, Permanent: permanent}
	if ctx.Err() == nil {
		relay.setFault(failure)
		relay.config.Report(ctx, *failure)
	}
	return failure
}
