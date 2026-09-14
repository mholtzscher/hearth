package devices

import "context"

// StopCommandAdmission atomically closes Command admission. New Commands are
// rejected with ErrCommandUnavailable. It is idempotent and never blocks.
func (service *Service) StopCommandAdmission() {
	service.commandAdmission.CloseAdmission()
}

// CommandAdmissionOpen reports whether Commands are still admitted.
// Application readiness combines this with runtime health through a narrow
// admission seam; shutdown closes admission before joining workers.
func (service *Service) CommandAdmissionOpen() bool {
	return service.commandAdmission.AdmissionOpen()
}

// WaitCommands joins already-admitted Command lifecycles without canceling
// them. Close admission first so no worker can be admitted after this wait.
// Detached workers outlive caller cancellation and must drain before shared
// observation, health, and persistence dependencies are torn down.
func (service *Service) WaitCommands(ctx context.Context) error {
	return service.commandAdmission.Wait(ctx)
}
