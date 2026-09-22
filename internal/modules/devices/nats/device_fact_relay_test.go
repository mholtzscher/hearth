package nats //nolint:testpackage // Tests exercise package-private relay, mapping and drain behavior.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// TestDeviceFactRelayPublishesPendingFactsInOutboxOrderWithStableWire proves the
// happy path end to end: one worker reads the outbox oldest-first, maps each
// typed fact to the exact subject and a schema-valid envelope whose emitted_at is
// the stored commit time, restores the persisted trace headers, stamps the
// stable Nats-Msg-Id and expected stream, and deletes each row only after the
// acknowledgement.
func TestDeviceFactRelayPublishesPendingFactsInOutboxOrderWithStableWire(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	publisher := newRecordingDeviceFactPublisher(change)
	validator := testDeviceFactValidator(t)
	entityID := mustEntityID(t)
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	first := testObservationFact(t, entityID, observedAt, `true`, devices.DispositionApplied)
	second := testObservationFact(t, entityID, observedAt.Add(time.Minute), `{"level":3}`, devices.DispositionUnchanged)
	event := testEntityEventFact(t, entityID, devices.EntityEventName("single_press"), observedAt.Add(2*time.Minute))
	outbox.enqueue(pendingFact(1, first), pendingFact(2, second), pendingFact(3, event))

	relay := startTestDeviceFactRelay(t, outbox, publisher.publish, testDeviceFactRelayOptions())
	waitForDeviceFactCondition(t, change, func() bool { return len(outbox.deletedIDs()) == 3 })
	if !relay.Active() {
		t.Fatal("relay is not active after publishing")
	}

	records := publisher.recorded()
	if len(records) != 3 {
		t.Fatalf("publications = %d, want 3", len(records))
	}
	wantOrder := []string{string(first.ID), string(second.ID), string(event.ID)}
	for index, record := range records {
		if record.messageID != wantOrder[index] {
			t.Fatalf("publication %d message id = %q, want %q", index, record.messageID, wantOrder[index])
		}
	}
	if deleted := outbox.deletedIDs(); !slices.Equal(deleted, []devices.DeviceFactID{first.ID, second.ID, event.ID}) {
		t.Fatalf("deletions = %v, want enqueue order %v", deleted, wantOrder)
	}
	assertObservationDeviceFactMessage(t, validator, records[0], first)
	assertObservationDeviceFactMessage(t, validator, records[1], second)
	assertEntityEventDeviceFactMessage(t, validator, records[2], event)
}

// TestDeviceFactRelayRetriesTransientFailuresWithStableIdentityAndBytes proves a
// broker failure keeps the row and that every retry republishes the same
// identity, subject, trace headers and payload bytes, so the broker collapses a
// retry within its duplicate window and a reader can stay idempotent beyond it.
func TestDeviceFactRelayRetriesTransientFailuresWithStableIdentityAndBytes(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	publisher := newRecordingDeviceFactPublisher(change)
	fact := testObservationFact(t, mustEntityID(t), time.Now().UTC(), `"retry"`, devices.DispositionApplied)
	outbox.enqueue(pendingFact(1, fact))
	publisher.failNext(2)

	relay := startTestDeviceFactRelay(t, outbox, publisher.publish, testDeviceFactRelayOptions())
	waitForDeviceFactCondition(t, change, func() bool { return len(outbox.deletedIDs()) == 1 })

	if attempts := publisher.attemptCount(); attempts != 3 {
		t.Fatalf("publication attempts = %d, want 3", attempts)
	}
	if pending := outbox.pendingIDs(); len(pending) != 0 {
		t.Fatalf("pending facts after a successful retry = %v", pending)
	}
	if !relay.Active() || relay.fault() != nil {
		t.Fatal("a transient broker failure faulted the relay")
	}
	records := publisher.recorded()
	for index, record := range records[1:] {
		if record.subject != records[0].subject ||
			record.messageID != records[0].messageID ||
			record.expectedStream != records[0].expectedStream ||
			record.traceparent != records[0].traceparent ||
			!bytes.Equal(record.payload, records[0].payload) {
			t.Fatalf("retry %d changed the publication: %#v", index+1, record)
		}
	}
}

// TestDeviceFactRelayRetainsTheRowWhenTheAcknowledgementNamesAnotherStream
// proves the relay deletes nothing until the broker confirms the fact is stored
// in the expected stream. An acknowledgement naming any other stream is a
// retryable failure, not a licence to forget a durable fact.
func TestDeviceFactRelayRetainsTheRowWhenTheAcknowledgementNamesAnotherStream(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	publisher := newRecordingDeviceFactPublisher(change)
	publisher.acknowledgeInStream("SOME_OTHER_STREAM")
	outbox.enqueue(pendingFact(1, testObservationFact(
		t, mustEntityID(t), time.Now().UTC(), `true`, devices.DispositionApplied,
	)))

	startTestDeviceFactRelay(t, outbox, publisher.publish, testDeviceFactRelayOptions())
	waitForDeviceFactCondition(t, change, func() bool { return publisher.attemptCount() >= 2 })
	if deletions := outbox.deletedIDs(); len(deletions) != 0 {
		t.Fatalf("relay deleted a fact acknowledged into another stream: %v", deletions)
	}
	if pending := outbox.pendingIDs(); len(pending) != 1 {
		t.Fatalf("pending facts = %v, want the retained row", pending)
	}

	publisher.acknowledgeInStream(DeviceFactStreamName)
	waitForDeviceFactCondition(t, change, func() bool { return len(outbox.deletedIDs()) == 1 })
}

// TestDeviceFactRelayRetriesDeleteFailureWithoutDroppingTheFact proves a delete
// failure keeps the acknowledged row pending and that the retry republishes the
// identical bytes before deleting it.
func TestDeviceFactRelayRetriesDeleteFailureWithoutDroppingTheFact(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	publisher := newRecordingDeviceFactPublisher(change)
	fact := testObservationFact(t, mustEntityID(t), time.Now().UTC(), `false`, devices.DispositionApplied)
	outbox.enqueue(pendingFact(1, fact))
	outbox.failDeletes(1)

	startTestDeviceFactRelay(t, outbox, publisher.publish, testDeviceFactRelayOptions())
	waitForDeviceFactCondition(t, change, func() bool { return len(outbox.deletedIDs()) == 1 })

	if attempts := publisher.attemptCount(); attempts != 2 {
		t.Fatalf("publication attempts = %d, want the rejected delete to republish once", attempts)
	}
	records := publisher.recorded()
	if !bytes.Equal(records[0].payload, records[1].payload) || records[0].messageID != records[1].messageID {
		t.Fatal("the retry after a delete failure changed the publication")
	}
}

// TestDeviceFactRelayTreatsDuplicateAcknowledgementAsSuccess proves a broker
// duplicate ack is a successful publication: the fact is already stored under
// the same Nats-Msg-Id, so the row is deleted instead of republished forever.
func TestDeviceFactRelayTreatsDuplicateAcknowledgementAsSuccess(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	publisher := newRecordingDeviceFactPublisher(change)
	publisher.reportDuplicates()
	outbox.enqueue(pendingFact(1, testObservationFact(
		t, mustEntityID(t), time.Now().UTC(), `true`, devices.DispositionApplied,
	)))

	startTestDeviceFactRelay(t, outbox, publisher.publish, testDeviceFactRelayOptions())
	waitForDeviceFactCondition(t, change, func() bool { return len(outbox.deletedIDs()) == 1 })
	if attempts := publisher.attemptCount(); attempts != 1 {
		t.Fatalf("publication attempts = %d, want 1", attempts)
	}
}

// TestDeviceFactRelayFaultsOnPoisonFactAndPreservesTheRow proves a deterministic
// mapping, subject or schema failure never loses durable evidence: nothing is
// published, nothing is deleted, the relay stops, Active fails and Drain reports
// the poison row.
//
//nolint:gocognit // The table asserts one identical fault contract for three deterministic failure classes.
func TestDeviceFactRelayFaultsOnPoisonFactAndPreservesTheRow(t *testing.T) {
	t.Parallel()
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		poison func(t *testing.T) devices.ObservationFact
		stage  string
		code   string
	}{
		{
			name: "subject invalid",
			poison: func(t *testing.T) devices.ObservationFact {
				t.Helper()
				return testObservationFact(
					t, devices.EntityID("not-a-canonical-entity"), observedAt, `true`, devices.DispositionApplied,
				)
			},
			stage: deviceFactStageMap,
			code:  deviceFactCodeSubjectInvalid,
		},
		{
			name: "stored fact invalid",
			poison: func(t *testing.T) devices.ObservationFact {
				t.Helper()
				fact := testObservationFact(t, mustEntityID(t), observedAt, `true`, devices.DispositionApplied)
				fact.CreatedAt = time.Time{}
				return fact
			},
			stage: deviceFactStageMap,
			code:  deviceFactCodeFactInvalid,
		},
		{
			name: "schema invalid",
			poison: func(t *testing.T) devices.ObservationFact {
				t.Helper()
				return testObservationFact(t, mustEntityID(t), observedAt, `not-json`, devices.DispositionApplied)
			},
			stage: deviceFactStageEncode,
			code:  deviceFactCodeEncodeFailed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			change := newDeviceFactChange()
			outbox := newFakeDeviceFactOutbox(change)
			publisher := newRecordingDeviceFactPublisher(change)
			logs, logger := newTestLogSink()
			poison := test.poison(t)
			healthy := testObservationFact(t, mustEntityID(t), observedAt, `true`, devices.DispositionApplied)
			outbox.enqueue(pendingFact(1, poison), pendingFact(2, healthy))

			relay, err := startDeviceFactRelay(
				outbox, testDeviceFactValidator(t), logger, publisher.publish, testDeviceFactRelayOptions(),
			)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-relay.Closed():
			case <-time.After(testFactLiveness):
				t.Fatal("relay did not stop after a poison fact")
			}
			if relay.Active() {
				t.Fatal("relay is active after a poison fact")
			}
			if attempts := publisher.attemptCount(); attempts != 0 {
				t.Fatalf("a poison fact was published %d times", attempts)
			}
			if deletions := outbox.deletedIDs(); len(deletions) != 0 {
				t.Fatalf("a poison fact was deleted: %v", deletions)
			}
			if pending := outbox.pendingIDs(); len(pending) != 2 {
				t.Fatalf("poison path dropped durable rows: %v", pending)
			}

			drainErr := relay.Drain(context.Background())
			var poisonErr *deviceFactPoisonError
			if !errors.As(drainErr, &poisonErr) {
				t.Fatalf("drain error = %v, want the poison error", drainErr)
			}
			if poisonErr.stage != test.stage || poisonErr.code != test.code {
				t.Fatalf("poison error = %#v, want stage %s code %s", poisonErr, test.stage, test.code)
			}
			poisonLogs := logEvents(logs.records(t), deviceFactEventPoison)
			if len(poisonLogs) != 1 {
				t.Fatalf("poison diagnostics = %d, want 1:\n%s", len(poisonLogs), logs.output())
			}
			if poisonLogs[0]["stage"] != test.stage ||
				poisonLogs[0]["error_code"] != test.code ||
				poisonLogs[0]["fact_id"] != string(poison.ID) ||
				poisonLogs[0]["family"] != string(devices.DeviceFactFamilyObservation) ||
				poisonLogs[0]["observation_id"] != string(poison.ObservationID) ||
				poisonLogs[0]["level"] != "ERROR" {
				t.Fatalf("poison diagnostic = %#v", poisonLogs[0])
			}
		})
	}
}

// TestDeviceFactRelayFaultsOnCorruptOutboxRowAndPreservesTheRow proves a durable
// row Core cannot decode is permanent poison, not a retryable read failure: the
// relay publishes nothing, deletes nothing, keeps the row, stops, reports Active
// false and returns the invalid-row fault from Drain, so a corrupt row fails
// readiness exactly like a mapping poison instead of being retried forever.
func TestDeviceFactRelayFaultsOnCorruptOutboxRowAndPreservesTheRow(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	database, outbox := openDeviceFactOutbox(t, change)
	publisher := newRecordingDeviceFactPublisher(change)
	logs, logger := newTestLogSink()
	fact := testObservationFact(
		t, mustEntityID(t), time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), `true`, devices.DispositionApplied,
	)
	insertPendingObservationFact(t, database, fact)
	corruptOutboxRowTimestamp(t, database, fact.ID)

	relay, err := startDeviceFactRelay(
		outbox, testDeviceFactValidator(t), logger, publisher.publish, testDeviceFactRelayOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-relay.Closed():
	case <-time.After(testFactLiveness):
		t.Fatal("relay did not stop after a corrupt outbox row")
	}
	if relay.Active() {
		t.Fatal("relay is active after a corrupt outbox row")
	}
	if attempts := publisher.attemptCount(); attempts != 0 {
		t.Fatalf("a corrupt outbox row was published %d times", attempts)
	}
	if remaining := countPendingDeviceFacts(t, database); remaining != 1 {
		t.Fatalf("pending rows after the fault = %d, want the corrupt row preserved", remaining)
	}

	drainErr := relay.Drain(context.Background())
	var poisonErr *deviceFactPoisonError
	if !errors.As(drainErr, &poisonErr) {
		t.Fatalf("drain error = %v, want the invalid-row poison error", drainErr)
	}
	if poisonErr.stage != deviceFactStageList || poisonErr.code != deviceFactCodeInvalidRow ||
		poisonErr.factID != string(fact.ID) {
		t.Fatalf("poison error = %#v, want stage %s code %s for %s",
			poisonErr, deviceFactStageList, deviceFactCodeInvalidRow, fact.ID)
	}
	if !errors.Is(drainErr, devices.ErrInvalidDeviceFactRow) {
		t.Fatalf("drain error = %v, want the permanent invalid-row class", drainErr)
	}
	poisonLogs := logEvents(logs.records(t), deviceFactEventPoison)
	if len(poisonLogs) != 1 {
		t.Fatalf("poison diagnostics = %d, want 1:\n%s", len(poisonLogs), logs.output())
	}
	if poisonLogs[0]["stage"] != deviceFactStageList ||
		poisonLogs[0]["error_code"] != deviceFactCodeInvalidRow ||
		poisonLogs[0]["fact_id"] != string(fact.ID) ||
		poisonLogs[0]["level"] != "ERROR" {
		t.Fatalf("poison diagnostic = %#v", poisonLogs[0])
	}
	if retries := logEvents(logs.records(t), deviceFactEventRetry); len(retries) != 0 {
		t.Fatalf("corrupt outbox row was retried instead of faulted:\n%s", logs.output())
	}
}

// TestDeviceFactRelayPublishesThePrefixOlderThanACorruptRowAndBlocksOnlyRowsBehindIt
// proves an outbox fault never discards decoded work: the valid facts older than
// a corrupt row are published and deleted in enqueue order, then the relay stops
// with the corrupt row and every row behind it still durable. The oracle is ADR
// 0020's "deliver or fault, never discard": the poison row is preserved and stops
// the relay, and only the facts queued behind it stay blocked.
func TestDeviceFactRelayPublishesThePrefixOlderThanACorruptRowAndBlocksOnlyRowsBehindIt(
	t *testing.T,
) {
	t.Parallel()
	change := newDeviceFactChange()
	database, outbox := openDeviceFactOutbox(t, change)
	publisher := newRecordingDeviceFactPublisher(change)
	logs, logger := newTestLogSink()
	entityID := mustEntityID(t)
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	olderObservation := testObservationFact(t, entityID, observedAt, `true`, devices.DispositionApplied)
	olderEvent := testEntityEventFact(
		t, entityID, devices.EntityEventName("single_press"), observedAt.Add(time.Minute),
	)
	poison := testObservationFact(t, entityID, observedAt.Add(2*time.Minute), `false`, devices.DispositionApplied)
	behindPoison := testObservationFact(t, entityID, observedAt.Add(3*time.Minute), `true`, devices.DispositionApplied)
	insertPendingObservationFact(t, database, olderObservation)
	insertPendingEntityEventFact(t, database, olderEvent)
	insertPendingObservationFact(t, database, poison)
	insertPendingObservationFact(t, database, behindPoison)
	corruptOutboxRowTimestamp(t, database, poison.ID)

	relay, err := startDeviceFactRelay(
		outbox, testDeviceFactValidator(t), logger, publisher.publish, testDeviceFactRelayOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-relay.Closed():
	case <-time.After(testFactLiveness):
		t.Fatal("relay did not stop after a corrupt outbox row")
	}
	if relay.Active() {
		t.Fatal("relay is active after a corrupt outbox row")
	}

	// Only the facts older than the corrupt row are published, in enqueue order;
	// nothing behind the corrupt row is attempted.
	records := publisher.recorded()
	if len(records) != 2 || records[0].messageID != string(olderObservation.ID) ||
		records[1].messageID != string(olderEvent.ID) {
		t.Fatalf("publications = %#v, want the valid prefix older than the poison row", records)
	}
	wantRemaining := []string{string(poison.ID), string(behindPoison.ID)}
	if remaining := pendingFactIDs(t, database); !slices.Equal(remaining, wantRemaining) {
		t.Fatalf("pending rows = %v, want the poison row and the row behind it", remaining)
	}

	drainErr := relay.Drain(context.Background())
	var poisonErr *deviceFactPoisonError
	if !errors.As(drainErr, &poisonErr) {
		t.Fatalf("drain error = %v, want the invalid-row poison error", drainErr)
	}
	if poisonErr.stage != deviceFactStageList || poisonErr.code != deviceFactCodeInvalidRow ||
		poisonErr.factID != string(poison.ID) {
		t.Fatalf("poison error = %#v, want stage %s code %s for %s",
			poisonErr, deviceFactStageList, deviceFactCodeInvalidRow, poison.ID)
	}
	if !errors.Is(drainErr, devices.ErrInvalidDeviceFactRow) {
		t.Fatalf("drain error = %v, want the permanent invalid-row class", drainErr)
	}
	if retries := logEvents(logs.records(t), deviceFactEventRetry); len(retries) != 0 {
		t.Fatalf("delivering the valid prefix logged a retry:\n%s", logs.output())
	}
	poisonLogs := logEvents(logs.records(t), deviceFactEventPoison)
	if len(poisonLogs) != 1 || poisonLogs[0]["fact_id"] != string(poison.ID) {
		t.Fatalf("poison diagnostics = %#v, want the corrupt row", poisonLogs)
	}
}

// TestDeviceFactRelayHoldsNoDatabaseLockAcrossPublish proves the relay reads the
// pending batch and releases the single SQLite connection before it publishes,
// so a stalled broker cannot block a durable write.
func TestDeviceFactRelayHoldsNoDatabaseLockAcrossPublish(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	database, outbox := openDeviceFactOutbox(t, change)
	publisher := newRecordingDeviceFactPublisher(change)
	entityID := mustEntityID(t)
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	insertPendingObservationFact(t, database, testObservationFact(
		t, entityID, observedAt, `true`, devices.DispositionApplied,
	))

	release := make(chan struct{})
	publisher.holdAllPublications(release)
	relay := startTestDeviceFactRelay(t, outbox, publisher.publish, testDeviceFactRelayOptions())
	waitForDeviceFactCondition(t, change, func() bool { return publisher.attemptCount() >= 1 })
	if !relay.Active() {
		t.Fatal("relay is not active while publishing")
	}

	written := make(chan error, 1)
	go func() {
		second := testObservationFact(t, entityID, observedAt.Add(time.Minute), `false`, devices.DispositionApplied)
		written <- insertObservationDeviceFact(context.Background(), database, second)
	}()
	select {
	case writeErr := <-written:
		if writeErr != nil {
			t.Fatal(writeErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the durable outbox was locked while a publication was in flight")
	}
	close(release)
	waitForDeviceFactCondition(t, change, func() bool { return countPendingDeviceFacts(t, database) == 0 })
}

// TestDeviceFactRelayWakesOnNotifierWithoutPolling proves the notifier is the
// low-latency path: with a one-hour poll interval, only the wake can publish a
// fact committed after the relay went idle.
func TestDeviceFactRelayWakesOnNotifierWithoutPolling(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	publisher := newRecordingDeviceFactPublisher(change)
	relay := startTestDeviceFactRelay(t, outbox, publisher.publish, testDeviceFactRelayOptions())
	waitForDeviceFactCondition(t, change, func() bool { return outbox.listCount() >= 1 })

	outbox.enqueue(pendingFact(1, testObservationFact(
		t, mustEntityID(t), time.Now().UTC(), `true`, devices.DispositionApplied,
	)))
	relay.NotifyPendingDeviceFacts()
	waitForDeviceFactCondition(t, change, func() bool { return len(outbox.deletedIDs()) == 1 })
}

// TestDeviceFactRelayPollRecoversLostWake proves the periodic poll is the
// authoritative rediscovery path: a fact committed without a wake hint is still
// published by the next poll, so a lost hint costs latency and never a fact.
func TestDeviceFactRelayPollRecoversLostWake(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	publisher := newRecordingDeviceFactPublisher(change)
	options := testDeviceFactRelayOptions()
	options.pollInterval = 20 * time.Millisecond
	startTestDeviceFactRelay(t, outbox, publisher.publish, options)
	waitForDeviceFactCondition(t, change, func() bool { return outbox.listCount() >= 1 })

	outbox.enqueue(pendingFact(1, testObservationFact(
		t, mustEntityID(t), time.Now().UTC(), `true`, devices.DispositionApplied,
	)))
	waitForDeviceFactCondition(t, change, func() bool { return len(outbox.deletedIDs()) == 1 })
}

// TestDeviceFactRelayRetriesOutboxReadFailure proves a durable read failure keeps
// every row and is retried with the fixed backoff instead of being treated as an
// empty outbox that has nothing left to publish. It also proves an ordinary read
// failure is not poison: the relay never faults, and the retry publishes and
// deletes the row.
func TestDeviceFactRelayRetriesOutboxReadFailure(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	publisher := newRecordingDeviceFactPublisher(change)
	outbox.enqueue(pendingFact(1, testObservationFact(
		t, mustEntityID(t), time.Now().UTC(), `true`, devices.DispositionApplied,
	)))
	outbox.failLists(2)

	relay := startTestDeviceFactRelay(t, outbox, publisher.publish, testDeviceFactRelayOptions())
	waitForDeviceFactCondition(t, change, func() bool { return len(outbox.deletedIDs()) == 1 })
	if lists := outbox.listCount(); lists < 3 {
		t.Fatalf("outbox reads = %d, want the failed reads retried", lists)
	}
	if !relay.Active() {
		t.Fatal("relay is inactive after a transient read failure")
	}
	if fault := relay.fault(); fault != nil {
		t.Fatalf("transient read failure faulted the relay: %v", fault)
	}
}

// TestDeviceFactRelayDrainPublishesPendingRows proves Drain is not a discard: it
// stops the worker and then publishes the pending rows itself with the same
// stable mapping.
func TestDeviceFactRelayDrainPublishesPendingRows(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	publisher := newRecordingDeviceFactPublisher(change)
	fact := testObservationFact(t, mustEntityID(t), time.Now().UTC(), `true`, devices.DispositionApplied)
	outbox.enqueue(pendingFact(1, fact))
	options := testDeviceFactRelayOptions()
	// Park the worker in an effectively infinite backoff so only Drain can
	// publish the pending row.
	options.retryBackoff = time.Hour
	publisher.persistFailure(true)

	relay := startTestDeviceFactRelay(t, outbox, publisher.publish, options)
	waitForDeviceFactCondition(t, change, func() bool { return publisher.attemptCount() == 1 })
	publisher.persistFailure(false)

	drainContext, cancelDrain := context.WithTimeout(context.Background(), testFactLiveness)
	defer cancelDrain()
	if err := relay.Drain(drainContext); err != nil {
		t.Fatal(err)
	}
	if attempts := publisher.attemptCount(); attempts != 2 {
		t.Fatalf("publication attempts = %d, want the drain to publish once", attempts)
	}
	if deleted := outbox.deletedIDs(); !slices.Equal(deleted, []devices.DeviceFactID{fact.ID}) {
		t.Fatalf("deletions after drain = %v", deleted)
	}
	select {
	case <-relay.Closed():
	case <-time.After(testFactLiveness):
		t.Fatal("relay worker did not stop after drain")
	}
	if relay.Active() {
		t.Fatal("relay is active after drain")
	}
}

// TestDeviceFactRelayDrainExpiryLeavesRowsForRestart proves a drain that cannot
// finish inside its context leaves every uncommitted row in the durable outbox --
// across both fact families -- and that a restarted relay publishes them in
// enqueue order with the stable mapping.
func TestDeviceFactRelayDrainExpiryLeavesRowsForRestart(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	database, outbox := openDeviceFactOutbox(t, change)
	publisher := newRecordingDeviceFactPublisher(change)
	entityID := mustEntityID(t)
	observedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	observation := testObservationFact(t, entityID, observedAt, `true`, devices.DispositionApplied)
	event := testEntityEventFact(t, entityID, devices.EntityEventName("single_press"), observedAt.Add(time.Minute))
	insertPendingObservationFact(t, database, observation)
	insertPendingEntityEventFact(t, database, event)

	options := testDeviceFactRelayOptions()
	options.retryBackoff = 5 * time.Millisecond
	publisher.persistFailure(true)
	firstRelay, err := startDeviceFactRelay(
		outbox, testDeviceFactValidator(t), discardLogger(), publisher.publish, options,
	)
	if err != nil {
		t.Fatal(err)
	}
	waitForDeviceFactCondition(t, change, func() bool { return publisher.attemptCount() >= 1 })

	drainContext, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
	drainErr := firstRelay.Drain(drainContext)
	cancelDrain()
	if drainErr == nil {
		t.Fatal("drain reported success while every publication failed")
	}
	select {
	case <-firstRelay.Closed():
	case <-time.After(testFactLiveness):
		t.Fatal("relay worker did not stop after an expired drain")
	}
	if remaining := countPendingDeviceFacts(t, database); remaining != 2 {
		t.Fatalf("pending facts after an expired drain = %d, want 2", remaining)
	}

	restartPublisher := newRecordingDeviceFactPublisher(change)
	publisher.persistFailure(false)
	secondRelay := startTestDeviceFactRelay(t, outbox, restartPublisher.publish, options)
	waitForDeviceFactCondition(t, change, func() bool { return countPendingDeviceFacts(t, database) == 0 })
	if !secondRelay.Active() {
		t.Fatal("restarted relay is not active")
	}
	records := restartPublisher.recorded()
	if len(records) != 2 || records[0].messageID != string(observation.ID) ||
		records[1].messageID != string(event.ID) {
		t.Fatalf("restart publications = %#v, want the pending rows in enqueue order", records)
	}
	validator := testDeviceFactValidator(t)
	assertObservationDeviceFactMessage(t, validator, records[0], observation)
	assertEntityEventDeviceFactMessage(t, validator, records[1], event)
}

// TestDeviceFactRelayDrainAbortsAnInFlightPublication proves Drain cancels the
// worker's publication pass instead of waiting out the publish bound: the stalled
// attempt ends on cancellation, the unacknowledged row stays durable, and the
// drain then publishes and deletes it itself.
func TestDeviceFactRelayDrainAbortsAnInFlightPublication(t *testing.T) {
	t.Parallel()
	change := newDeviceFactChange()
	outbox := newFakeDeviceFactOutbox(change)
	publisher := newRecordingDeviceFactPublisher(change)
	publisher.holdNextPublications(1, nil)
	fact := testObservationFact(t, mustEntityID(t), time.Now().UTC(), `true`, devices.DispositionApplied)
	outbox.enqueue(pendingFact(1, fact))
	options := testDeviceFactRelayOptions()
	// Only cancellation can end the stalled publication, so a drain that waited
	// for the publish bound would fail the elapsed-time assertion below.
	options.publishTimeout = time.Hour

	relay := startTestDeviceFactRelay(t, outbox, publisher.publish, options)
	waitForDeviceFactCondition(t, change, func() bool { return publisher.attemptCount() >= 1 })

	started := time.Now()
	drainContext, cancelDrain := context.WithTimeout(context.Background(), testFactLiveness)
	defer cancelDrain()
	if err := relay.Drain(drainContext); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("drain waited %s for a stalled publication", elapsed)
	}
	if deleted := outbox.deletedIDs(); !slices.Equal(deleted, []devices.DeviceFactID{fact.ID}) {
		t.Fatalf("deletions after drain = %v, want the row published by the drain", deleted)
	}
}

// An expired join must not start a drain publisher beside a worker still in I/O.
func TestDeviceFactRelayDrainTimeoutKeepsSinglePublisher(t *testing.T) {
	t.Parallel()
	outbox := newFakeDeviceFactOutbox(newDeviceFactChange())
	fact := testObservationFact(t, mustEntityID(t), time.Now().UTC(), `true`, devices.DispositionApplied)
	outbox.enqueue(pendingFact(1, fact))
	entered := make(chan context.Context, 1)
	blocked := make(chan struct{})
	release := sync.OnceFunc(func() { close(blocked) })
	defer release()
	var attempts atomic.Int32
	publish := func(ctx context.Context, _ *natsgo.Msg) (*jetstream.PubAck, error) {
		if attempts.Add(1) == 1 {
			entered <- ctx
			<-blocked
			return nil, ctx.Err()
		}
		return &jetstream.PubAck{Stream: DeviceFactStreamName}, nil
	}
	relay := startTestDeviceFactRelay(t, outbox, publish, testDeviceFactRelayOptions())
	var publicationContext context.Context
	select {
	case publicationContext = <-entered:
	case <-time.After(testFactLiveness):
		t.Fatal("worker did not enter publication")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := relay.Drain(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain = %v, want canceled wait", err)
	}
	if publicationContext.Err() != context.Canceled {
		t.Fatal("Drain did not cancel the worker publication")
	}
	if relay.Active() {
		t.Fatal("expired Drain left readiness active")
	}
	select {
	case <-relay.Closed():
		t.Fatal("expired Drain untracked a live publisher")
	default:
	}
	if attempts.Load() != 1 || len(outbox.deletedIDs()) != 0 {
		t.Fatal("expired join started another publisher or deleted the pending row")
	}
	release()
	joined, cancelJoin := context.WithTimeout(t.Context(), testFactLiveness)
	defer cancelJoin()
	if err := relay.Drain(joined); err != nil {
		t.Fatalf("later Drain = %v", err)
	}
	if attempts.Load() != 2 || !slices.Equal(outbox.deletedIDs(), []devices.DeviceFactID{fact.ID}) {
		t.Fatal("later Drain did not publish and delete the retained row once")
	}
}

func TestDeviceFactRelayValidatesDependencies(t *testing.T) {
	t.Parallel()
	validator := testDeviceFactValidator(t)
	outbox := newFakeDeviceFactOutbox(newDeviceFactChange())
	publisher := func(context.Context, *natsgo.Msg) (*jetstream.PubAck, error) {
		return &jetstream.PubAck{Stream: DeviceFactStreamName}, nil
	}
	options := defaultDeviceFactRelayOptions()

	if _, err := StartDeviceFactRelay(nil, outbox, validator, nil); err == nil {
		t.Fatal("relay accepted a nil JetStream context")
	}
	tests := []struct {
		name    string
		outbox  devices.DeviceFactOutbox
		valid   *contractsv1.Validator
		publish deviceFactPublisher
		options deviceFactRelayOptions
	}{
		{"outbox", nil, validator, publisher, options},
		{"validator", outbox, nil, publisher, options},
		{"publisher", outbox, validator, nil, options},
		{"batch size", outbox, validator, publisher, deviceFactRelayOptions{
			batchSize: 0, pollInterval: time.Second, retryBackoff: time.Second, publishTimeout: time.Second,
		}},
		{"poll interval", outbox, validator, publisher, deviceFactRelayOptions{
			batchSize: 1, pollInterval: 0, retryBackoff: time.Second, publishTimeout: time.Second,
		}},
		{"retry backoff", outbox, validator, publisher, deviceFactRelayOptions{
			batchSize: 1, pollInterval: time.Second, retryBackoff: 0, publishTimeout: time.Second,
		}},
		{"publish timeout", outbox, validator, publisher, deviceFactRelayOptions{
			batchSize: 1, pollInterval: time.Second, retryBackoff: time.Second, publishTimeout: 0,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := startDeviceFactRelay(
				test.outbox, test.valid, discardLogger(), test.publish, test.options,
			); err == nil {
				t.Fatalf("relay accepted invalid %s", test.name)
			}
		})
	}
}

// assertObservationDeviceFactMessage proves one published Observation message
// carries the canonical subject, the restored trace context, the stable
// identity headers and a schema-valid envelope built from the stored row.
func assertObservationDeviceFactMessage(
	t *testing.T,
	validator *contractsv1.Validator,
	record recordedDeviceFactMessage,
	fact devices.ObservationFact,
) {
	t.Helper()
	wantSubject, subjectErr := natswire.ObservationFactSubject(string(fact.EntityID), string(fact.Disposition))
	if subjectErr != nil {
		t.Fatal(subjectErr)
	}
	if record.subject != wantSubject {
		t.Fatalf("subject = %q, want %q", record.subject, wantSubject)
	}
	if record.expectedStream != DeviceFactStreamName {
		t.Fatalf("expected stream = %q, want %q", record.expectedStream, DeviceFactStreamName)
	}
	if record.traceparent != fact.Trace.Traceparent || record.tracestate != fact.Trace.Tracestate {
		t.Fatalf("trace headers = %q/%q, want %q/%q",
			record.traceparent, record.tracestate, fact.Trace.Traceparent, fact.Trace.Tracestate)
	}
	envelope := decodeTestFactEnvelope(t, validator, contractsv1.ObservationFactSchemaID, record.payload)
	if envelope.ID != string(fact.ID) || envelope.Schema != contractsv1.ObservationFactSchemaID {
		t.Fatalf("envelope identity = %#v", envelope)
	}
	if envelope.EmittedAt != fact.CreatedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("envelope emitted_at = %q, want the stored commit time", envelope.EmittedAt)
	}
	if envelope.CorrelationID != string(fact.CorrelationID) {
		t.Fatalf("envelope correlation = %q", envelope.CorrelationID)
	}
	if envelope.CausationID == nil || *envelope.CausationID != string(fact.ObservationID) {
		t.Fatalf("envelope causation = %v", envelope.CausationID)
	}
	var data observationFactData
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.ObservationID != string(fact.ObservationID) ||
		data.EntityID != string(fact.EntityID) ||
		data.Disposition != string(fact.Disposition) ||
		string(data.Value) != string(fact.Value) ||
		string(data.PreviousValue) != string(fact.PreviousValue) ||
		data.AdapterReceivedAt != fact.AdapterReceivedAt.UTC().Format(time.RFC3339Nano) ||
		data.ObservedAt != fact.ObservedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("observation fact data = %#v", data)
	}
	if len(fact.PreviousValue) == 0 && data.PreviousValue != nil {
		t.Fatalf("absent previous_value encoded as %s", data.PreviousValue)
	}
	if fact.SourceUpdatedAt == nil || data.SourceUpdatedAt == nil ||
		*data.SourceUpdatedAt != fact.SourceUpdatedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("observation source_updated_at = %v", data.SourceUpdatedAt)
	}
}

// assertEntityEventDeviceFactMessage proves one published Entity Event message
// carries its canonical subject and a schema-valid envelope built from the
// stored row.
func assertEntityEventDeviceFactMessage(
	t *testing.T,
	validator *contractsv1.Validator,
	record recordedDeviceFactMessage,
	fact devices.EntityEventFact,
) {
	t.Helper()
	wantSubject, subjectErr := natswire.EntityEventFactSubject(string(fact.EntityID), string(fact.Name))
	if subjectErr != nil {
		t.Fatal(subjectErr)
	}
	if record.subject != wantSubject {
		t.Fatalf("subject = %q, want %q", record.subject, wantSubject)
	}
	if record.traceparent != fact.Trace.Traceparent || record.tracestate != fact.Trace.Tracestate {
		t.Fatalf("trace headers = %q/%q", record.traceparent, record.tracestate)
	}
	envelope := decodeTestFactEnvelope(t, validator, contractsv1.EntityEventFactSchemaID, record.payload)
	if envelope.ID != string(fact.ID) ||
		envelope.Schema != contractsv1.EntityEventFactSchemaID ||
		envelope.EmittedAt != fact.CreatedAt.UTC().Format(time.RFC3339Nano) ||
		envelope.CorrelationID != string(fact.CorrelationID) {
		t.Fatalf("entity event envelope = %#v", envelope)
	}
	if envelope.CausationID == nil || *envelope.CausationID != string(fact.EventID) {
		t.Fatalf("entity event causation = %v", envelope.CausationID)
	}
	var data entityEventFactData
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.EventID != string(fact.EventID) ||
		data.EntityID != string(fact.EntityID) ||
		data.Name != string(fact.Name) ||
		data.ReportedAt != fact.ReportedAt.UTC().Format(time.RFC3339Nano) ||
		data.ReceivedAt != fact.ReceivedAt.UTC().Format(time.RFC3339Nano) ||
		data.RecordedAt != fact.RecordedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("entity event data = %#v", data)
	}
}
