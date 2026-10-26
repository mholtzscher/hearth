package automations

import (
	"context"
	"time"
)

// executeDelay commits reached evidence before waiting and terminal evidence
// before advancing. Diagnostic wall time never controls the native interval.
func (service *Service) executeDelay(ctx context.Context, run Run, stepID StepID, delay DelayStep) bool {
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	monotonicStart := time.Now()
	startedAt := service.dependencies.Now().UTC()
	duration := time.Duration(delay.DurationMS) * time.Millisecond
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.RecordStep(
		writeContext,
		StepRecord{RunID: run.ID, StepID: stepID, Evidence: DelayStarted{StartedAt: startedAt}},
	)
	cancel()
	if err != nil {
		service.interruptBranchRun(ctx, run.ID, stepID, FailureExecutorFault)
		return false
	}
	if service.executionStop.Err() == nil {
		if remaining := duration - time.Since(monotonicStart); remaining > 0 {
			timer := time.NewTimer(remaining)
			select {
			case <-timer.C:
			case <-service.executionStop.Done():
			}
			timer.Stop()
		}
	}
	// Check again even when timer and stop were ready together.
	if service.executionStop.Err() != nil {
		service.interruptDelay(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	err = service.completeDelay(ctx, run.ID, stepID, service.dependencies.Now().UTC(), nil)
	if err != nil {
		service.interruptDelay(ctx, run.ID, stepID, FailureExecutorFault)
		return false
	}
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	return true
}

func (service *Service) completeDelay(
	ctx context.Context,
	runID RunID,
	stepID StepID,
	at time.Time,
	failureCode *string,
) error {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	return service.repository.CompleteStep(
		writeContext,
		StepExecutionCompletion{
			RunID:    runID,
			StepID:   stepID,
			Evidence: DelayFinished{CompletedAt: at, FailureCode: failureCode},
		},
	)
}

// interruptDelay atomically interrupts the active wait and its parent, once.
// An operation fault uses the central single scoped cleanup attempt instead of
// retrying delay completion. Terminal evidence always remains unchanged.
func (service *Service) interruptDelay(ctx context.Context, runID RunID, stepID StepID, code string) {
	if code == FailureExecutorFault {
		service.interruptRunExecutorFault(ctx, runID, stepID)
		return
	}
	err := service.completeDelay(ctx, runID, stepID, service.dependencies.Now().UTC(), &code)
	if err != nil {
		service.interruptRunExecutorFault(ctx, runID, stepID)
	}
}
