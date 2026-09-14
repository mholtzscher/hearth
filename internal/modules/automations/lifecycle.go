package automations

import (
	"context"
	"log/slog"
	"time"
)

// StopAdmission atomically closes automation admission. New manual and
// fact-triggered Runs are refused with [ErrAdmissionUnavailable]; already
// admitted Runs finish their current Command and drain.
func (service *Service) StopAdmission() {
	service.gate.Lock()
	defer service.gate.Unlock()
	service.admissionOpen = false
}

// AdmissionOpen reports whether new Runs may still be admitted. An executor
// fault permanently closes admission, so a latched fault is already reflected
// here. Readiness combines this with device Command admission.
func (service *Service) AdmissionOpen() bool {
	service.gate.Lock()
	defer service.gate.Unlock()
	return service.admissionOpen
}

// WaitRuns joins already-admitted Run workers and any admission still in flight
// without canceling their current Commands. Close admission first so no new
// admission or worker can be registered during the wait, then call it before
// shared dependencies are torn down.
func (service *Service) WaitRuns(ctx context.Context) error {
	service.gate.Lock()
	idle := service.idle
	service.gate.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// InterruptActiveRuns marks every running Run and Step as interrupted after a
// restart or before NATS connects. It never replays or infers success.
func (service *Service) InterruptActiveRuns(ctx context.Context, at time.Time) error {
	if err := service.repository.InterruptActiveRuns(ctx, at, AutomationFailureCoreRestarted); err != nil {
		return err
	}
	service.dependencies.Logger.WarnContext(
		ctx,
		"automation runs interrupted",
		slog.String("event", "automation.run_interrupted"),
		slog.String("reason", AutomationFailureCoreRestarted),
	)
	return nil
}

// registerAdmittedRuns releases the reserved admission slot and registers one
// tracked worker per committed Run in a single critical section. It runs only
// after the admission transaction commits, so no worker is registered for a
// failed transaction, and a simultaneous [Service.StopAdmission] plus
// [Service.WaitRuns] can never observe the service idle while a committed Run is
// about to start. The database's partial unique index remains the final busy
// guard; the counters only mirror what still needs to drain.
func (service *Service) registerAdmittedRuns(runs ...AutomationRun) {
	service.gate.Lock()
	defer service.gate.Unlock()
	service.admitting--
	service.workers += len(runs)
	service.closeIdleLocked()
}

func (service *Service) releaseRunWorker() {
	service.gate.Lock()
	defer service.gate.Unlock()
	service.workers--
	service.closeIdleLocked()
}

// beginAdmission reserves one in-flight admission slot and reports whether the
// gate is still open. Checking the gate and reserving the slot share one
// critical section, so an admission that races [Service.StopAdmission] is
// refused before it commits, and an admission that already reserved a slot is
// joined by [Service.WaitRuns] even though it registers its worker only after
// that transaction commits.
func (service *Service) beginAdmission() bool {
	service.gate.Lock()
	defer service.gate.Unlock()
	if !service.admissionOpen {
		return false
	}
	service.reopenIdleLocked()
	service.admitting++
	return true
}

// abandonAdmission releases one reserved admission slot that committed no Run.
// A failed or refused admission must not keep [Service.WaitRuns] blocked.
func (service *Service) abandonAdmission() {
	service.gate.Lock()
	defer service.gate.Unlock()
	service.admitting--
	service.closeIdleLocked()
}

// closeIdleLocked closes the idle channel once nothing is admitted: no
// in-flight admission and no Run worker. It is called only after a release, so
// the channel it closes is always open.
func (service *Service) closeIdleLocked() {
	if service.admitting == 0 && service.workers == 0 {
		close(service.idle)
	}
}

// reopenIdleLocked replaces the closed idle channel once the service was idle,
// so the next [Service.WaitRuns] observes the newly admitted work. It is a no-op
// while an admission or worker already holds the service.
func (service *Service) reopenIdleLocked() {
	if service.admitting == 0 && service.workers == 0 {
		service.idle = make(chan struct{})
	}
}

// latchExecutorFault closes automation admission permanently for the rest of
// the process after any Step start, Step completion, Run completion, or
// ownership check could not be established truthfully. Admission never reopens,
// so the closed gate is the only latched state.
func (service *Service) latchExecutorFault(ctx context.Context, runID AutomationRunID, position int) {
	service.gate.Lock()
	defer service.gate.Unlock()
	service.admissionOpen = false
	service.dependencies.Logger.ErrorContext(
		ctx,
		"automation executor fault latched until restart",
		slog.String("event", "automation.executor_fault"),
		slog.String("run_id", string(runID)),
		slog.Int("step_position", position),
		slog.String("error_code", AutomationFailureExecutorFault),
	)
}
