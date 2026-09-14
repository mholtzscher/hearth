package lifecycle

import (
	"context"
	"sync"
)

// AdmissionGroup gates new work and tracks reservations and their child goroutines.
// Close admission before Wait to ensure all admitted work is joined.
//
// Use NewAdmissionGroup; the zero value is not usable. Do not copy after first
// use. All methods are safe for concurrent use.
type AdmissionGroup struct {
	mu            sync.Mutex
	admissionOpen bool
	active        int // Live reservations plus child goroutines.
	idle          chan struct{}
}

// NewAdmissionGroup returns an open, idle group.
func NewAdmissionGroup() *AdmissionGroup {
	idle := make(chan struct{})
	close(idle)
	return &AdmissionGroup{admissionOpen: true, idle: idle}
}

// TryAcquire atomically reserves admission, or returns nil, false if closed.
func (group *AdmissionGroup) TryAcquire() (*Reservation, bool) {
	group.mu.Lock()
	defer group.mu.Unlock()
	if !group.admissionOpen {
		return nil, false
	}
	if group.active == 0 {
		group.idle = make(chan struct{})
	}
	group.active++
	return &Reservation{group: group}, true
}

// AdmissionOpen reports whether TryAcquire still admits work.
func (group *AdmissionGroup) AdmissionOpen() bool {
	group.mu.Lock()
	defer group.mu.Unlock()
	return group.admissionOpen
}

// CloseAdmission permanently closes admission without waiting for admitted work.
// Repeated calls are no-ops.
func (group *AdmissionGroup) CloseAdmission() {
	group.mu.Lock()
	defer group.mu.Unlock()
	group.admissionOpen = false
}

// Wait snapshots the current idle channel and returns nil when it closes, or
// ctx.Err() on cancellation. If both are ready, either result is possible.
// Cancellation does not affect admitted work. Close admission first to prevent
// new work from starting after Wait observes an idle group.
func (group *AdmissionGroup) Wait(ctx context.Context) error {
	group.mu.Lock()
	idle := group.idle
	group.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Reservation holds an admission until Release. Children started with Go keep
// the group busy independently of the reservation.
//
// Use AdmissionGroup.TryAcquire; the zero value is not usable. Do not copy.
// Methods are synchronized, but Go panics if Release takes effect first.
type Reservation struct {
	group    *AdmissionGroup
	released bool // Protected by group.mu.
}

// Go registers a child before starting its goroutine, preventing a gap in tracking.
// It is allowed after admission closes but panics after Release.
// Worker panics are not recovered.
func (reservation *Reservation) Go(work func()) {
	group := reservation.group
	group.mu.Lock()
	if reservation.released {
		group.mu.Unlock()
		panic("lifecycle: Reservation.Go called after Release")
	}
	group.active++
	group.mu.Unlock()
	go func() {
		defer group.childDone()
		work()
	}()
}

// Release ends this reservation without canceling or untracking its children.
// Repeated calls are no-ops.
func (reservation *Reservation) Release() {
	group := reservation.group
	group.mu.Lock()
	defer group.mu.Unlock()
	if reservation.released {
		return
	}
	reservation.released = true
	group.releaseActiveLocked()
}

// releaseActiveLocked releases one reservation or child with group.mu held.
func (group *AdmissionGroup) releaseActiveLocked() {
	group.active--
	if group.active == 0 {
		close(group.idle)
	}
}

// childDone releases a completed child's tracking slot.
func (group *AdmissionGroup) childDone() {
	group.mu.Lock()
	defer group.mu.Unlock()
	group.releaseActiveLocked()
}
