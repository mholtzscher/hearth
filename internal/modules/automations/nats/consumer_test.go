package nats //nolint:testpackage // Tests exercise package-private consumer lifecycle.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// observationWire builds one canonical Observation fact fixture with the
// supplied fact identity, so a test can publish distinct facts in one stream.
func observationWire(
	t *testing.T,
	validator *contractsv1.Validator,
	factID string,
) testDeviceFactMessage {
	t.Helper()
	input := defaultObservationFactInput()
	input.factID = factID
	input.messageID = factID
	return observationFactMessage(t, validator, input)
}

// TestDeviceFactConsumerAdmitsBothFactFamilies protects the whole accepted-fact
// path and fails if either family is dropped, mis-decoded, or acknowledged
// before admission.
func TestDeviceFactConsumerAdmitsBothFactFamilies(t *testing.T) {
	t.Parallel()
	js := startDeviceFactServer(t)
	validator := testValidator(t)
	receiver := newFakeDeviceFactReceiver()
	_, consumer := startDeviceFactConsumer(t, js, receiver)

	publishDeviceFact(t, js, observationWire(t, validator, testFactOneID))
	publishDeviceFact(t, js, entityEventFactMessage(t, validator, defaultEntityEventFactInput()))

	facts := receiver.waitForCall(t, 2)
	assertObservationFact(t, facts[0], testFactOneID, testObservationID, testEntityAID)
	assertEntityEventFact(t, facts[1], testFactTwoID, testEntityEventID, testEntityAID)
	waitForConsumerInfo(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Stream == 2 && info.NumAckPending == 0
	})
}

// TestDeviceFactConsumerStartsAtStreamTailOnFirstProvision protects the
// DeliverNew first-creation policy and fails if a new process replays retained
// facts published before it started.
func TestDeviceFactConsumerStartsAtStreamTailOnFirstProvision(t *testing.T) {
	t.Parallel()
	js := startDeviceFactServer(t)
	validator := testValidator(t)
	publishDeviceFact(t, js, observationWire(t, validator, testFactOneID))

	receiver := newFakeDeviceFactReceiver()
	_, consumer := startDeviceFactConsumer(t, js, receiver)
	waitForConsumerInfo(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.Config.DeliverPolicy == jetstream.DeliverNewPolicy && info.NumPending == 0
	})

	publishDeviceFact(t, js, observationWire(t, validator, testFactTwoID))
	facts := receiver.waitForCall(t, 1)
	if facts[0].Observation.FactID != devices.DeviceFactID(testFactTwoID) {
		t.Fatalf("delivered fact %q, want only the fact published after the tail",
			facts[0].Observation.FactID)
	}
	if count := receiver.callCount(); count != 1 {
		t.Fatalf("admitted %d facts, want only the post-tail fact", count)
	}
}

// TestDeviceFactConsumerResumesAcknowledgeFloorAfterRestart protects durable
// recovery and fails if a restarted consumer replays acknowledged facts or
// jumps past unacknowledged ones.
func TestDeviceFactConsumerResumesAcknowledgeFloorAfterRestart(t *testing.T) {
	t.Parallel()
	js := startDeviceFactServer(t)
	validator := testValidator(t)
	receiver := newFakeDeviceFactReceiver()

	first, firstBroker := startDeviceFactConsumer(t, js, receiver)
	publishDeviceFact(t, js, observationWire(t, validator, testFactOneID))
	receiver.waitForCall(t, 1)
	waitForConsumerInfo(t, firstBroker, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Stream == 1 && info.NumAckPending == 0
	})
	if err := first.Drain(); err != nil {
		t.Fatalf("drain first consumer: %v", err)
	}

	publishDeviceFact(t, js, observationWire(t, validator, testFactTwoID))
	_, secondBroker := startDeviceFactConsumer(t, js, receiver)
	facts := receiver.waitForCall(t, 2)
	if facts[1].Observation.FactID != devices.DeviceFactID(testFactTwoID) {
		t.Fatalf("resumed fact %q, want only the unacknowledged fact",
			facts[1].Observation.FactID)
	}
	waitForConsumerInfo(t, secondBroker, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Stream == 2 && info.NumAckPending == 0
	})
}

// TestDeviceFactConsumerRedeliversTransientAdmissionFailure protects the
// NakWithDelay retry path and fails if a transient storage failure is positively
// acknowledged or retried without a bounded delay.
func TestDeviceFactConsumerRedeliversTransientAdmissionFailure(t *testing.T) {
	t.Parallel()
	js := startDeviceFactServer(t)
	validator := testValidator(t)
	receiver := newFakeDeviceFactReceiver()
	receiver.failNext(1, errors.New("sqlite unavailable"))
	_, consumer := startDeviceFactConsumer(t, js, receiver)

	publishDeviceFact(t, js, observationWire(t, validator, testFactOneID))
	facts := receiver.waitForCall(t, 2)
	for index, fact := range facts {
		if fact.Observation.FactID != devices.DeviceFactID(testFactOneID) {
			t.Fatalf("delivery %d fact id = %q, want %q", index, fact.Observation.FactID, testFactOneID)
		}
	}
	waitForConsumerInfo(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Stream == 1 && info.NumAckPending == 0
	})
}

// TestDeviceFactConsumerSuppressesBrokerDuplicate protects at-least-once
// insurance and fails if a republished row inside the duplicate window creates a
// second admission.
func TestDeviceFactConsumerSuppressesBrokerDuplicate(t *testing.T) {
	t.Parallel()
	js := startDeviceFactServer(t)
	validator := testValidator(t)
	receiver := newFakeDeviceFactReceiver()
	_, consumer := startDeviceFactConsumer(t, js, receiver)

	message := observationWire(t, validator, testFactOneID)
	if ack := publishDeviceFact(t, js, message); ack.Duplicate {
		t.Fatal("the first publication was reported as a duplicate")
	}
	if ack := publishDeviceFact(t, js, message); !ack.Duplicate {
		t.Fatal("the republished fact was stored again inside the duplicate window")
	}
	receiver.waitForCall(t, 1)
	waitForConsumerInfo(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Stream == 1 && info.NumAckPending == 0
	})
	if count := receiver.callCount(); count != 1 {
		t.Fatalf("admitted %d facts, want the duplicate suppressed", count)
	}
}

// TestDeviceFactConsumerTerminatesMalformedFactThroughBroker protects the trust
// boundary end to end and fails if a deterministic malformed message blocks the
// one-at-a-time consumer instead of being terminated.
func TestDeviceFactConsumerTerminatesMalformedFactThroughBroker(t *testing.T) {
	t.Parallel()
	js := startDeviceFactServer(t)
	validator := testValidator(t)
	receiver := newFakeDeviceFactReceiver()
	_, consumer := startDeviceFactConsumer(t, js, receiver)

	publishDeviceFact(t, js, testDeviceFactMessage{
		subject:   rawDeviceFactSubject(testEntityAID, "observation", "applied"),
		messageID: testFactOneID,
		payload:   []byte(`{"id":`),
	})
	publishDeviceFact(t, js, observationWire(t, validator, testFactTwoID))
	facts := receiver.waitForCall(t, 1)
	if facts[0].Observation.FactID != devices.DeviceFactID(testFactTwoID) {
		t.Fatalf("admitted fact %q, want only the valid fact", facts[0].Observation.FactID)
	}
	waitForConsumerInfo(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.NumAckPending == 0 && info.NumPending == 0 && receiver.callCount() == 1
	})
}

// TestDeviceFactConsumerDrainStopsDelivery protects shutdown ordering and fails
// if a drained consumer keeps admitting facts or reports itself active.
func TestDeviceFactConsumerDrainStopsDelivery(t *testing.T) {
	t.Parallel()
	js := startDeviceFactServer(t)
	validator := testValidator(t)
	receiver := newFakeDeviceFactReceiver()
	running, consumer := startDeviceFactConsumer(t, js, receiver)

	publishDeviceFact(t, js, observationWire(t, validator, testFactOneID))
	receiver.waitForCall(t, 1)
	waitForConsumerInfo(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.AckFloor.Stream == 1 && info.NumAckPending == 0
	})
	if !running.Active() {
		t.Fatal("a live consumer reports itself inactive")
	}
	if err := running.Drain(); err != nil {
		t.Fatalf("drain device fact consumer: %v", err)
	}
	if running.Active() {
		t.Fatal("a drained consumer still reports itself active")
	}
	select {
	case <-running.Closed():
	default:
		t.Fatal("a drained consumer did not report itself closed")
	}

	publishDeviceFact(t, js, observationWire(t, validator, testFactTwoID))
	waitForConsumerInfo(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.NumPending == 1 && info.NumAckPending == 0
	})
	if count := receiver.callCount(); count != 1 {
		t.Fatalf("a drained consumer admitted %d facts, want only the pre-drain fact", count)
	}
}

// TestStartDeviceFactConsumerRequiresDependencies protects application assembly
// and fails if a missing receiver or validator silently starts a subscription.
func TestStartDeviceFactConsumerRequiresDependencies(t *testing.T) {
	t.Parallel()
	js := startDeviceFactServer(t)
	consumer, err := ProvisionDeviceFactConsumer(context.Background(), js, testDeviceFactStreamName)
	if err != nil {
		t.Fatal(err)
	}
	validator := testValidator(t)
	receiver := newFakeDeviceFactReceiver()
	tests := []struct {
		name      string
		receiver  DeviceFactReceiver
		validator *contractsv1.Validator
	}{
		{"missing receiver", nil, validator},
		{"missing validator", receiver, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, startErr := StartDeviceFactConsumer(
				context.Background(), consumer, test.receiver, test.validator, discardLogger(),
			); startErr == nil {
				t.Fatalf("consumer started without %s", test.name)
			}
		})
	}
}

// TestDeviceFactConsumerAdmitsSynchronously protects the synchronous admission
// contract and fails if the callback acknowledges or starts the next Fact before
// the current admission returns, which would allow overlapping household actions.
func TestDeviceFactConsumerAdmitsSynchronously(t *testing.T) {
	t.Parallel()
	js := startDeviceFactServer(t)
	validator := testValidator(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	entered := make(chan struct{}, 1)
	receiver := newFakeDeviceFactReceiver()
	receiver.onReceive = func(_ context.Context, fact automations.DeviceFact) {
		if fact.Observation == nil || fact.Observation.FactID != devices.DeviceFactID(testFactOneID) {
			return
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}
	_, consumer := startDeviceFactConsumer(t, js, receiver)

	publishDeviceFact(t, js, observationWire(t, validator, testFactOneID))
	select {
	case <-entered:
	case <-time.After(testLiveness):
		t.Fatal("the first fact never reached admission")
	}
	publishDeviceFact(t, js, observationWire(t, validator, testFactTwoID))
	waitForConsumerInfo(t, consumer, func(info *jetstream.ConsumerInfo) bool {
		return info.NumAckPending == 1 && info.NumPending == 1
	})
	if count := receiver.callCount(); count != 1 {
		t.Fatalf("admitted %d facts while the first was blocked, want 1", count)
	}

	unblock()
	facts := receiver.waitForCall(t, 2)
	if facts[1].Observation.FactID != devices.DeviceFactID(testFactTwoID) {
		t.Fatalf("second fact id = %q, want %q", facts[1].Observation.FactID, testFactTwoID)
	}
}

// assertDeviceFactReceiver is a compile-time check that the automations service
// satisfies the transport's admission seam, so hearthd can never wire a
// capability the consumer does not use.
func assertDeviceFactReceiver(DeviceFactReceiver) {}

// TestAutomationServiceSatisfiesDeviceFactReceiver protects the module boundary
// and fails to compile if the consumer's admission seam and the service drift.
func TestAutomationServiceSatisfiesDeviceFactReceiver(t *testing.T) {
	t.Parallel()
	assertDeviceFactReceiver((*automations.Service)(nil))
}

// assertObservationFact asserts one mapped Observation fact's identity so a
// defect that swaps identity, entity, or family is visible.
func assertObservationFact(
	t *testing.T,
	fact automations.DeviceFact,
	factID string,
	observationID string,
	entityID string,
) {
	t.Helper()
	if fact.Family != automations.DeviceFactObservation || fact.Observation == nil {
		t.Fatalf("mapped family = %#v, want an observation", fact)
	}
	switch {
	case fact.Observation.FactID != devices.DeviceFactID(factID):
		t.Fatalf("fact id = %q, want %q", fact.Observation.FactID, factID)
	case fact.Observation.ObservationID != devices.ObservationID(observationID):
		t.Fatalf("observation id = %q, want %q", fact.Observation.ObservationID, observationID)
	case fact.Observation.EntityID != devices.EntityID(entityID):
		t.Fatalf("entity id = %q, want %q", fact.Observation.EntityID, entityID)
	case fact.Observation.Disposition != devices.DispositionApplied:
		t.Fatalf("disposition = %q, want applied", fact.Observation.Disposition)
	}
}

// assertEntityEventFact asserts one mapped Entity Event fact's identity.
func assertEntityEventFact(
	t *testing.T,
	fact automations.DeviceFact,
	factID string,
	eventID string,
	entityID string,
) {
	t.Helper()
	if fact.Family != automations.DeviceFactEntityEvent || fact.EntityEvent == nil {
		t.Fatalf("mapped family = %#v, want an entity event", fact)
	}
	switch {
	case fact.EntityEvent.FactID != devices.DeviceFactID(factID):
		t.Fatalf("fact id = %q, want %q", fact.EntityEvent.FactID, factID)
	case fact.EntityEvent.EventID != devices.EntityEventID(eventID):
		t.Fatalf("event id = %q, want %q", fact.EntityEvent.EventID, eventID)
	case fact.EntityEvent.EntityID != devices.EntityID(entityID):
		t.Fatalf("entity id = %q, want %q", fact.EntityEvent.EntityID, entityID)
	case fact.EntityEvent.Name != devices.EntityEventName(testEventName):
		t.Fatalf("event name = %q, want %q", fact.EntityEvent.Name, testEventName)
	}
}
