package nats //nolint:testpackage // Tests exercise package-private mapping, disposition, and lifecycle.

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
)

const (
	// testDeviceFactStreamName is the stream name devices owns; hearthd supplies
	// it to the automations transport, so tests must supply it too.
	testDeviceFactStreamName = "HEARTH_DEVICE_FACTS_V1"
	// testLiveness bounds broker waits.
	testLiveness = 5 * time.Second
	// testPollInterval sets polling cadence; the observed condition decides success.
	testPollInterval = 10 * time.Millisecond
	// testDuplicateWindow mirrors the stream's bounded broker duplicate window so
	// a republished fact collapses into one stored message.
	testDuplicateWindow = 2 * time.Hour
)

// Canonical UUIDv7 fixtures accepted by the strict schemas and device parsers.
const (
	testEntityAID       = "ent_01890f47-7a6b-7c4d-8e9f-0123456789a1"
	testEntityBID       = "ent_01890f47-7a6b-7c4d-8e9f-0123456789a2"
	testFactOneID       = "fct_01890f47-7a6b-7c4d-8e9f-0123456789b1"
	testFactTwoID       = "fct_01890f47-7a6b-7c4d-8e9f-0123456789b2"
	testObservationID   = "obs_01890f47-7a6b-7c4d-8e9f-0123456789c1"
	testOtherObsID      = "obs_01890f47-7a6b-7c4d-8e9f-0123456789c2"
	testEntityEventID   = "evt_01890f47-7a6b-7c4d-8e9f-0123456789d1"
	testOtherEventID    = "evt_01890f47-7a6b-7c4d-8e9f-0123456789d2"
	testCorrelationID   = "cor_01890f47-7a6b-7c4d-8e9f-0123456789e1"
	testEventName       = "single_press"
	testEmittedAtString = "2026-09-14T10:00:00Z"
)

// testDeviceFactMessage is one publishable Device Fact: the exact subject, the
// broker message identity header, and the exact payload bytes.
type testDeviceFactMessage struct {
	subject   string
	messageID string
	payload   []byte
}

// observationFactInput describes a wire fixture; empty overrides use canonical defaults.
type observationFactInput struct {
	factID          string
	observationID   string
	entityID        string
	payloadEntityID string
	disposition     string
	subjectVariant  string
	value           string
	previousValue   json.RawMessage
	emittedAt       time.Time
	correlationID   string
	causationID     *string
	messageID       string
	rawPayload      []byte
}

func defaultObservationFactInput() observationFactInput {
	return observationFactInput{
		factID:          testFactOneID,
		observationID:   testObservationID,
		entityID:        testEntityAID,
		payloadEntityID: testEntityAID,
		disposition:     string(devices.DispositionApplied),
		subjectVariant:  string(devices.DispositionApplied),
		value:           `{"on":true,"level":42}`,
		emittedAt:       mustTestTime(testEmittedAtString),
		correlationID:   testCorrelationID,
		messageID:       testFactOneID,
	}
}

// entityEventFactInput describes one Entity Event wire fixture with the same
// override convention.
type entityEventFactInput struct {
	factID          string
	eventID         string
	entityID        string
	payloadEntityID string
	name            string
	subjectVariant  string
	emittedAt       time.Time
	correlationID   string
	causationID     *string
	messageID       string
	rawPayload      []byte
}

func defaultEntityEventFactInput() entityEventFactInput {
	return entityEventFactInput{
		factID:          testFactTwoID,
		eventID:         testEntityEventID,
		entityID:        testEntityAID,
		payloadEntityID: testEntityAID,
		name:            testEventName,
		subjectVariant:  testEventName,
		emittedAt:       mustTestTime(testEmittedAtString),
		correlationID:   testCorrelationID,
		messageID:       testFactTwoID,
	}
}

func mustTestTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func testValidator(t *testing.T) *contractsv1.Validator {
	t.Helper()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

// observationFactMessage encodes one schema-valid Observation fact, or uses the
// supplied raw payload so a test can publish malformed bytes.
func observationFactMessage(
	t *testing.T,
	validator *contractsv1.Validator,
	input observationFactInput,
) testDeviceFactMessage {
	t.Helper()
	payload := input.rawPayload
	if payload == nil {
		causationID := input.causationID
		if causationID == nil {
			causationID = &input.observationID
		}
		emittedAt := input.emittedAt.UTC().Format(time.RFC3339Nano)
		envelope := natswire.Envelope[observationFactData]{
			ID:            input.factID,
			Schema:        contractsv1.ObservationFactSchemaID,
			EmittedAt:     emittedAt,
			CorrelationID: input.correlationID,
			CausationID:   causationID,
			Data: observationFactData{
				ObservationID:     input.observationID,
				EntityID:          input.payloadEntityID,
				Disposition:       input.disposition,
				Value:             json.RawMessage(input.value),
				PreviousValue:     input.previousValue,
				AdapterReceivedAt: emittedAt,
				ObservedAt:        emittedAt,
			},
		}
		encoded, err := natswire.Encode(validator, contractsv1.ObservationFactSchemaID, envelope)
		if err != nil {
			t.Fatalf("encode observation fact: %v", err)
		}
		payload = encoded
	}
	subject, err := natswire.ObservationFactSubject(input.entityID, input.subjectVariant)
	if err != nil {
		t.Fatalf("build observation fact subject: %v", err)
	}
	return testDeviceFactMessage{subject: subject, messageID: input.messageID, payload: payload}
}

// entityEventFactMessage encodes one schema-valid Entity Event fact, or uses the
// supplied raw payload so a test can publish malformed bytes.
func entityEventFactMessage(
	t *testing.T,
	validator *contractsv1.Validator,
	input entityEventFactInput,
) testDeviceFactMessage {
	t.Helper()
	payload := input.rawPayload
	if payload == nil {
		causationID := input.causationID
		if causationID == nil {
			causationID = &input.eventID
		}
		emittedAt := input.emittedAt.UTC().Format(time.RFC3339Nano)
		envelope := natswire.Envelope[entityEventFactData]{
			ID:            input.factID,
			Schema:        contractsv1.EntityEventFactSchemaID,
			EmittedAt:     emittedAt,
			CorrelationID: input.correlationID,
			CausationID:   causationID,
			Data: entityEventFactData{
				EventID:    input.eventID,
				EntityID:   input.payloadEntityID,
				Name:       input.name,
				ReportedAt: emittedAt,
				ReceivedAt: emittedAt,
				RecordedAt: emittedAt,
			},
		}
		encoded, err := natswire.Encode(validator, contractsv1.EntityEventFactSchemaID, envelope)
		if err != nil {
			t.Fatalf("encode entity event fact: %v", err)
		}
		payload = encoded
	}
	subject, err := natswire.EntityEventFactSubject(input.entityID, input.subjectVariant)
	if err != nil {
		t.Fatalf("build entity event fact subject: %v", err)
	}
	return testDeviceFactMessage{subject: subject, messageID: input.messageID, payload: payload}
}

// asWire converts one fixture into the package-private wire input the mapping
// consumes.
func (message testDeviceFactMessage) asWire() deviceFactWireMessage {
	return deviceFactWireMessage(message)
}

// fakeDeviceFactReceiver records Facts and deadlines and can fail calls to test redelivery.
type fakeDeviceFactReceiver struct {
	mutex       sync.Mutex
	facts       []automations.DeviceFact
	remains     []time.Duration
	failures    int
	failErr     error
	onReceive   func(ctx context.Context, fact automations.DeviceFact)
	transitions chan struct{}
}

func newFakeDeviceFactReceiver() *fakeDeviceFactReceiver {
	return &fakeDeviceFactReceiver{transitions: make(chan struct{}, 1)}
}

func (receiver *fakeDeviceFactReceiver) ReceiveDeviceFact(
	ctx context.Context,
	fact automations.DeviceFact,
) (automations.AdmissionOutcome, error) {
	receiver.mutex.Lock()
	receiver.facts = append(receiver.facts, fact)
	if deadline, ok := ctx.Deadline(); ok {
		receiver.remains = append(receiver.remains, time.Until(deadline))
	}
	receiver.mutex.Unlock()
	receiver.signal()
	if receiver.onReceive != nil {
		receiver.onReceive(ctx, fact)
	}
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	if receiver.failures > 0 {
		receiver.failures--
		return automations.AdmissionOutcome{}, receiver.failErr
	}
	return automations.AdmissionOutcome{}, nil
}

func (receiver *fakeDeviceFactReceiver) signal() {
	select {
	case receiver.transitions <- struct{}{}:
	default:
	}
}

func (receiver *fakeDeviceFactReceiver) waitForCall(t *testing.T, count int) []automations.DeviceFact {
	t.Helper()
	deadline := time.NewTimer(testLiveness)
	defer deadline.Stop()
	for {
		facts := receiver.recorded()
		if len(facts) >= count {
			return facts
		}
		select {
		case <-receiver.transitions:
		case <-deadline.C:
			t.Fatalf("receiver admitted %d facts, want %d", len(facts), count)
		}
	}
}

func (receiver *fakeDeviceFactReceiver) recorded() []automations.DeviceFact {
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	return append([]automations.DeviceFact(nil), receiver.facts...)
}

func (receiver *fakeDeviceFactReceiver) callCount() int {
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	return len(receiver.facts)
}

func (receiver *fakeDeviceFactReceiver) observedDeadlines() []time.Duration {
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	return append([]time.Duration(nil), receiver.remains...)
}

func (receiver *fakeDeviceFactReceiver) failNext(count int, err error) {
	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()
	receiver.failures = count
	receiver.failErr = err
}

// drainDeviceFactConsumer drains one consumer inside the test liveness budget,
// so a receiver that never returns fails the test instead of hanging cleanup.
func drainDeviceFactConsumer(t *testing.T, running *platformnats.Consumer) {
	t.Helper()
	drainContext, cancelDrain := context.WithTimeout(context.Background(), testLiveness)
	defer cancelDrain()
	if drainErr := running.Drain(drainContext); drainErr != nil {
		t.Errorf("drain device fact consumer: %v", drainErr)
	}
}

// discardLogger returns a logger that drops every record, for tests that observe
// broker state instead of diagnostics.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
