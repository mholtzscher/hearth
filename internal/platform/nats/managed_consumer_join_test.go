package nats_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

// startupTerminationConsumer models nats.go returning an already-closed signal
// for an invalid subscription while a callback started during Consume is live.
type startupTerminationConsumer struct {
	jetstream.Consumer

	callbackStarted <-chan struct{}
	errorCallback   bool
}

func (consumer *startupTerminationConsumer) Consume(
	handler jetstream.MessageHandler,
	options ...jetstream.PullConsumeOpt,
) (jetstream.ConsumeContext, error) {
	closed := make(chan struct{})
	close(closed)
	consume := &invalidConsumeContext{closed: closed}
	if consumer.errorCallback {
		for _, option := range options {
			if errorHandler, ok := option.(jetstream.ConsumeErrHandler); ok {
				go errorHandler(consume, errors.New("subscription failed"))
			}
		}
	} else {
		go handler(nil)
	}
	select {
	case <-consumer.callbackStarted:
		return consume, nil
	case <-time.After(testLifecycleTimeout):
		return nil, errors.New("startup callback did not enter")
	}
}

type invalidConsumeContext struct {
	closed <-chan struct{}
}

func (*invalidConsumeContext) Stop()                           {}
func (*invalidConsumeContext) Drain()                          {}
func (consume *invalidConsumeContext) Closed() <-chan struct{} { return consume.closed }

// Drain must join callbacks even if upstream closure is observed too late to
// provide that guarantee. It fails if the primitive trusts Closed alone.
func TestConsumerJoinsCallbacksAfterStartupTermination(t *testing.T) {
	t.Parallel()
	for _, errorCallback := range []bool{false, true} {
		name := "message"
		if errorCallback {
			name = "consume error"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			t.Cleanup(unblock)
			callback := func() {
				close(started)
				<-release
			}
			consumer, err := platformnats.StartConsumer(
				&startupTerminationConsumer{callbackStarted: started, errorCallback: errorCallback},
				func(jetstream.Msg) { callback() },
				platformnats.ConsumerOptions{OnConsumeError: func(error) { callback() }},
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if drainErr := consumer.Drain(ctx); !errors.Is(drainErr, context.DeadlineExceeded) {
				t.Fatalf("Drain while startup callback is live = %v, want deadline exceeded", drainErr)
			}
			assertStillOpen(t, consumer.Closed(), "Closed bypassed the startup callback")
			unblock()
			awaitClosed(t, consumer)
		})
	}
}
