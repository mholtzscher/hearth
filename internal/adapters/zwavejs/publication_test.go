package zwavejs //nolint:testpackage // Tests drive the coordinator mailbox to order queue admission.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// A blocked publisher must not accumulate unbounded work, drain stale batches after teardown, or prepare linked
// evidence after a sibling enqueue ends its generation.
func TestRuntimePublicationOverflowRecoversWithFreshSnapshot(t *testing.T) {
	t.Parallel()
	for _, trigger := range []string{"ordinary", "poll sibling", "poll linked"} {
		t.Run(trigger, func(t *testing.T) {
			t.Parallel()
			checkPublicationOverflowRecovery(t, trigger)
		})
	}
}

func checkPublicationOverflowRecovery(t *testing.T, trigger string) {
	t.Helper()
	recorder := &runtimeRecorder{}
	session := newRuntimeSession(recorder)
	snapshot := snapshotFixture(testHomeID,
		switchNodeFixture(23, "Switch"), dimmerNodeFixture(24, "Dimmer"))
	first := newFakeConnection(recorder, versionFixture(), snapshot)
	second := newFakeConnection(recorder, versionFixture(), snapshot)
	zwave, dialer := reconciledRuntime(t, session, first, second)
	waitForRoutesActivated(t, session)
	baseline := len(session.recordedObservations())

	entered := make(chan struct{})
	cancelled := make(chan struct{})
	var calls atomic.Int32
	session.setPublishHook(func(ctx context.Context, observation adapter.Observation) error {
		if string(observation.Value) != "false" {
			return nil
		}
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			close(cancelled)
		}
		return ctx.Err()
	})
	ctx, cancel := context.WithTimeout(t.Context(), harnessTimeout)
	defer cancel()
	send := func() {
		t.Helper()
		select {
		case zwave.runtimeEvents <- upstreamEvent{
			generation: 1, connection: first,
			event: receivedEvent{
				Event: valueUpdatedEventForNode(23,
					testValueID(commandClassBinarySwitch, 0, valuePropertyCurrentValue), "false"),
				ReceivedAt: time.Now().UTC(),
			},
		}:
		case <-ctx.Done():
			t.Fatal("coordinator stopped consuming Events")
		}
	}
	send()
	awaitSignal(t, entered, "the active publication to block")
	pending := publicationQueueLimit
	if trigger == "poll linked" {
		pending-- // Reserve room for the poll's ordinary power sibling.
	}
	for range pending {
		send()
	}
	if trigger == "ordinary" {
		send()
	} else {
		responder := newFakeResponder(recorder, session)
		command := commandFixture(routeEntityID(24, "brightness"), `{"value":15}`, time.Now().Add(time.Minute))
		if err := runCommand(t, zwave, command, responder); err != nil {
			t.Fatal(err)
		}
		if accepted, rejected, total := responderCounts(
			responder,
		); accepted != 1 || rejected != 0 ||
			total != 1 {
			t.Fatalf("responses = %d/%d/%d, want one acceptance", accepted, rejected, total)
		}
	}
	awaitSignal(t, cancelled, "overflow to cancel the active publication")
	waitFor(t, "fresh snapshot after overflow", func() bool {
		return dialer.dialCount() >= 2 && len(session.recordedObservations()) >= baseline*2
	})
	assertPublicationOverflowRecovered(t, session, zwave, baseline, calls.Load())
}

func assertPublicationOverflowRecovered(
	t *testing.T,
	session *runtimeSession,
	zwave *Adapter,
	baseline int,
	calls int32,
) {
	t.Helper()
	if !session.logs.has("adapter.publication_queue_overflowed") {
		t.Fatal("generation ended without a publication overflow diagnostic")
	}
	if calls != 1 {
		t.Fatalf("stale publication calls = %d, want only the canceled active call", calls)
	}
	if got := len(session.recordedObservations()); got != baseline*2 {
		t.Fatalf("Observations = %d, want only the two snapshots", got)
	}
	if got := len(session.recordedLinked()); got != 0 {
		t.Fatalf("linked Observations = %d, want none from the overflowing generation", got)
	}
	select {
	case <-zwave.runtimeDone:
		t.Fatal("publication overflow stopped the Adapter instead of reconnecting")
	default:
	}
}
