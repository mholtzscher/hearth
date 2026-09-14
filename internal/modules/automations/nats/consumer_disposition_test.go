package nats //nolint:testpackage // Tests exercise package-private message disposition.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// recordingDeviceFactMsg is a minimal jetstream.Msg that records exactly which
// disposition the handler chose, so a test can distinguish Ack, NakWithDelay,
// and Term without a broker.
type recordingDeviceFactMsg struct {
	subject     string
	header      natsgo.Header
	data        []byte
	metadata    *jetstream.MsgMetadata
	metadataErr error

	mutex     sync.Mutex
	acks      int
	terms     int
	nakDelays []time.Duration
}

func newRecordingDeviceFactMsg(message testDeviceFactMessage) *recordingDeviceFactMsg {
	header := make(natsgo.Header)
	header.Set(natsgo.MsgIdHdr, message.messageID)
	return &recordingDeviceFactMsg{
		subject: message.subject,
		header:  header,
		data:    message.payload,
		metadata: &jetstream.MsgMetadata{
			Sequence: jetstream.SequencePair{Stream: 1, Consumer: 1},
			Stream:   testDeviceFactStreamName,
			Consumer: DeviceFactConsumerName,
		},
	}
}

func (msg *recordingDeviceFactMsg) Metadata() (*jetstream.MsgMetadata, error) {
	if msg.metadataErr != nil {
		return nil, msg.metadataErr
	}
	return msg.metadata, nil
}

func (msg *recordingDeviceFactMsg) Data() []byte           { return msg.data }
func (msg *recordingDeviceFactMsg) Headers() natsgo.Header { return msg.header }
func (msg *recordingDeviceFactMsg) Subject() string        { return msg.subject }
func (msg *recordingDeviceFactMsg) Reply() string          { return "" }

func (msg *recordingDeviceFactMsg) Ack() error {
	msg.mutex.Lock()
	defer msg.mutex.Unlock()
	msg.acks++
	return nil
}

func (msg *recordingDeviceFactMsg) DoubleAck(context.Context) error { return msg.Ack() }

func (msg *recordingDeviceFactMsg) Nak() error { return msg.NakWithDelay(0) }

func (msg *recordingDeviceFactMsg) NakWithDelay(delay time.Duration) error {
	msg.mutex.Lock()
	defer msg.mutex.Unlock()
	msg.nakDelays = append(msg.nakDelays, delay)
	return nil
}

func (msg *recordingDeviceFactMsg) InProgress() error { return nil }

func (msg *recordingDeviceFactMsg) Term() error {
	msg.mutex.Lock()
	defer msg.mutex.Unlock()
	msg.terms++
	return nil
}

func (msg *recordingDeviceFactMsg) TermWithReason(string) error { return msg.Term() }

func (msg *recordingDeviceFactMsg) dispositions() (int, int, []time.Duration) {
	msg.mutex.Lock()
	defer msg.mutex.Unlock()
	return msg.acks, msg.terms, append([]time.Duration(nil), msg.nakDelays...)
}

// TestHandleDeviceFactMessageDisposition protects ack-after-admission, permanent
// termination of deterministic wire input, and delayed negative acknowledgement
// of transient admission failures. It fails if any malformed or retryable fact is
// positively acknowledged or executed.
func TestHandleDeviceFactMessageDisposition(t *testing.T) {
	t.Parallel()
	validator := testValidator(t)
	validObservation := observationFactMessage(t, validator, defaultObservationFactInput())
	validEvent := entityEventFactMessage(t, validator, defaultEntityEventFactInput())
	malformedPayload := testDeviceFactMessage{
		subject:   rawDeviceFactSubject(testEntityAID, "observation", "applied"),
		messageID: testFactOneID,
		payload:   []byte(`{"id":`),
	}
	messageIDMismatch := observationFactMessage(t, validator, mutation(func(input *observationFactInput) {
		input.messageID = testFactTwoID
	}))
	causationMismatch := observationFactMessage(t, validator, mutation(func(input *observationFactInput) {
		input.causationID = new(testOtherObsID)
	}))
	subjectMismatch := observationFactMessage(t, validator, mutation(func(input *observationFactInput) {
		input.payloadEntityID = testEntityBID
	}))
	unknownFamily := observationFactMessage(t, validator, defaultObservationFactInput())
	unknownFamily.subject = rawDeviceFactSubject(testEntityAID, "command", "applied")

	tests := []struct {
		name         string
		message      testDeviceFactMessage
		failNext     error
		wantAck      int
		wantTerm     int
		wantNakDelay time.Duration
		wantAdmitted int
	}{
		{
			name: "observation admitted", message: validObservation,
			wantAck: 1, wantAdmitted: 1,
		},
		{
			name: "entity event admitted", message: validEvent,
			wantAck: 1, wantAdmitted: 1,
		},
		{
			name: "malformed payload terminated", message: malformedPayload,
			wantTerm: 1,
		},
		{
			name: "message id mismatch terminated", message: messageIDMismatch,
			wantTerm: 1,
		},
		{
			name: "causation mismatch terminated", message: causationMismatch,
			wantTerm: 1,
		},
		{
			name: "subject mismatch terminated", message: subjectMismatch,
			wantTerm: 1,
		},
		{
			name: "unknown family terminated", message: unknownFamily,
			wantTerm: 1,
		},
		{
			name: "deterministic admission failure terminated", message: validObservation,
			failNext: automations.ErrInvalidDeviceFact, wantTerm: 1, wantAdmitted: 1,
		},
		{
			name: "transient storage failure redelivered", message: validObservation,
			failNext:     errors.New("sqlite unavailable"),
			wantNakDelay: DeviceFactConsumerNakDelay, wantAdmitted: 1,
		},
		{
			name: "closed admission redelivered", message: validObservation,
			failNext:     automations.ErrAdmissionUnavailable,
			wantNakDelay: DeviceFactConsumerNakDelay, wantAdmitted: 1,
		},
		{
			name: "admission deadline redelivered", message: validObservation,
			failNext:     context.DeadlineExceeded,
			wantNakDelay: DeviceFactConsumerNakDelay, wantAdmitted: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			msg := newRecordingDeviceFactMsg(test.message)
			receiver := newFakeDeviceFactReceiver()
			if test.failNext != nil {
				receiver.failNext(1, test.failNext)
			}
			handleDeviceFactMessage(context.Background(), msg, validator, receiver, discardLogger())
			acks, terms, nakDelays := msg.dispositions()
			if acks != test.wantAck || terms != test.wantTerm {
				t.Fatalf("ack=%d term=%d, want ack=%d term=%d", acks, terms, test.wantAck, test.wantTerm)
			}
			if test.wantNakDelay == 0 && len(nakDelays) != 0 {
				t.Fatalf("unexpected negative acknowledgements: %v", nakDelays)
			}
			if test.wantNakDelay != 0 &&
				(len(nakDelays) != 1 || nakDelays[0] != test.wantNakDelay) {
				t.Fatalf("negative acknowledgement delays = %v, want [%s]",
					nakDelays, test.wantNakDelay)
			}
			if receiver.callCount() != test.wantAdmitted {
				t.Fatalf("admitted %d facts, want %d", receiver.callCount(), test.wantAdmitted)
			}
		})
	}
}

// TestHandleDeviceFactMessageBoundsAdmission protects the two-second admission
// context and fails if a live callback could run for an unbounded time and let
// the broker create concurrent delivery.
func TestHandleDeviceFactMessageBoundsAdmission(t *testing.T) {
	t.Parallel()
	validator := testValidator(t)
	msg := newRecordingDeviceFactMsg(observationFactMessage(t, validator, defaultObservationFactInput()))
	receiver := newFakeDeviceFactReceiver()
	handleDeviceFactMessage(context.Background(), msg, validator, receiver, discardLogger())

	deadlines := receiver.observedDeadlines()
	if len(deadlines) != 1 {
		t.Fatalf("observed %d admission deadlines, want 1", len(deadlines))
	}
	if deadlines[0] <= 0 || deadlines[0] > DeviceFactAdmissionTimeout {
		t.Fatalf("admission deadline remaining = %s, want within (0, %s]",
			deadlines[0], DeviceFactAdmissionTimeout)
	}
}

// TestHandleDeviceFactMessageLeavesUnreadableMetadataPending protects metadata
// handling and fails if a message whose broker metadata cannot be read is
// positively acknowledged, terminated, or executed.
func TestHandleDeviceFactMessageLeavesUnreadableMetadataPending(t *testing.T) {
	t.Parallel()
	validator := testValidator(t)
	msg := newRecordingDeviceFactMsg(observationFactMessage(t, validator, defaultObservationFactInput()))
	msg.metadataErr = errors.New("metadata unavailable")
	receiver := newFakeDeviceFactReceiver()
	handleDeviceFactMessage(context.Background(), msg, validator, receiver, discardLogger())

	acks, terms, nakDelays := msg.dispositions()
	if acks != 0 || terms != 0 || len(nakDelays) != 0 {
		t.Fatalf("unreadable metadata was dispositioned: ack=%d term=%d nak=%v", acks, terms, nakDelays)
	}
	if receiver.callCount() != 0 {
		t.Fatalf("admitted %d facts without metadata, want 0", receiver.callCount())
	}
}

// TestDeviceFactConsumerClosedWithoutSubscriptionIsClosed protects shutdown
// readiness and fails if a consumer that never subscribed can block a drain.
func TestDeviceFactConsumerClosedWithoutSubscriptionIsClosed(t *testing.T) {
	t.Parallel()
	consumer := &DeviceFactConsumer{}
	select {
	case <-consumer.Closed():
	default:
		t.Fatal("a consumer without a subscription is not closed")
	}
	if consumer.Active() {
		t.Fatal("a consumer without a subscription reports itself active")
	}
	if err := consumer.Drain(); err != nil {
		t.Fatalf("draining an unstarted consumer: %v", err)
	}
}
