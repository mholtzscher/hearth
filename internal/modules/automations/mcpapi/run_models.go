package mcpapi

import (
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

type RunReceipt struct {
	AutomationID string     `json:"automation_id"          minLength:"1"`
	RunID        string     `json:"run_id"                 minLength:"1"`
	Status       string     `json:"status"                               enum:"running,succeeded,failed,interrupted"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	FailureCode  string     `json:"failure_code,omitempty" minLength:"1"`
}

// TransformSchema preserves the completion evidence required by each Run status.
func (RunReceipt) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	schema.OneOf = runLifecycleSchema()
	return schema
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
