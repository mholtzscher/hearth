package mcpapi

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type RunReceipt struct {
	AutomationID string     `json:"automation_id"`
	RunID        string     `json:"run_id"`
	Status       string     `json:"status"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	FailureCode  string     `json:"failure_code,omitempty"`
}

// runReceipt reports the state actually returned by admission, not invented completion.
func runReceipt(run automations.Run) RunReceipt {
	result := RunReceipt{AutomationID: string(run.AutomationID), RunID: string(run.ID), Status: "running"}
	switch state := run.State.(type) {
	case automations.RunningRun:
	case automations.CompletedRun:
		result.CompletedAt = &state.CompletedAt
		switch outcome := state.Outcome.(type) {
		case automations.SucceededRun:
			result.Status = "succeeded"
		case automations.FailedRun:
			result.Status, result.FailureCode = "failed", outcome.FailureCode
		case automations.InterruptedRun:
			result.Status, result.FailureCode = "interrupted", outcome.FailureCode
		default:
			panic("invalid retained Run outcome")
		}
	default:
		panic("invalid retained Run state")
	}
	return result
}
