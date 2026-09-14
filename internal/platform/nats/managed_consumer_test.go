package nats_test // The primitive is exercised through its exported API only.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

// testLifecycleTimeout bounds every wait for a lifecycle signal so a missed hook
// or a lifecycle that never finishes fails the test instead of hanging the suite.
const testLifecycleTimeout = 5 * time.Second

// stubConsumer is a jetstream.Consumer whose only configured behavior is
// Consume. It captures the handler, the installed consume-error handler, and the
// controllable ConsumeContext the primitive under test must manage.
type stubConsumer struct {
	jetstream.Consumer // Embedded nil interface: these tests implement only Consume.

	consumeContext *stubConsumeContext
	startErr       error
	consumeCalls   int
	errHandler     jetstream.ConsumeErrHandlerFunc
}

func newStubConsumer() *stubConsumer {
	return &stubConsumer{consumeContext: newStubConsumeContext()}
}

// Consume records every option it receives so a test can prove the primitive
// installed an error handler, then returns the controllable context.
func (stub *stubConsumer) Consume(
	handler jetstream.MessageHandler,
	opts ...jetstream.PullConsumeOpt,
) (jetstream.ConsumeContext, error) {
	stub.consumeCalls++
	for _, opt := range opts {
		if errOption, ok := opt.(jetstream.ConsumeErrHandler); ok {
			stub.errHandler = jetstream.ConsumeErrHandlerFunc(errOption)
		}
	}
	if stub.startErr != nil {
		return nil, stub.startErr
	}
	if stub.consumeContext == nil {
		//nolint:nilnil // A stub that breaks the jetstream.Consumer contract on purpose.
		return nil, nil
	}
	stub.consumeContext.handler = handler
	return stub.consumeContext, nil
}

// deliverMessage queues one message for the subscription dispatcher, exactly as
// nats.go queues a message the broker delivered to its asynchronous
// subscription. The primitive never inspects a message, so a nil message keeps
// these tests free of the full jetstream.Msg surface.
func (stub *stubConsumer) deliverMessage() {
	stub.consumeContext.enqueue(nil)
}

// consumeMode is the stub's model of the single subscription state nats.go
// linearizes Stop and Drain on: whichever call arrives first decides, and the
// later call is a complete no-op.
type consumeMode int32

const (
	// modeRunning delivers queued messages as they arrive.
	modeRunning consumeMode = iota
	// modeStop discards queued messages, lets the callback already executing
	// finish, and then completes.
	modeStop
	// modeDrain delivers queued messages, lets the callback already executing
	// finish, and then completes.
	modeDrain
	// modeTerminated is broker-side termination: delivery ends like Stop, but no
	// intentional Stop or Drain call was made.
	modeTerminated
)

// stubConsumeContext is a controllable jetstream.ConsumeContext that mirrors the
// nats.go v1.53.1 guarantees the primitive depends on. Those guarantees are
// properties of the one subscription Stop and Drain share, not of either call
// alone:
//
//   - Stop and Drain linearize on that state, so Stop cannot interrupt a Drain
//     and a Drain cannot turn a Stop into a drain. The losing call requests
//     nothing.
//   - Neither call returns a closed Closed. A callback already executing runs to
//     completion first, because nats.go observes the stop only when its
//     dispatcher next looks, and Closed closes when that dispatcher exits. Stop
//     still discards messages queued behind the executing callback; Drain
//     delivers them.
//   - A broker-side termination ends delivery the way Stop does, without either
//     call being recorded.
type stubConsumeContext struct {
	handler jetstream.MessageHandler

	mu     sync.Mutex
	cond   *sync.Cond
	queued []jetstream.Msg
	mode   consumeMode

	startOnce sync.Once
	closed    chan struct{}
	// stopRequested and drainRequested report which intentional call reached the
	// subscription. At most one of them closes, because the first call decides and
	// the later call is a no-op.
	stopRequested  chan struct{}
	drainRequested chan struct{}
}

func newStubConsumeContext() *stubConsumeContext {
	consume := &stubConsumeContext{
		closed:         make(chan struct{}),
		stopRequested:  make(chan struct{}),
		drainRequested: make(chan struct{}),
	}
	consume.cond = sync.NewCond(&consume.mu)
	return consume
}

// Stop mirrors nats.go: the call requests delivery stop and returns immediately,
// discarding messages queued behind the callback already executing, while Closed
// stays open until that callback returned.
func (consume *stubConsumeContext) Stop() {
	consume.ensureDispatching()
	if !consume.requestMode(modeStop) {
		return
	}
	close(consume.stopRequested)
}

// Drain mirrors nats.go: the call requests a drain and returns immediately,
// delivering messages queued behind the callback already executing before
// Closed closes.
func (consume *stubConsumeContext) Drain() {
	consume.ensureDispatching()
	if !consume.requestMode(modeDrain) {
		return
	}
	close(consume.drainRequested)
}

func (consume *stubConsumeContext) Closed() <-chan struct{} { return consume.closed }

// terminate simulates broker-side termination with neither Stop nor Drain.
func (consume *stubConsumeContext) terminate() {
	consume.ensureDispatching()
	consume.requestMode(modeTerminated)
}

// requestMode performs the shared first-call-wins transition and wakes the
// dispatcher, reporting whether this call was the first one.
func (consume *stubConsumeContext) requestMode(mode consumeMode) bool {
	consume.mu.Lock()
	defer consume.mu.Unlock()
	if consume.mode != modeRunning {
		return false
	}
	consume.mode = mode
	consume.cond.Broadcast()
	return true
}

// enqueue hands the dispatcher one message, exactly as nats.go queues a message
// the broker delivered to its asynchronous subscription.
func (consume *stubConsumeContext) enqueue(msg jetstream.Msg) {
	consume.ensureDispatching()
	consume.mu.Lock()
	consume.queued = append(consume.queued, msg)
	consume.cond.Broadcast()
	consume.mu.Unlock()
}

// ensureDispatching starts the single message dispatcher on first use, so a stub
// the primitive never subscribed or shut down leaves no goroutine behind.
func (consume *stubConsumeContext) ensureDispatching() {
	consume.startOnce.Do(func() { go consume.dispatch() })
}

// dispatch delivers queued messages one at a time, so a stop request is observed
// only once the callback already executing returned. It closes Closed when
// delivery truly ended.
func (consume *stubConsumeContext) dispatch() {
	defer close(consume.closed)
	for {
		consume.mu.Lock()
		for consume.mode == modeRunning && len(consume.queued) == 0 {
			consume.cond.Wait()
		}
		if consume.mode != modeRunning && consume.mode != modeDrain {
			// Stop and broker-side termination discard whatever the broker queued
			// behind the callback that was already executing.
			consume.queued = nil
			consume.mu.Unlock()
			return
		}
		if len(consume.queued) == 0 {
			// Drain: every queued message was delivered.
			consume.mu.Unlock()
			return
		}
		msg := consume.queued[0]
		consume.queued = consume.queued[1:]
		consume.mu.Unlock()
		consume.handler(msg)
	}
}

// awaitSignal waits for one lifecycle signal or fails the test after the bounded
// wait.
func awaitSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(testLifecycleTimeout):
		t.Fatal(message)
	}
}

// awaitClosed waits for a consumer to finish its lifecycle handling.
func awaitClosed(t *testing.T, consumer *platformnats.Consumer) {
	t.Helper()
	awaitSignal(t, consumer.Closed(), "the consumer never reported lifecycle completion")
}

// awaitDrainWait waits for a Drain call to return.
func awaitDrainWait(t *testing.T, drained <-chan error) error {
	t.Helper()
	select {
	case drainErr := <-drained:
		return drainErr
	case <-time.After(testLifecycleTimeout):
		t.Fatal("Drain never returned")
		return nil
	}
}

// assertStillOpen fails when a lifecycle signal that must not have happened yet
// already happened.
func assertStillOpen(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal(message)
	default:
	}
}

// assertClosedNow fails when a lifecycle signal that must already have happened
// has not.
func assertClosedNow(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	default:
		t.Fatal(message)
	}
}

// signalClosed reports whether a lifecycle signal already happened.
func signalClosed(signal <-chan struct{}) bool {
	select {
	case <-signal:
		return true
	default:
		return false
	}
}

// This test protects the primitive's own admission of its inputs and fails if a
// missing consumer or handler panics or reaches Consume instead of failing
// before any subscription.
func TestStartConsumerRejectsMissingDependencies(t *testing.T) {
	t.Parallel()
	stub := newStubConsumer()
	handler := func(jetstream.Msg) {}
	startAttempts := []struct {
		name     string
		consumer jetstream.Consumer
		handler  func(jetstream.Msg)
	}{
		{name: "missing consumer", consumer: nil, handler: handler},
		{name: "missing handler", consumer: stub, handler: nil},
	}
	for _, attempt := range startAttempts {
		managed, err := platformnats.StartConsumer(attempt.consumer, attempt.handler, platformnats.ConsumerOptions{})
		if err == nil {
			t.Fatalf("StartConsumer accepted a %s", attempt.name)
		}
		if managed != nil {
			t.Fatalf("StartConsumer returned %#v for a %s", managed, attempt.name)
		}
	}
	if stub.consumeCalls != 0 {
		t.Fatalf("failed starts called Consume %d times, want 0", stub.consumeCalls)
	}
}

// This test protects start-failure behavior and fails if a failed subscription
// returns a consumer or loses the upstream cause.
func TestStartConsumerFailedSubscriptionReturnsNoLifecycle(t *testing.T) {
	t.Parallel()
	startErr := errors.New("broker rejected the subscription")
	stub := newStubConsumer()
	stub.startErr = startErr
	managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {}, platformnats.ConsumerOptions{})
	if managed != nil {
		t.Fatalf("a failed subscription returned %#v, want no consumer", managed)
	}
	if !errors.Is(err, startErr) {
		t.Fatalf("start error = %v, want it to wrap %v", err, startErr)
	}
	if stub.consumeCalls != 1 {
		t.Fatalf("Consume called %d times, want 1", stub.consumeCalls)
	}
}

// This test protects the started-lifecycle invariant and fails if a consumer
// that reports success without a delivery context starts a watcher that can only
// panic.
func TestStartConsumerRejectsASuccessfulSubscriptionWithoutContext(t *testing.T) {
	t.Parallel()
	stub := newStubConsumer()
	stub.consumeContext = nil
	managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {}, platformnats.ConsumerOptions{})
	if managed != nil {
		t.Fatalf("StartConsumer returned %#v without a consume context", managed)
	}
	if err == nil {
		t.Fatal("StartConsumer reported success without a consume context")
	}
}

// This test protects activity reporting and one-shot unexpected termination, and
// fails if a started consumer reports inactive, never closes, reports the fault
// more than once, or resubscribes.
func TestConsumerReportsUnexpectedTerminationExactlyOnce(t *testing.T) {
	t.Parallel()
	var terminations atomic.Int32
	stub := newStubConsumer()
	managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {}, platformnats.ConsumerOptions{
		OnUnexpectedTermination: func() { terminations.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !managed.Active() {
		t.Fatal("a started consumer reports itself inactive")
	}
	assertStillOpen(t, managed.Closed(), "a running consumer reported lifecycle completion")

	stub.consumeContext.terminate()
	awaitClosed(t, managed)
	if managed.Active() {
		t.Fatal("a terminated consumer still reports itself active")
	}
	if calls := terminations.Load(); calls != 1 {
		t.Fatalf("unexpected-termination hook ran %d times, want exactly 1", calls)
	}
	// Shutdown after an unexpected termination is inert: it neither reports a
	// second fault nor resubscribes, and the subscription the primitive no longer
	// owns records no request at all.
	managed.Stop()
	if drainErr := managed.Drain(context.Background()); drainErr != nil {
		t.Fatalf("draining a terminated consumer: %v", drainErr)
	}
	assertStillOpen(t, stub.consumeContext.stopRequested, "shutdown after termination requested a stop")
	assertStillOpen(t, stub.consumeContext.drainRequested, "shutdown after termination requested a drain")
	if calls := terminations.Load(); calls != 1 {
		t.Fatalf("unexpected-termination hook ran %d times after shutdown, want 1", calls)
	}
	if stub.consumeCalls != 1 {
		t.Fatalf("consumer subscribed %d times, want no restart", stub.consumeCalls)
	}
}

// This test protects the close ordering contract and fails if Closed reports
// completion before the unexpected-termination hook returned, because admission
// decisions must be final when a joiner observes closure.
func TestConsumerClosesOnlyAfterUnexpectedTerminationHookReturns(t *testing.T) {
	t.Parallel()
	hookEntered := make(chan struct{})
	hookRelease := make(chan struct{})
	stub := newStubConsumer()
	managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {}, platformnats.ConsumerOptions{
		OnUnexpectedTermination: func() {
			close(hookEntered)
			<-hookRelease
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stub.consumeContext.terminate()
	awaitSignal(t, hookEntered, "the unexpected-termination hook never ran")
	assertStillOpen(t, managed.Closed(), "Closed reported completion before the termination hook returned")
	close(hookRelease)
	awaitClosed(t, managed)
}

// This test protects the intentional-shutdown contract and fails if Stop or
// Drain is classified as an unexpected termination, which would close a
// module's admission gate during a planned shutdown.
func TestConsumerIntentionalShutdownSuppressesUnexpectedTermination(t *testing.T) {
	t.Parallel()
	shutdowns := map[string]func(consumer *platformnats.Consumer) error{
		"stop": func(consumer *platformnats.Consumer) error {
			consumer.Stop()
			return nil
		},
		"drain": func(consumer *platformnats.Consumer) error {
			return consumer.Drain(context.Background())
		},
	}
	// Stop clears activity before teardown begins, so readiness fails as soon as
	// shutdown starts.
	for name, shutdown := range shutdowns {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var terminations atomic.Int32
			stub := newStubConsumer()
			managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {}, platformnats.ConsumerOptions{
				OnUnexpectedTermination: func() { terminations.Add(1) },
			})
			if err != nil {
				t.Fatal(err)
			}
			if shutdownErr := shutdown(managed); shutdownErr != nil {
				t.Errorf("intentional shutdown failed: %v", shutdownErr)
			}
			awaitClosed(t, managed)
			if managed.Active() {
				t.Fatal("a shut-down consumer still reports itself active")
			}
			assertClosedNow(t, managed.Closed(), "an intentional shutdown never reported completion")
			if calls := terminations.Load(); calls != 0 {
				t.Fatalf("intentional shutdown reported %d unexpected terminations, want 0", calls)
			}
		})
	}
}

// blockedConsumer is a started managed consumer whose message handler blocks on
// its first message. A test controls when that executing callback returns, so it
// can observe whether shutdown waits for it and whether a later shutdown call
// delivers or discards the messages queued behind it.
type blockedConsumer struct {
	stub            *stubConsumer
	consume         *stubConsumeContext
	managed         *platformnats.Consumer
	callbackRelease chan struct{}
	delivered       atomic.Int32
	terminations    atomic.Int32
}

func startBlockedConsumer(t *testing.T) *blockedConsumer {
	t.Helper()
	stub := newStubConsumer()
	callbackEntered := make(chan struct{})
	blocked := &blockedConsumer{
		stub:            stub,
		consume:         stub.consumeContext,
		callbackRelease: make(chan struct{}),
	}
	managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {
		if blocked.delivered.Add(1) == 1 {
			close(callbackEntered)
			<-blocked.callbackRelease
		}
	}, platformnats.ConsumerOptions{
		OnUnexpectedTermination: func() { blocked.terminations.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	blocked.managed = managed
	blocked.stub.deliverMessage()
	awaitSignal(t, callbackEntered, "the message handler never ran")
	return blocked
}

// This test protects the difference between a Stop call returning and a
// lifecycle finishing, and fails if Stop joins the in-flight callback - one
// stuck message would hold Core shutdown open - or if Closed closes before that
// callback returned, which would let a caller tear down dependencies the
// callback still uses.
func TestConsumerStopReturnsWithoutClosingWhileACallbackIsInFlight(t *testing.T) {
	t.Parallel()
	blocked := startBlockedConsumer(t)

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		blocked.managed.Stop()
	}()
	awaitSignal(t, stopped, "Stop did not return while a callback was in flight")
	awaitSignal(t, blocked.consume.stopRequested, "Stop never requested delivery stop")
	assertStillOpen(
		t,
		blocked.managed.Closed(),
		"Stop reported lifecycle completion before the in-flight callback returned",
	)
	if blocked.managed.Active() {
		t.Fatal("a stopped consumer still reports itself active")
	}

	close(blocked.callbackRelease)
	awaitClosed(t, blocked.managed)
}

// This test protects the drain join and fails if Drain returns before an
// in-flight callback finished or before the watcher completed, which would let
// shutdown close dependencies under a callback that still commits.
func TestConsumerDrainJoinsInFlightCallbacksAndWatcher(t *testing.T) {
	t.Parallel()
	var terminations atomic.Int32
	stub := newStubConsumer()
	callbackEntered := make(chan struct{})
	callbackRelease := make(chan struct{})
	managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {
		close(callbackEntered)
		<-callbackRelease
	}, platformnats.ConsumerOptions{
		OnUnexpectedTermination: func() { terminations.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	stub.deliverMessage()
	awaitSignal(t, callbackEntered, "the message handler never ran")

	drained := make(chan error, 1)
	go func() { drained <- managed.Drain(context.Background()) }()
	awaitSignal(t, stub.consumeContext.drainRequested, "Drain never requested delivery stop")
	if managed.Active() {
		t.Fatal("a draining consumer still reports itself active")
	}
	assertStillOpen(t, managed.Closed(), "Drain completed while a callback was still in flight")
	select {
	case drainErr := <-drained:
		t.Fatalf("Drain returned %v before the in-flight callback finished", drainErr)
	default:
	}

	close(callbackRelease)
	if drainErr := awaitDrainWait(t, drained); drainErr != nil {
		t.Fatalf("drain managed durable consumer: %v", drainErr)
	}
	awaitClosed(t, managed)
	if calls := terminations.Load(); calls != 0 {
		t.Fatalf("intentional drain reported %d unexpected terminations, want 0", calls)
	}
}

// This test protects the bounded-drain contract and fails if a canceled context
// aborts delivery or claims completion: cancellation must stop only the wait,
// and the watcher must still finish once the callback returns.
func TestConsumerDrainContextStopsWaitingOnly(t *testing.T) {
	t.Parallel()
	var terminations atomic.Int32
	stub := newStubConsumer()
	callbackEntered := make(chan struct{})
	callbackRelease := make(chan struct{})
	managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {
		close(callbackEntered)
		<-callbackRelease
	}, platformnats.ConsumerOptions{
		OnUnexpectedTermination: func() { terminations.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	stub.deliverMessage()
	awaitSignal(t, callbackEntered, "the message handler never ran")

	drainContext, cancelDrain := context.WithCancel(context.Background())
	drained := make(chan error, 1)
	go func() { drained <- managed.Drain(drainContext) }()
	awaitSignal(t, stub.consumeContext.drainRequested, "Drain never requested delivery stop")
	cancelDrain()
	drainErr := awaitDrainWait(t, drained)
	if !errors.Is(drainErr, context.Canceled) {
		t.Fatalf("Drain after context cancellation = %v, want context.Canceled", drainErr)
	}
	// Cancellation stopped only the wait, so delivery is still draining and its
	// lifecycle handling still completes.
	assertStillOpen(t, managed.Closed(), "a canceled drain reported lifecycle completion")
	close(callbackRelease)
	awaitClosed(t, managed)
	if calls := terminations.Load(); calls != 0 {
		t.Fatalf("canceled drain reported %d unexpected terminations, want 0", calls)
	}
}

// This test protects the shared first-call-wins state nats.go linearizes Stop
// and Drain on and fails if the later call overrides the earlier one: a Drain
// after a Stop must not deliver the messages the Stop discarded, and a Stop
// after a Drain must neither discard the messages the drain owes nor record a
// stop request of its own.
func TestConsumerFirstShutdownCallDecides(t *testing.T) {
	t.Parallel()

	t.Run("stop then drain discards queued messages", func(t *testing.T) {
		t.Parallel()
		blocked := startBlockedConsumer(t)
		blocked.stub.deliverMessage()
		blocked.managed.Stop()
		awaitSignal(t, blocked.consume.stopRequested, "Stop never requested delivery stop")
		assertStillOpen(t, blocked.managed.Closed(), "Stop completed the lifecycle before the callback returned")

		drained := make(chan error, 1)
		go func() { drained <- blocked.managed.Drain(context.Background()) }()
		close(blocked.callbackRelease)
		if drainErr := awaitDrainWait(t, drained); drainErr != nil {
			t.Fatalf("draining after a stop: %v", drainErr)
		}
		awaitClosed(t, blocked.managed)
		assertStillOpen(t, blocked.consume.drainRequested, "a Drain after a Stop recorded a drain")
		if delivered := blocked.delivered.Load(); delivered != 1 {
			t.Fatalf("stop then drain delivered %d messages, want the queued message discarded", delivered)
		}
		if calls := blocked.terminations.Load(); calls != 0 {
			t.Fatalf("stop then drain reported %d unexpected terminations, want 0", calls)
		}
	})

	t.Run("drain then stop still delivers queued messages", func(t *testing.T) {
		t.Parallel()
		blocked := startBlockedConsumer(t)
		blocked.stub.deliverMessage()
		drained := make(chan error, 1)
		go func() { drained <- blocked.managed.Drain(context.Background()) }()
		awaitSignal(t, blocked.consume.drainRequested, "Drain never requested delivery stop")

		blocked.managed.Stop()
		assertStillOpen(t, blocked.managed.Closed(), "a Stop after a Drain completed the lifecycle early")
		close(blocked.callbackRelease)
		if drainErr := awaitDrainWait(t, drained); drainErr != nil {
			t.Fatalf("draining before a stop: %v", drainErr)
		}
		awaitClosed(t, blocked.managed)
		assertStillOpen(t, blocked.consume.stopRequested, "a Stop after a Drain recorded a stop")
		if delivered := blocked.delivered.Load(); delivered != 2 {
			t.Fatalf("drain then stop delivered %d messages, want both", delivered)
		}
		if calls := blocked.terminations.Load(); calls != 0 {
			t.Fatalf("drain then stop reported %d unexpected terminations, want 0", calls)
		}
	})
}

// This test protects shutdown escalation after a timed-out Drain and fails if
// the later Stop deadlocks, refaults, resubscribes, or discards work the drain
// still owes. nats.go linearizes Stop and Drain on one state, so the escalation
// must be a safe no-op rather than a second stop.
func TestConsumerStopAfterDrainIsSafeAndSuppressesFaults(t *testing.T) {
	t.Parallel()
	blocked := startBlockedConsumer(t)
	blocked.stub.deliverMessage()

	drainContext, cancelDrain := context.WithCancel(context.Background())
	drained := make(chan error, 1)
	go func() { drained <- blocked.managed.Drain(drainContext) }()
	awaitSignal(t, blocked.consume.drainRequested, "Drain never requested delivery stop")
	cancelDrain()
	if drainErr := awaitDrainWait(t, drained); !errors.Is(drainErr, context.Canceled) {
		t.Fatalf("Drain after context cancellation = %v, want context.Canceled", drainErr)
	}

	blocked.managed.Stop()
	assertStillOpen(t, blocked.managed.Closed(), "a Stop after a Drain completed the lifecycle early")
	close(blocked.callbackRelease)
	awaitClosed(t, blocked.managed)
	// The Drain already decided the shutdown, so the Stop recorded no stop and
	// the drain still delivered the message queued behind the callback.
	assertStillOpen(t, blocked.consume.stopRequested, "a Stop after a Drain recorded a stop")
	if delivered := blocked.delivered.Load(); delivered != 2 {
		t.Fatalf("stop after drain delivered %d messages, want the drain to finish both", delivered)
	}
	if calls := blocked.terminations.Load(); calls != 0 {
		t.Fatalf("stop after drain reported %d unexpected terminations, want 0", calls)
	}
	if blocked.stub.consumeCalls != 1 {
		t.Fatalf("consumer subscribed %d times, want no restart", blocked.stub.consumeCalls)
	}
}

// This test protects concurrent shutdown safety and fails if repeated or
// simultaneous Stop and Drain calls double-close, deadlock, report a fault, or
// leave the consumer reported active.
func TestConsumerRepeatedAndConcurrentShutdownIsSafe(t *testing.T) {
	t.Parallel()
	var terminations atomic.Int32
	stub := newStubConsumer()
	managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {}, platformnats.ConsumerOptions{
		OnUnexpectedTermination: func() { terminations.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}

	shutdownCalls := 8
	var waitGroup sync.WaitGroup
	for index := range shutdownCalls {
		waitGroup.Go(func() {
			if index%2 == 0 {
				managed.Stop()
				return
			}
			if drainErr := managed.Drain(context.Background()); drainErr != nil {
				t.Errorf("drain managed durable consumer: %v", drainErr)
			}
		})
	}
	waitGroup.Wait()
	awaitClosed(t, managed)
	// Stop and Drain linearize on one shared state, so exactly one of the
	// interleaved calls decided the shutdown and the others requested nothing.
	stopped := signalClosed(stub.consumeContext.stopRequested)
	drained := signalClosed(stub.consumeContext.drainRequested)
	if stopped == drained {
		t.Fatalf("concurrent shutdown recorded stop=%t and drain=%t, want exactly one", stopped, drained)
	}
	if managed.Active() {
		t.Fatal("a concurrently shut-down consumer still reports itself active")
	}
	if calls := terminations.Load(); calls != 0 {
		t.Fatalf("intentional shutdown reported %d unexpected terminations, want 0", calls)
	}
	if stub.consumeCalls != 1 {
		t.Fatalf("consumer subscribed %d times, want no restart", stub.consumeCalls)
	}
}

// This test protects the module diagnostics seam and fails if the primitive
// installs no error handler, drops an error, or lets a consume error terminate
// the consumer or report a fault.
func TestConsumerForwardsConsumeErrorsToOptions(t *testing.T) {
	t.Parallel()
	consumeErr := errors.New("consume failed")
	stub := newStubConsumer()
	var received []error
	var terminations atomic.Int32
	managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {}, platformnats.ConsumerOptions{
		OnConsumeError:          func(err error) { received = append(received, err) },
		OnUnexpectedTermination: func() { terminations.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if stub.errHandler == nil {
		t.Fatal("StartConsumer installed no consume-error handler")
	}
	stub.errHandler(stub.consumeContext, consumeErr)
	if len(received) != 1 || !errors.Is(received[0], consumeErr) {
		t.Fatalf("consume-error hook received %v, want [%v]", received, consumeErr)
	}
	if !managed.Active() {
		t.Fatal("a consume error stopped the consumer")
	}
	assertStillOpen(t, managed.Closed(), "a consume error completed the consumer lifecycle")
	if calls := terminations.Load(); calls != 0 {
		t.Fatalf("a consume error reported %d unexpected terminations, want 0", calls)
	}
}

// This test protects readiness and shutdown when Core never established the
// subscription and fails if an inert consumer reports active, panics on
// shutdown, or never reports closure.
func TestConsumerWithoutSubscriptionIsInert(t *testing.T) {
	t.Parallel()
	consumers := map[string]*platformnats.Consumer{
		"nil consumer":  nil,
		"zero consumer": {},
	}
	for name, consumer := range consumers {
		if consumer.Active() {
			t.Fatalf("%s without a subscription reports active", name)
		}
		consumer.Stop()
		if drainErr := consumer.Drain(context.Background()); drainErr != nil {
			t.Fatalf("draining %s without a subscription: %v", name, drainErr)
		}
		if consumer.Active() {
			t.Fatalf("stopped %s without a subscription reports active", name)
		}
		assertClosedNow(t, consumer.Closed(), name+" without a subscription never reports closure")
	}
}

// This test protects the no-options path and fails if a consumer without module
// hooks panics or blocks when it terminates unexpectedly.
func TestConsumerWithoutOptionsToleratesUnexpectedTermination(t *testing.T) {
	t.Parallel()
	stub := newStubConsumer()
	managed, err := platformnats.StartConsumer(stub, func(jetstream.Msg) {}, platformnats.ConsumerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	stub.consumeContext.terminate()
	awaitClosed(t, managed)
	if managed.Active() {
		t.Fatal("a terminated consumer still reports itself active")
	}
}
