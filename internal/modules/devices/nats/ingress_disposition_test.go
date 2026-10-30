package nats //nolint:testpackage // Pin each consumer's distinct unexpected/context-failure policy.

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestObservationOperationalFailuresStayUnacknowledged(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{errors.New("storage fault"), context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			t.Parallel()
			validator := entityEventValidator(t)
			message := newAckOrderingTestMessage(
				t,
				ackOrderingObservationPayload(t, validator, testObservationID, time.Now().UTC()),
				testObservationID,
				nil,
			)
			calls := 0
			handleObservationMessage(
				t.Context(),
				message,
				validator,
				projectorFunc(
					func(context.Context, string, adapters.RuntimeID, devices.Observation, time.Time) (devices.ProjectionResult, error) {
						calls++
						return devices.ProjectionResult{}, cause
					},
				),
				slog.New(slog.DiscardHandler),
			)
			if calls != 1 || message.ackCount() != 0 || message.termCount() != 0 || message.plainNakCount() != 0 ||
				message.nakWithDelayCount() != 0 {
				t.Fatal("projection fault acquired a new acknowledgement or retry policy")
			}
		})
	}
}

func TestEntityEventOperationalFailuresKeepDelayedRedelivery(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{errors.New("storage fault"), context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			t.Parallel()
			message := newEntityEventTestMessage(
				t,
				devices.EntityID(testEntityID),
				entityEventEnvelope(
					t,
					testEntityEventID,
					devices.EntityID(testEntityID),
					"single_press",
					time.Now().UTC(),
				),
				nil,
			)
			recorder := &testEntityEventRecorder{err: cause}
			handleEntityEventMessage(
				t.Context(),
				message,
				entityEventValidator(t),
				recorder,
				slog.New(slog.DiscardHandler),
			)
			if len(recorder.recorded()) != 1 || message.ackCount() != 0 || message.termCount() != 0 ||
				message.plainNakCount() != 0 ||
				message.nakWithDelayCount() != 1 ||
				message.nakDelayRequested() != EntityEventRedeliveryDelay {
				t.Fatal("record fault changed delayed negative acknowledgement")
			}
		})
	}
}

func TestAvailabilityContextFailuresDoNotInventReplies(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if _, handled := mapAvailabilityResult(time.Time{}, 1, cause); handled {
			t.Fatalf("context failure invented a domain reply: %v", cause)
		}
	}
}
