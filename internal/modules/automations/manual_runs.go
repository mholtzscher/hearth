package automations

import "context"

// ManualRunInput carries explicit operator intent for one manual admission; no
// Command identities are accepted.
type ManualRunInput struct {
	AutomationID     AutomationID
	BypassConditions bool
}

// StartManualRun admits one Run from the current definition snapshot, even when
// the Automation is disabled. Conditions are evaluated unless
// [ManualRunInput.BypassConditions] requests an explicit bypass; a committed
// Condition Skip returns [ErrAutomationConditionsBlocked] after the transaction.
func (service *Service) StartManualRun(ctx context.Context, input ManualRunInput) (Run, error) {
	reservation, admitted := service.admission.TryAcquire()
	if !admitted {
		return Run{}, ErrAdmissionUnavailable
	}
	defer reservation.Release()
	// The device gate is checked separately: automation admission closes first on
	// shutdown, and the cross-module gates never claim an atomic check-and-admit.
	if service.devices == nil || !service.devices.CommandAdmissionOpen() {
		return Run{}, ErrAdmissionUnavailable
	}
	result, err := service.admitManualRun(ctx, input)
	if err != nil {
		// Release before diagnostics so a blocked log sink cannot hold Drain.
		reservation.Release()
		service.logConditionStateCorrupt(ctx, err)
		return Run{}, err
	}
	var run Run
	switch result := result.(type) {
	case Skip:
		skip := result
		reservation.Release()
		service.logSkipped(ctx, AdmissionSkip{
			SkipID:       skip.ID,
			AutomationID: skip.AutomationID,
			Revision:     skip.Revision,
			Cause:        skip.Cause,
			Reason:       skip.Reason,
		})
		return Run{}, &ConditionsBlockedError{
			AutomationID: skip.AutomationID,
			SkipID:       skip.ID,
			Reason:       skip.Reason,
		}
	case Run:
		run = result
	default:
		return Run{}, invalid("invalid committed manual admission result %T", result)
	}
	// Caller cancellation must not cancel an admitted Run.
	workerContext := context.WithoutCancel(ctx)
	reservation.Go(func() { service.executeRun(workerContext, run) })
	reservation.Release()
	service.logRunStarted(ctx, run)
	return run, nil
}

// admitManualRun reads the current definition before opening the admission transaction.
func (service *Service) admitManualRun(
	ctx context.Context,
	input ManualRunInput,
) (ManualAdmissionResult, error) {
	admissionContext, cancel := context.WithTimeout(ctx, AdmissionTimeout)
	defer cancel()
	record, err := service.repository.GetAutomation(admissionContext, input.AutomationID)
	if err != nil {
		return nil, err
	}
	snapshot := emptyEntityStateSnapshot()
	if !input.BypassConditions && record.Definition.Conditions != nil {
		required, requiredErr := RequiredConditionEntityIDs(*record.Definition.Conditions)
		if requiredErr != nil {
			return nil, requiredErr
		}
		snapshot, err = service.readConditionStateSnapshot(admissionContext, required)
		if err != nil {
			return nil, err
		}
	}
	return service.repository.AdmitManualRun(admissionContext, input, snapshot, service.dependencies.Now())
}
