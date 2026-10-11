package automations

import "time"

// RunOutcome is one established terminal outcome.
//
//sumtype:decl
type RunOutcome interface{ isRunOutcome() }

// SucceededRun records successful completion of the selected execution path.
type SucceededRun struct{}

// FailedRun records a Command or branch failure.
type FailedRun struct{ FailureCode string }

// InterruptedRun records stopped execution without an established outcome.
type InterruptedRun struct{ FailureCode string }

func (SucceededRun) isRunOutcome()   {}
func (FailedRun) isRunOutcome()      {}
func (InterruptedRun) isRunOutcome() {}

// RunState is either active execution or a timestamped terminal outcome.
//
//sumtype:decl
type RunState interface{ isRunState() }

// RunningRun records active execution.
type RunningRun struct{}

// CompletedRun records when a terminal outcome was committed.
type CompletedRun struct {
	CompletedAt time.Time
	Outcome     RunOutcome
}

func (RunningRun) isRunState()   {}
func (CompletedRun) isRunState() {}
