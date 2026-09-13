package ecowitt //nolint:testpackage // Availability tests exercise the package-private freshness tracker.

import (
	"fmt"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// trackerRoutes builds a synthetic snapshot with one Entity per index, so a
// batch-bound test can exceed the SDK's maximum batch size.
func trackerRoutes(entityCount int) routeSnapshot {
	snapshot := routeSnapshot{}
	for index := range entityCount {
		snapshot.entities = append(snapshot.entities, entityRoute{
			Index:     index,
			Slot:      outdoorArraySlot,
			EntityKey: fmt.Sprintf("synthetic-%03d", index),
			EntityID:  fmt.Sprintf("ent-synthetic-%03d", index),
		})
	}
	return snapshot
}

// TestAvailabilityDeadlineRequiresStationEvidence protects that a
// never-observed Entity has no stale deadline until the first accepted station
// report anchors it at exactly three upload intervals.
func TestAvailabilityDeadlineRequiresStationEvidence(t *testing.T) {
	t.Parallel()

	tracker := newAvailabilityTracker(19)
	if _, ok := tracker.deadline(fixtureUploadInterval); ok {
		t.Fatal("stale deadline exists before any station evidence")
	}
	anchor := fixtureReceivedAt
	tracker.noteReport(anchor)
	deadline, ok := tracker.deadline(fixtureUploadInterval)
	if !ok {
		t.Fatal("stale deadline is absent after the first station report")
	}
	want := anchor.Add(3 * fixtureUploadInterval)
	if !deadline.Equal(want) {
		t.Fatalf("stale deadline = %s, want exactly three upload intervals after the first report (%s)", deadline, want)
	}
	tracker.noteReport(anchor.Add(time.Minute))
	laterDeadline, _ := tracker.deadline(fixtureUploadInterval)
	if !laterDeadline.Equal(want) {
		t.Fatalf("a second station report moved the never-observed anchor to %s", laterDeadline)
	}
}

// TestAvailabilityStaleTransitionsAreExactlyThreeIntervals protects per-Entity
// staleness timing and catalog ordering using a fake clock.
func TestAvailabilityStaleTransitionsAreExactlyThreeIntervals(t *testing.T) {
	t.Parallel()

	tracker := newAvailabilityTracker(19)
	anchor := fixtureReceivedAt
	tracker.noteReport(anchor)
	tracker.observe(0, anchor.Add(fixtureUploadInterval))
	if stale := tracker.staleTransitions(
		anchor.Add(3*fixtureUploadInterval-time.Second),
		fixtureUploadInterval,
	); len(
		stale,
	) != 0 {
		t.Fatalf("stale transitions one second early = %v, want none", stale)
	}
	stale := tracker.staleTransitions(anchor.Add(3*fixtureUploadInterval), fixtureUploadInterval)
	if len(stale) != 18 {
		t.Fatalf("stale transitions = %v, want the 18 never-observed Entities", stale)
	}
	for index, entityIndex := range stale {
		if entityIndex != index+1 {
			t.Fatalf("stale transitions = %v, want catalog order without the observed Entity", stale)
		}
	}
	if extra := tracker.staleTransitions(anchor.Add(3*fixtureUploadInterval), fixtureUploadInterval); len(extra) != 0 {
		t.Fatalf("already unavailable Entities transitioned again: %v", extra)
	}
	if early := tracker.staleTransitions(
		anchor.Add(4*fixtureUploadInterval-time.Second), fixtureUploadInterval,
	); len(early) != 0 {
		t.Fatalf("observed Entity went stale one second early: %v", early)
	}
	remaining := tracker.staleTransitions(anchor.Add(4*fixtureUploadInterval), fixtureUploadInterval)
	if len(remaining) != 1 || remaining[0] != 0 {
		t.Fatalf("observed Entity stale transitions = %v, want [0] one interval later", remaining)
	}
	if _, ok := tracker.deadline(fixtureUploadInterval); ok {
		t.Fatal("stale deadline remains after every Entity is unavailable")
	}
}

// TestAvailabilityPlansOnlyStatusTransitions protects the repeated-report
// suppression rule: an Entity Core already believes available is not reported
// again.
func TestAvailabilityPlansOnlyStatusTransitions(t *testing.T) {
	t.Parallel()

	tracker := newAvailabilityTracker(4)
	if planned := tracker.planAvailable([]int{0, 2}); len(planned) != 2 {
		t.Fatalf("first availability plan = %v, want both observed Entities", planned)
	}
	if planned := tracker.planAvailable([]int{0, 2}); len(planned) != 0 {
		t.Fatalf("repeated availability plan = %v, want no transition", planned)
	}
	if planned := tracker.planAvailable([]int{1}); len(planned) != 1 || planned[0] != 1 {
		t.Fatalf("newly observed plan = %v, want [1]", planned)
	}
	tracker.revertAvailable([]int{1})
	if planned := tracker.planAvailable([]int{1}); len(planned) != 1 || planned[0] != 1 {
		t.Fatalf("reverted availability plan = %v, want the Entity resent", planned)
	}
}

// TestAvailabilityUnhealthyTransitionClearsEveryEntity protects Core's
// documented behavior of clearing availability on an unhealthy adapter, so
// recovery must re-send availability for every Entity.
func TestAvailabilityUnhealthyTransitionClearsEveryEntity(t *testing.T) {
	t.Parallel()

	tracker := newAvailabilityTracker(3)
	tracker.planAvailable([]int{0, 1, 2})
	tracker.markUnhealthy()
	if planned := tracker.planAvailable([]int{0, 1, 2}); len(planned) != 3 {
		t.Fatalf("post-recovery availability plan = %v, want all three Entities", planned)
	}
}

// TestAvailabilityReportsPreserveOrderStatusReasonAndBatchBound protects the
// explicit availability batch shape and the SDK's 256-entry bound.
func TestAvailabilityReportsPreserveOrderStatusReasonAndBatchBound(t *testing.T) {
	t.Parallel()

	routes := trackerRoutes(300)
	tracker := newAvailabilityTracker(len(routes.entities))
	indices := make([]int, 0, len(routes.entities))
	for index := range routes.entities {
		indices = append(indices, index)
	}
	batchTime := fixtureReceivedAt
	batches := tracker.availabilityReports(
		routes, indices, adapter.AvailabilityUnavailable, measurementStaleReason, batchTime,
	)
	if len(batches) != 2 {
		t.Fatalf("availability batches = %d, want 2 for 300 Entities", len(batches))
	}
	if len(batches[0]) != 256 || len(batches[1]) != 44 {
		t.Fatalf("availability batch sizes = [%d, %d], want [256, 44]", len(batches[0]), len(batches[1]))
	}
	for batchIndex, batch := range batches {
		for entryIndex, entry := range batch {
			wantIndex := batchIndex*256 + entryIndex
			if entry.EntityID != routes.entities[wantIndex].EntityID {
				t.Fatalf("batch[%d][%d] Entity ID = %q, want %q",
					batchIndex, entryIndex, entry.EntityID, routes.entities[wantIndex].EntityID)
			}
			if entry.Status != adapter.AvailabilityUnavailable {
				t.Fatalf("batch[%d][%d] status = %q", batchIndex, entryIndex, entry.Status)
			}
			if entry.ReasonCode != measurementStaleReason {
				t.Fatalf("batch[%d][%d] reason = %q", batchIndex, entryIndex, entry.ReasonCode)
			}
			if !entry.SourceObservedAt.Equal(batchTime) {
				t.Fatalf("batch[%d][%d] source observed at = %s, want %s",
					batchIndex, entryIndex, entry.SourceObservedAt, batchTime)
			}
		}
	}
	empty := tracker.availabilityReports(routes, nil, adapter.AvailabilityAvailable, "", batchTime)
	if len(empty) != 0 {
		t.Fatalf("availability batches for no Entities = %d, want none", len(empty))
	}
}

// TestAvailabilityObserveDoesNotCommitReportedStatus protects the split
// between local measurement evidence and Core's reported status: observing a
// measurement never pretends Core already knows.
func TestAvailabilityObserveDoesNotCommitReportedStatus(t *testing.T) {
	t.Parallel()

	tracker := newAvailabilityTracker(2)
	tracker.observe(0, fixtureReceivedAt)
	if planned := tracker.planAvailable([]int{0}); len(planned) != 1 {
		t.Fatalf("availability plan after observe = %v, want the Entity reported", planned)
	}
	deadline, ok := tracker.deadline(fixtureUploadInterval)
	if !ok {
		t.Fatal("observed Entity has no stale deadline")
	}
	want := fixtureReceivedAt.Add(3 * fixtureUploadInterval)
	if !deadline.Equal(want) {
		t.Fatalf("observed Entity stale deadline = %s, want %s", deadline, want)
	}
}
