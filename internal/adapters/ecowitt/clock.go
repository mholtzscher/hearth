package ecowitt

import "time"

// clock is the Adapter's private time seam. Production code reads real time
// and schedules real timers; runtime tests inject a manually advanced clock so
// report and measurement deadlines are deterministic instead of wall-clock
// dependent.
type clock interface {
	// Now returns the current local time. The value carries a monotonic
	// reading in production, so deadline arithmetic never follows the station's
	// dateutc or a wall-clock step.
	Now() time.Time
	// AfterFunc schedules one deadline callback and returns a stoppable timer.
	AfterFunc(time.Duration, func()) timer
}

// timer stops one scheduled deadline.
type timer interface {
	Stop()
}

// systemClock is the production clock.
type systemClock struct{}

// Now implements clock.
func (systemClock) Now() time.Time { return time.Now() }

// AfterFunc implements clock.
func (systemClock) AfterFunc(after time.Duration, callback func()) timer {
	return systemTimer{timer: time.AfterFunc(after, callback)}
}

// systemTimer adapts the standard library timer to the private timer seam.
type systemTimer struct {
	timer *time.Timer
}

// Stop implements timer.
func (scheduled systemTimer) Stop() {
	if scheduled.timer != nil {
		scheduled.timer.Stop()
	}
}
