package automations

import (
	"context"
	"errors"
	"log/slog"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func (service *Service) executeInvoke(ctx context.Context, parent Run, id StepID, step InvokeStep) bool {
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, parent.ID, id, service.executionStopReason())
		return false
	}
	reservation, ok := service.admission.TryAcquire()
	if !ok {
		service.interruptBranchRun(ctx, parent.ID, id, service.executionStopReason())
		return false
	}
	admission, cancel := context.WithTimeout(ctx, AdmissionTimeout)
	snapshot := emptyEntityStateSnapshot()
	record, err := service.repository.GetAutomation(admission, step.AutomationID)
	if err == nil && record.Definition.Conditions != nil {
		var required []devices.EntityID
		required, err = RequiredConditionEntityIDs(*record.Definition.Conditions)
		if err == nil {
			snapshot, err = service.readConditionStateSnapshot(admission, required)
		}
	} else if errors.Is(err, ErrAutomationNotFound) {
		err = nil
	}
	var child *Run
	input := InvokeStepInput{ParentRunID: parent.ID, StepID: id}
	if err == nil {
		child, err = service.repository.AdmitInvokedRun(admission, input, snapshot, service.dependencies.Now().UTC())
	}
	cancel()
	reservation.Release()
	if err != nil {
		service.interruptRunExecutorFault(ctx, parent.ID, id)
		return false
	}
	if child == nil {
		service.dependencies.Logger.InfoContext(ctx, "automation invocation rejected",
			slog.String("event", "automation.invoke_rejected"),
			slog.String("run_id", string(parent.ID)), slog.String("step_id", string(id)))
		return false
	}
	service.logRunStarted(ctx, *child)
	service.executeRun(ctx, *child)
	if !service.executionOpen() || ctx.Err() != nil {
		write, cancelWrite := context.WithTimeout(ctx, automationPersistenceTimeout)
		err = service.repository.CompleteStep(write, StepExecutionCompletion{RunID: parent.ID, StepID: id,
			Evidence: InvokeStopped{At: service.dependencies.Now().UTC(), ReasonCode: FailureCoreStopping}})
		cancelWrite()
		if err != nil {
			service.interruptRunExecutorFault(ctx, parent.ID, id)
		}
		return false
	}
	write, cancelWrite := context.WithTimeout(ctx, automationPersistenceTimeout)
	details, err := service.repository.CompleteInvokeStep(write, input, service.dependencies.Now().UTC())
	cancelWrite()
	if err != nil {
		service.interruptRunExecutorFault(ctx, parent.ID, id)
		return false
	}
	_, succeeded := details.State.(InvokeSucceeded)
	return succeeded
}
