package devices

import "context"

// StopCommandAdmission atomically closes direct Command admission. Only explicit
// automation Step calls still execute and drain; new direct Commands are
// rejected with ErrCommandUnavailable.
func (service *Service) StopCommandAdmission() {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	service.commandAdmissionOpen = false
}

// CommandAdmissionOpen reports whether direct Commands are still admitted.
// Application readiness combines this with automation admission and runtime
// health through a narrow admission seam; shutdown closes both admissions
// before joining workers.
func (service *Service) CommandAdmissionOpen() bool {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	return service.commandAdmissionOpen
}

// WaitCommands joins already-admitted Command workers without canceling them.
// Close admission first so no worker can be registered after this wait.
// Detached workers outlive caller cancellation and must drain before shared
// observation, health, and persistence dependencies are torn down.
func (service *Service) WaitCommands(ctx context.Context) error {
	service.lifecycleMu.Lock()
	idle := service.commandIdle
	service.lifecycleMu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// admitCommandWorker registers one direct Command worker under the lifecycle
// gate and reports whether admission is still open. Tracked workers must be
// released exactly once: the synchronous startAdmittedCommand defer owns the
// registration until it transfers ownership to the detached lifecycle goroutine.
func (service *Service) admitCommandWorker() bool {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	if !service.commandAdmissionOpen {
		return false
	}
	service.trackCommandWorkerLocked()
	return true
}

// admitAutomationStepWorker registers one already-admitted automation Step
// worker. Only the automation executor calls this, after the automation gate
// committed the Step intent; HTTP callers use ExecuteCommand and never reach
// it. The synchronous startAdmittedCommand defer owns the registration until
// it transfers ownership to the detached lifecycle goroutine.
func (service *Service) admitAutomationStepWorker() {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	service.trackCommandWorkerLocked()
}

func (service *Service) trackCommandWorkerLocked() {
	if service.commandWorkers == 0 {
		service.commandIdle = make(chan struct{})
	}
	service.commandWorkers++
}

func (service *Service) releaseCommandWorker() {
	service.lifecycleMu.Lock()
	defer service.lifecycleMu.Unlock()
	service.commandWorkers--
	if service.commandWorkers == 0 {
		close(service.commandIdle)
	}
}
