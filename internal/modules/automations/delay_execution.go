package automations

import (
	"context"
	"time"
)

// executeDelay commits reached evidence before waiting and terminal evidence
// before advancing. Diagnostic wall time never controls the native interval.
func (service *Service) executeDelay(ctx context.Context, run Run, stepID StepID, delay DelayStep, position int) bool {
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	monotonicStart := time.Now()
	startedAt := service.dependencies.Now().UTC()
	duration := time.Duration(delay.DurationMS) * time.Millisecond
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.RecordDelayStart(writeContext, DelayStart{
		RunID: run.ID, StepID: stepID, Position: position, StartedAt: startedAt,
	})
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
	err = service.completeDelay(ctx, DelayCompletion{
		RunID: run.ID, StepID: stepID, Status: DelayCompleted,
		CompletedAt: service.dependencies.Now().UTC(),
	})
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

func (service *Service) completeDelay(ctx context.Context, completion DelayCompletion) error {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	return service.repository.CompleteDelay(writeContext, completion)
}

// interruptDelay atomically interrupts the active wait and its parent, once.
// Failed or ambiguous writes leave recovery to startup rather than retrying.
func (service *Service) interruptDelay(ctx context.Context, runID RunID, stepID StepID, code string) {
	err := service.completeDelay(ctx, DelayCompletion{
		RunID: runID, StepID: stepID, Status: DelayInterrupted,
		CompletedAt: service.dependencies.Now().UTC(), FailureCode: &code,
	})
	if code == FailureExecutorFault || err != nil {
		service.latchRunExecutorFault(ctx, runID, stepID)
	}
}
