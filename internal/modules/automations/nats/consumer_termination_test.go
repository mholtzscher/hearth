package nats //nolint:testpackage // Tests exercise package-private consumer termination.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// controllableConsumeContext is a ConsumeContext whose termination one test
// controls. stop closes it without a drain, exactly as a deleted consumer or a
// dropped subscription does, while the consumer's own Drain closes it through
// the same channel after marking the drain intentional.
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

// stubDeviceFactConsumerResource is a jetstream.Consumer whose only implemented
// method is Consume, which hands back the supplied ConsumeContext. Start never
// calls another Consumer method, so the embedded interface stays unused.
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

// gatedDeviceFactReceiver is a DeviceFactReceiver that also implements
// AdmissionGate, recording whether the transport closed admission. It admits no
// Fact, which the termination tests never publish.
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
// ConsumeContext and joins its termination watcher before the test ends.
func startStubbedDeviceFactConsumer(
	t *testing.T,
	receiver DeviceFactReceiver,
) (*DeviceFactConsumer, *controllableConsumeContext) {
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
	t.Cleanup(func() {
		if drainErr := running.Drain(); drainErr != nil {
			t.Errorf("drain device fact consumer: %v", drainErr)
		}
	})
	return running, consume
}

// TestDeviceFactConsumerLatchesAdmissionOnUnexpectedTermination protects the
// consumer-fault contract and fails if a consume loop that ends without an
// intentional Drain leaves automation admission open.
func TestDeviceFactConsumerLatchesAdmissionOnUnexpectedTermination(t *testing.T) {
	t.Parallel()
	receiver := &gatedDeviceFactReceiver{}
	running, consume := startStubbedDeviceFactConsumer(t, receiver)

	if !running.Active() {
		t.Fatal("a live consumer reports itself inactive")
	}
	consume.Stop()

	<-running.terminated
	if running.Active() {
		t.Fatal("a terminated consumer still reports itself active")
	}
	select {
	case <-running.Closed():
	default:
		t.Fatal("a terminated consumer did not report itself closed")
	}
	if calls := receiver.gateCalls.Load(); calls != 1 {
		t.Fatalf("closing automation admission %d times, want exactly 1", calls)
	}
	if admitted := receiver.admitted.Load(); admitted != 0 {
		t.Fatalf("admitted %d facts, want none", admitted)
	}
}

// TestDeviceFactConsumerDoesNotLatchAdmissionOnDrain protects the intentional
// shutdown path and fails if a requested drain is misread as a consumer fault
// and falsely closes automation admission.
func TestDeviceFactConsumerDoesNotLatchAdmissionOnDrain(t *testing.T) {
	t.Parallel()
	receiver := &gatedDeviceFactReceiver{}
	running, _ := startStubbedDeviceFactConsumer(t, receiver)

	if err := running.Drain(); err != nil {
		t.Fatalf("drain device fact consumer: %v", err)
	}
	// Drain joins the termination watcher, so its fault decision is already
	// final and no late latch can arrive after this point.
	if calls := receiver.gateCalls.Load(); calls != 0 {
		t.Fatalf("intentional drain closed automation admission %d times, want 0", calls)
	}
	if running.Active() {
		t.Fatal("a drained consumer still reports itself active")
	}
}

// TestDeviceFactConsumerWithoutGateToleratesUnexpectedTermination protects the
// optional gate capability and fails if unexpected termination panics or
// misbehaves for a receiver that does not implement AdmissionGate.
func TestDeviceFactConsumerWithoutGateToleratesUnexpectedTermination(t *testing.T) {
	t.Parallel()
	receiver := newFakeDeviceFactReceiver()
	running, consume := startStubbedDeviceFactConsumer(t, receiver)

	consume.Stop()
	<-running.terminated
	if running.Active() {
		t.Fatal("a terminated consumer still reports itself active")
	}
}

// TestDeviceFactConsumerLatchesAdmissionWhenConsumerIsDeleted proves the fault
// path against a real broker and the real automations service: deleting the
// durable consumer out from under the subscription closes automation admission
// through Service.StopAdmission, so the existing readiness check fails until
// restart. It never publishes a Fact, so the service performs no persistence.
func TestDeviceFactConsumerLatchesAdmissionWhenConsumerIsDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	js := startDeviceFactServer(t)
	resource, err := ProvisionDeviceFactConsumer(ctx, js, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	service := automations.NewService(nil, nil, automations.AutomationDependencies{})
	if !service.AdmissionOpen() {
		t.Fatal("a freshly assembled service reports admission closed")
	}
	running, err := StartDeviceFactConsumer(
		ctx, resource, service, testValidator(t), discardLogger(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if drainErr := running.Drain(); drainErr != nil {
			t.Errorf("drain device fact consumer: %v", drainErr)
		}
	})

	stream, err := js.Stream(ctx, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	if deleteErr := stream.DeleteConsumer(ctx, DeviceFactConsumerName); deleteErr != nil {
		t.Fatalf("delete device fact consumer: %v", deleteErr)
	}

	<-running.terminated
	if service.AdmissionOpen() {
		t.Fatal("automation admission stayed open after the consumer was deleted")
	}
	if running.Active() {
		t.Fatal("a consumer whose broker resource was deleted still reports itself active")
	}
}

// assertAdmissionGate is a compile-time check that the automations service keeps
// the narrow gate capability the transport latches on, so hearthd can never wire
// a receiver whose admission the consumer cannot close.
func assertAdmissionGate(AdmissionGate) {}

// TestAutomationServiceSatisfiesAdmissionGate protects the fault-latch boundary
// and fails to compile if the consumer's gate seam and the service drift.
func TestAutomationServiceSatisfiesAdmissionGate(t *testing.T) {
	t.Parallel()
	assertAdmissionGate((*automations.Service)(nil))
}
