package api

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// AutomationDelayExecutionBody exposes reached wait evidence and diagnostic due time.
type AutomationDelayExecutionBody struct {
	StepID      string     `json:"step_id"`
	Position    int        `json:"position"               minimum:"0" maximum:"63"`
	DurationMS  int64      `json:"duration_ms"            minimum:"1" maximum:"86400000"`
	Status      string     `json:"status"                                                enum:"running,completed,interrupted"`
	StartedAt   time.Time  `json:"started_at"`
	DueAt       time.Time  `json:"due_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	FailureCode *string    `json:"failure_code,omitempty"`
}

func delayExecutionBody(delay automations.DelayExecution) AutomationDelayExecutionBody {
	return AutomationDelayExecutionBody{
		StepID: string(delay.StepID), Position: delay.Position, DurationMS: delay.DurationMS,
		Status: string(delay.Status), StartedAt: delay.StartedAt, DueAt: delay.DueAt,
		CompletedAt: delay.CompletedAt, FailureCode: delay.FailureCode,
	}
}
