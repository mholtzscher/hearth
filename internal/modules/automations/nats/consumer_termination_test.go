package nats //nolint:testpackage // Tests exercise package-private consumer termination.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

// controllableConsumeContext lets tests terminate consumption with or without Drain.
type controllableConsumeContext struct {
	closed chan struct{}
	once   sync.Once
}

func newControllableConsumeContext() *controllableConsumeContext {
	return &controllableConsumeContext{closed: make(chan struct{})}
}

func (consume *controllableConsumeContext) Stop()  { consume.terminate() }
func (consume *controllableConsumeContext) Drain() { consume.terminate() }

func (consume *controllableConsumeContext) Closed() <-chan struct{} { return consume.closed }

func (consume *controllableConsumeContext) terminate() {
	consume.once.Do(func() { close(consume.closed) })
}

// stubDeviceFactConsumerResource implements only Consume, returning a controlled context.
type stubDeviceFactConsumerResource struct {
	jetstream.Consumer

	consume *controllableConsumeContext
}

func (stub *stubDeviceFactConsumerResource) Consume(
	jetstream.MessageHandler,
	...jetstream.PullConsumeOpt,
) (jetstream.ConsumeContext, error) {
	return stub.consume, nil
}

// gatedDeviceFactReceiver records gate closure; these termination tests publish no Facts.
type gatedDeviceFactReceiver struct {
	gateCalls atomic.Int32
	admitted  atomic.Int32
}

func (receiver *gatedDeviceFactReceiver) ReceiveDeviceFact(
	context.Context,
	automations.DeviceFact,
) (automations.AdmissionOutcome, error) {
	receiver.admitted.Add(1)
	return automations.AdmissionOutcome{}, nil
}

func (receiver *gatedDeviceFactReceiver) StopAdmission() {
	receiver.gateCalls.Add(1)
}

// startStubbedDeviceFactConsumer starts one consumer over the controllable
// ConsumeContext and always drains it before the test ends.
func startStubbedDeviceFactConsumer(
	t *testing.T,
	receiver DeviceFactReceiver,
) (*platformnats.Consumer, *controllableConsumeContext) {
	t.Helper()
	consume := newControllableConsumeContext()
	running, err := StartDeviceFactConsumer(
		context.Background(),
		&stubDeviceFactConsumerResource{consume: consume},
		receiver,
		testValidator(t),
		discardLogger(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { drainDeviceFactConsumer(t, running) })
	return running, consume
}

// waitForConsumerClosed waits for the consumer's lifecycle handling, including
// its fault decision, to finish. The wait is bounded so a termination the
// broker never reports fails the test instead of hanging it.
func waitForConsumerClosed(t *testing.T, running *platformnats.Consumer) {
	t.Helper()
	select {
	case <-running.Closed():
	case <-time.After(testLiveness):
		t.Fatal("device fact consumer did not report closure")
	}
}

// Unexpected termination must close admission.
func TestDeviceFactConsumerLatchesAdmissionOnUnexpectedTermination(t *testing.T) {
	t.Parallel()
	receiver := &gatedDeviceFactReceiver{}
	running, consume := startStubbedDeviceFactConsumer(t, receiver)

	if !running.Active() {
		t.Fatal("a live consumer reports itself inactive")
	}
	consume.Stop()

	waitForConsumerClosed(t, running)
	if running.Active() {
		t.Fatal("a terminated consumer still reports itself active")
	}
	if calls := receiver.gateCalls.Load(); calls != 1 {
		t.Fatalf("closing automation admission %d times, want exactly 1", calls)
	}
	if admitted := receiver.admitted.Load(); admitted != 0 {
		t.Fatalf("admitted %d facts, want none", admitted)
	}
}

// Intentional Drain must not be classified as a consumer fault.
func TestDeviceFactConsumerDoesNotLatchAdmissionOnDrain(t *testing.T) {
	t.Parallel()
	receiver := &gatedDeviceFactReceiver{}
	running, _ := startStubbedDeviceFactConsumer(t, receiver)

	// Drain joins the lifecycle watcher, so its fault decision is already final
	// and no late latch can arrive after this point.
	drainDeviceFactConsumer(t, running)
	if calls := receiver.gateCalls.Load(); calls != 0 {
		t.Fatalf("intentional drain closed automation admission %d times, want 0", calls)
	}
	if running.Active() {
		t.Fatal("a drained consumer still reports itself active")
	}
}

// Unexpected termination must tolerate a receiver without the optional gate.
func TestDeviceFactConsumerWithoutGateToleratesUnexpectedTermination(t *testing.T) {
	t.Parallel()
	receiver := newFakeDeviceFactReceiver()
	running, consume := startStubbedDeviceFactConsumer(t, receiver)

	consume.Stop()
	waitForConsumerClosed(t, running)
	if running.Active() {
		t.Fatal("a terminated consumer still reports itself active")
	}
}

// Broker-reported termination must close the real service's admission gate.
// Wait for an outstanding pull so deletion reaches it as a terminal status;
// deletion between pulls may leave nats.go retrying without reporting closure.
func TestDeviceFactConsumerLatchesAdmissionWhenConsumerIsDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	js := startDeviceFactServer(t)
	validator := testValidator(t)
	resource, err := ProvisionDeviceFactConsumer(ctx, js, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	service := automations.NewService(nil, nil, automations.AutomationDependencies{})
	if !service.AdmissionOpen() {
		t.Fatal("a freshly assembled service reports admission closed")
	}
	running, err := StartDeviceFactConsumer(ctx, resource, service, validator, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { drainDeviceFactConsumer(t, running) })

	// A malformed Fact is terminated by the consumer and never reaches
	// admission, so the delivery cycle is observable without a repository.
	publishDeviceFact(t, js, testDeviceFactMessage{
		subject:   rawDeviceFactSubject(testEntityAID, "observation", "applied"),
		messageID: testFactOneID,
		payload:   []byte(`{"id":`),
	})
	waitForConsumerInfo(t, resource, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Stream == 1 && info.NumAckPending == 0 && info.NumWaiting > 0
	})

	stream, err := js.Stream(ctx, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	if deleteErr := stream.DeleteConsumer(ctx, DeviceFactConsumerName); deleteErr != nil {
		t.Fatalf("delete device fact consumer: %v", deleteErr)
	}

	waitForConsumerClosed(t, running)
	if service.AdmissionOpen() {
		t.Fatal("automation admission stayed open after the consumer was deleted")
	}
	if running.Active() {
		t.Fatal("a consumer whose broker resource was deleted still reports itself active")
	}
}

// assertAdmissionGate checks that the service implements the optional gate interface.
func assertAdmissionGate(AdmissionGate) {}

// The service must remain compatible with the consumer's fault-latch interface.
func TestAutomationServiceSatisfiesAdmissionGate(t *testing.T) {
	t.Parallel()
	assertAdmissionGate((*automations.Service)(nil))
}
