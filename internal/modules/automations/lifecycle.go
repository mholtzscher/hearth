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

// AdmissionOpen reports whether new Runs may be admitted, including executor
// fault state. Readiness also checks device Command admission.
func (service *Service) AdmissionOpen() bool {
	service.gate.Lock()
	defer service.gate.Unlock()
	return service.admissionOpen
}

// WaitRuns joins in-flight admissions and Run workers without canceling Commands.
// Close admission first and keep shared dependencies alive until it returns.
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

// InterruptActiveRuns marks running Runs and Steps interrupted on restart.
// Call before opening transports; it never replays Commands or infers success.
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

// registerAdmittedRuns atomically replaces an admission reservation with workers
// after commit, so WaitRuns cannot observe an idle gap before workers start.
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

// beginAdmission atomically checks the gate and reserves a slot. WaitRuns tracks
// accepted admissions even before their transactions commit.
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

// closeIdleLocked closes idle after the last admission or worker releases.
// Call only after a release, with gate held and idle still open.
func (service *Service) closeIdleLocked() {
	if service.admitting == 0 && service.workers == 0 {
		close(service.idle)
	}
}

// reopenIdleLocked opens a new idle channel when admitting work to an idle service.
// Call with gate held, before incrementing the admission count.
func (service *Service) reopenIdleLocked() {
	if service.admitting == 0 && service.workers == 0 {
		service.idle = make(chan struct{})
	}
}

// latchExecutorFault closes admission until restart when execution cannot verify
// or persist progress. The closed gate is the only latched state.
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
