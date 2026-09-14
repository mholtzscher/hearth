package lifecycle

import (
	"context"
	"sync"
)

// AdmissionGroup gates admission while open and tracks every admitted
// Reservation and every child goroutine started with Reservation.Go, so a
// caller can join admitted work. NewAdmissionGroup starts open and idle.
//
// CloseAdmission permanently refuses later admissions and never waits for work
// already admitted. Close admission before Wait for a reliable final join;
// otherwise a new admission can race with Wait returning from an idle group.
//
// Construct an AdmissionGroup with NewAdmissionGroup; its zero value is not
// usable. It must not be copied after first use. All methods are safe for
// concurrent use.
type AdmissionGroup struct {
	mu            sync.Mutex
	admissionOpen bool
	active        int // Live reservations plus child goroutines.
	idle          chan struct{}
}

// NewAdmissionGroup returns an open, idle group that admits work until
// CloseAdmission runs.
func NewAdmissionGroup() *AdmissionGroup {
	idle := make(chan struct{})
	close(idle)
	return &AdmissionGroup{admissionOpen: true, idle: idle}
}

// TryAcquire atomically admits the caller and returns an active Reservation. It
// reports false and returns no Reservation once CloseAdmission has run.
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

// CloseAdmission permanently closes admission and returns without waiting for
// admitted work. Repeated and concurrent calls are safe.
func (group *AdmissionGroup) CloseAdmission() {
	group.mu.Lock()
	defer group.mu.Unlock()
	group.admissionOpen = false
}

// Wait joins the group's idle transition without canceling or otherwise
// changing admitted work. It returns nil when the idle channel it snapshots
// closes, or the context error if canceled. If both are ready, either result is
// possible. Close admission before Wait for a reliable final join.
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

// Reservation is one active admission granted by AdmissionGroup.TryAcquire. It
// tracks the child goroutines started with Go, so the group stays busy until
// those children finish. Release ends only the parent admission, never its
// children.
//
// Obtain a Reservation through TryAcquire; its zero value is not usable. It
// must not be copied. Go and Release are safe for concurrent use.
type Reservation struct {
	group    *AdmissionGroup
	released bool // Protected by group.mu.
}

// Go registers work as a tracked child before starting its goroutine, so Wait
// never observes an idle gap between registration and execution. A live
// Reservation may still start children after the group closes admission. Go
// panics when the Reservation is already released. Panics in work are not
// recovered, so a failing child crashes the process instead of disappearing.
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

// Release ends the parent admission and leaves children started with Go running
// until they finish on their own. Repeated calls are no-ops.
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

// childDone records one tracked child goroutine completion.
func (group *AdmissionGroup) childDone() {
	group.mu.Lock()
	defer group.mu.Unlock()
	group.releaseActiveLocked()
}
