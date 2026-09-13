package ecowitt

import (
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	// staleIntervalCount is the number of consecutive upload intervals without
	// a valid measurement after which an Entity becomes unavailable. A field
	// may be omitted transiently, so an absent or malformed value starts or
	// continues the interval without failing early.
	staleIntervalCount = 3
	// availabilityBatchLimit mirrors the SDK's maximum batch size.
	availabilityBatchLimit = 256
)

// availabilityStatus is what the Adapter believes Core currently holds for one
// Entity. unknown means Core has no availability evidence, which is also the
// state every unhealthy transition produces because Core clears availability
// on an unhealthy adapter.
type availabilityStatus uint8

const (
	availabilityUnknown availabilityStatus = iota
	availabilityAvailable
	availabilityUnavailable
)

// entityFreshness is one Entity's local measurement evidence.
type entityFreshness struct {
	// lastValidAt is the monotonic local receipt time of the Entity's most
	// recent valid measurement. Its zero value means never observed.
	lastValidAt time.Time
	// reported is the availability status the Adapter last committed, or
	// unknown after an unhealthy transition or before the first report.
	reported availabilityStatus
}

// availabilityTracker owns per-Entity measurement freshness, stale deadlines,
// and the report batches derived from them. Availability timers use local
// monotonic receipt time, never dateutc.
type availabilityTracker struct {
	entities      []entityFreshness
	firstReportAt time.Time
	transitions   uint64
}

// newAvailabilityTracker builds the per-Entity state for one route snapshot.
func newAvailabilityTracker(entityCount int) *availabilityTracker {
	return &availabilityTracker{entities: make([]entityFreshness, entityCount)}
}

// noteReport records the first accepted station report. A never-observed
// Entity stays unknown until its first valid measurement or until three
// intervals after this instant, when it becomes unavailable.
func (tracker *availabilityTracker) noteReport(at time.Time) {
	if tracker.firstReportAt.IsZero() {
		tracker.firstReportAt = at
	}
}

// observe records one valid measurement as explicit availability evidence.
// The reported status is left alone: the transition to available is planned
// separately so a rejected availability report can be reverted.
func (tracker *availabilityTracker) observe(index int, at time.Time) {
	tracker.entities[index].lastValidAt = at
}

// planAvailable marks every observed Entity available and returns the Entity
// indices whose Core status must be refreshed. Entities Core already believes
// available are skipped, so a repeated available report with no status
// transition is not sent.
func (tracker *availabilityTracker) planAvailable(indices []int) []int {
	planned := make([]int, 0, len(indices))
	for _, index := range indices {
		if tracker.entities[index].reported == availabilityAvailable {
			continue
		}
		tracker.entities[index].reported = availabilityAvailable
		planned = append(planned, index)
	}
	if len(planned) > 0 {
		tracker.transitions++
	}
	return planned
}

// revertAvailable returns optimistically planned Entities to unknown, so a
// superseded or rejected availability report is re-sent after recovery
// instead of being assumed committed.
func (tracker *availabilityTracker) revertAvailable(indices []int) {
	for _, index := range indices {
		if tracker.entities[index].reported == availabilityAvailable {
			tracker.entities[index].reported = availabilityUnknown
		}
	}
}

// markUnhealthy models Core clearing every Entity's availability on an
// unhealthy adapter transition. Recovery therefore requires a new live report
// and fresh availability before Observations.
func (tracker *availabilityTracker) markUnhealthy() {
	for index := range tracker.entities {
		tracker.entities[index].reported = availabilityUnknown
	}
}

// deadline returns the earliest instant at which some Entity still eligible
// for a stale transition becomes unavailable. An Entity already reported
// unavailable imposes no deadline.
func (tracker *availabilityTracker) deadline(uploadInterval time.Duration) (time.Time, bool) {
	var earliest time.Time
	for index := range tracker.entities {
		due, eligible := tracker.staleDeadlineAt(index, uploadInterval)
		if !eligible {
			continue
		}
		if earliest.IsZero() || due.Before(earliest) {
			earliest = due
		}
	}
	return earliest, !earliest.IsZero()
}

// staleTransitions commits the unavailable status of every Entity whose stale
// interval has elapsed and returns their indices in catalog order.
func (tracker *availabilityTracker) staleTransitions(now time.Time, uploadInterval time.Duration) []int {
	stale := make([]int, 0, len(tracker.entities))
	for index := range tracker.entities {
		due, eligible := tracker.staleDeadlineAt(index, uploadInterval)
		if !eligible || now.Before(due) {
			continue
		}
		tracker.entities[index].reported = availabilityUnavailable
		stale = append(stale, index)
	}
	if len(stale) > 0 {
		tracker.transitions++
	}
	return stale
}

// staleDeadlineAt is one Entity's stale instant: three upload intervals after
// its most recent valid measurement, or after the first accepted station
// report for an Entity never observed.
func (tracker *availabilityTracker) staleDeadlineAt(
	index int,
	uploadInterval time.Duration,
) (time.Time, bool) {
	entity := tracker.entities[index]
	if entity.reported == availabilityUnavailable {
		return time.Time{}, false
	}
	anchor := entity.lastValidAt
	if anchor.IsZero() {
		if tracker.firstReportAt.IsZero() {
			return time.Time{}, false
		}
		anchor = tracker.firstReportAt
	}
	return anchor.Add(staleIntervalCount * uploadInterval), true
}

// availabilityReports builds the ordered availability batch for one set of
// Entity indices, chunked to the SDK's maximum batch size, so a caller can
// never exceed Core's bound.
func (tracker *availabilityTracker) availabilityReports(
	routes routeSnapshot,
	indices []int,
	status adapter.EntityAvailabilityStatus,
	reasonCode string,
	sourceObservedAt time.Time,
) [][]adapter.EntityAvailabilityReport {
	batches := make([][]adapter.EntityAvailabilityReport, 0, 1)
	var batch []adapter.EntityAvailabilityReport
	for _, index := range indices {
		batch = append(batch, adapter.EntityAvailabilityReport{
			EntityID:         routes.entities[index].EntityID,
			Status:           status,
			SourceObservedAt: sourceObservedAt,
			ReasonCode:       reasonCode,
		})
		if len(batch) == availabilityBatchLimit {
			batches = append(batches, batch)
			batch = nil
		}
	}
	if len(batch) > 0 {
		batches = append(batches, batch)
	}
	return batches
}
