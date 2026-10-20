//go:build integration

package nats //nolint:testpackage // Tests exercise package-private relay, mapping and drain behavior.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

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
		outbox, testDeviceFactValidator(t), logger, publisher, testDeviceFactRelayOptions(),
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
		outbox, testDeviceFactValidator(t), logger, publisher, testDeviceFactRelayOptions(),
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
	relay := startTestDeviceFactRelay(t, outbox, publisher, testDeviceFactRelayOptions())
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
	options.RetryBackoff = 5 * time.Millisecond
	publisher.persistFailure(true)
	firstRelay, err := startDeviceFactRelay(
		outbox, testDeviceFactValidator(t), discardLogger(), publisher, options,
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
	secondRelay := startTestDeviceFactRelay(t, outbox, restartPublisher, options)
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
