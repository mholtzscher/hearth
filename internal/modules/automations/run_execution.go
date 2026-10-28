package automations

import (
	"context"
	"log/slog"
	"time"
)

// automationPersistenceTimeout bounds one automation-owned SQLite read or write.
// It never wraps the external Command call, which devices owns and deadlines.
const automationPersistenceTimeout = 5 * time.Second

// Stable Step and Run failure codes. They are fixed tokens, never upstream text.
const (
	// FailureCoreStopping marks a Step or Run stopped because Command
	// admission closed during drain.
	FailureCoreStopping = "core_stopping"
	// FailureCoreRestarted marks a Step or Run interrupted by restart.
	FailureCoreRestarted = "core_restarted"
	// FailureExecutorFault marks a Step or Run the executor could not
	// advance truthfully, such as an unverifiable or nonterminal Command.
	FailureExecutorFault = "executor_fault"
	// FailureInvalidCommand marks a Step rejected before Command creation.
	FailureInvalidCommand = "invalid_command"
	// FailureEntityNotFound marks a Step whose Entity vanished before
	// Command creation.
	FailureEntityNotFound = "entity_not_found"
	// FailureInternalError is the fallback for one confirmed Command
	// failure with no more specific durable code.
	FailureInternalError = "internal_error"
)

// interruptRunExecutorFault reports Run/control-flow faults without a command position.
func (service *Service) interruptRunExecutorFault(ctx context.Context, runID RunID, stepID StepID) {
	service.interruptFaultedRun(ctx, runID, slog.String("step_id", string(stepID)))
}

// interruptFaultedRun makes one bounded, atomic cleanup attempt. Failure leaves
// this Automation busy until startup recovery, not the module unavailable.
// Callers stop advancing the Run; cleanup never replays external work.
func (service *Service) interruptFaultedRun(ctx context.Context, runID RunID, location slog.Attr) {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err := service.repository.InterruptRun(writeContext, runID, service.dependencies.Now().UTC())
	cancel()
	service.dependencies.Logger.ErrorContext(ctx, "automation run executor fault",
		slog.String("event", "automation.executor_fault"),
		slog.String("run_id", string(runID)), location,
		slog.String("error_code", FailureExecutorFault))
	if err != nil {
		// Database errors can contain payloads; log only stable diagnostic tokens.
		service.dependencies.Logger.ErrorContext(ctx, "automation run interruption failed; startup recovery required",
			slog.String("event", "automation.run_interruption_failed"),
			slog.String("run_id", string(runID)),
			slog.String("error_code", FailureExecutorFault))
	}
}

// executeRun executes one immutable Run snapshot sequentially. The next Step
// starts only after the prior Command reaches a successful terminal outcome, and
// the first failure or interruption stops the Run without retry.
func (service *Service) executeRun(ctx context.Context, run Run) {
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, run.ID, "", service.executionStopReason())
		return
	}
	values, err := newRunValues(run)
	if err != nil {
		service.failInitialization(ctx, run, "trigger", err)
		return
	}
	if !service.initializeValues(ctx, run, values) {
		return
	}
	if service.executeSequence(ctx, run, run.Snapshot.Steps, values) {
		if !service.executionOpen() {
			service.interruptBranchRun(ctx, run.ID, "", service.executionStopReason())
			return
		}
		service.completeRun(ctx, run.ID, SucceededRun{})
	}
}

func (service *Service) completeRun(
	ctx context.Context,
	runID RunID,
	outcome RunOutcome,
) {
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	if err := service.repository.CompleteRun(writeContext, RunCompletion{
		RunID:   runID,
		Outcome: outcome,
	}); err != nil {
		service.interruptRunExecutorFault(ctx, runID, "")
		return
	}
	service.logRunCompleted(ctx, runID, outcome)
}

func (service *Service) logRunCompleted(ctx context.Context, runID RunID, outcome RunOutcome) {
	service.dependencies.Logger.InfoContext(
		ctx,
		"automation run completed",
		slog.String("event", "automation.run_completed"),
		slog.String("run_id", string(runID)),
		slog.String("status", string(RunOutcomeStatus(outcome))),
	)
}
