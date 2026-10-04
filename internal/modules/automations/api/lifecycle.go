package api

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationRunBody exposes an immutable snapshot and exactly one lifecycle DTO.
type AutomationRunBody struct{ Variant AutomationRunVariant }

//sumtype:decl
type AutomationRunVariant interface{ isAutomationRunBody() }

type RunningRunBody struct {
	AutomationRunFields

	Status string `json:"status" enum:"running"`
}

type SucceededRunBody struct {
	AutomationRunFields

	Status      string    `json:"status"       enum:"succeeded"`
	CompletedAt time.Time `json:"completed_at"`
}

type UnsuccessfulRunBody struct {
	AutomationRunFields

	Status      string    `json:"status"       enum:"failed,interrupted"`
	CompletedAt time.Time `json:"completed_at"`
	FailureCode string    `json:"failure_code"`
}

func (RunningRunBody) isAutomationRunBody()      {}
func (SucceededRunBody) isAutomationRunBody()    {}
func (UnsuccessfulRunBody) isAutomationRunBody() {}

func (body AutomationRunBody) MarshalJSON() ([]byte, error) {
	switch body.Variant.(type) {
	case RunningRunBody, SucceededRunBody, UnsuccessfulRunBody:
		return json.Marshal(body.Variant)
	default:
		return nil, fmt.Errorf("invalid Run DTO %T", body.Variant)
	}
}

func runLifecycleBody(fields AutomationRunFields, state automations.RunState) AutomationRunBody {
	switch state := state.(type) {
	case automations.RunningRun:
		return AutomationRunBody{Variant: RunningRunBody{AutomationRunFields: fields, Status: "running"}}
	case automations.CompletedRun:
		switch outcome := state.Outcome.(type) {
		case automations.SucceededRun:
			return AutomationRunBody{Variant: SucceededRunBody{
				AutomationRunFields: fields, Status: "succeeded", CompletedAt: state.CompletedAt,
			}}
		case automations.FailedRun:
			return AutomationRunBody{Variant: UnsuccessfulRunBody{
				AutomationRunFields: fields,
				Status:              "failed",
				CompletedAt:         state.CompletedAt,
				FailureCode:         outcome.FailureCode,
			}}
		case automations.InterruptedRun:
			return AutomationRunBody{Variant: UnsuccessfulRunBody{
				AutomationRunFields: fields,
				Status:              "interrupted",
				CompletedAt:         state.CompletedAt,
				FailureCode:         outcome.FailureCode,
			}}
		default:
			panic("invalid retained Run outcome")
		}
	default:
		panic("invalid retained Run state")
	}
}

// AutomationStepAttemptBody exposes only ownership-verified Command evidence.
type AutomationStepAttemptBody struct{ Variant AutomationStepAttemptVariant }

//sumtype:decl
type AutomationStepAttemptVariant interface{ isAutomationStepAttemptBody() }

type AutomationStepAttemptFields struct {
	Position int    `json:"position"`
	StepID   string `json:"step_id"`
}

type NotAttemptedStepBody struct {
	AutomationStepAttemptFields

	Status string `json:"status" enum:"not_attempted"`
}

type RunningStepBody struct {
	AutomationStepAttemptFields

	Status    string    `json:"status"     enum:"running"`
	StartedAt time.Time `json:"started_at"`
}

type VerifiedStepBody struct {
	AutomationStepAttemptFields

	Status            string    `json:"status"              enum:"satisfied,dispatched"`
	StartedAt         time.Time `json:"started_at"`
	CompletedAt       time.Time `json:"completed_at"`
	VerifiedCommandID string    `json:"verified_command_id"`
}

type UnsuccessfulStepBody struct {
	AutomationStepAttemptFields

	Status            string    `json:"status"                        enum:"failed,interrupted"`
	StartedAt         time.Time `json:"started_at"`
	CompletedAt       time.Time `json:"completed_at"`
	FailureCode       string    `json:"failure_code"`
	VerifiedCommandID *string   `json:"verified_command_id,omitempty"`
}

func (NotAttemptedStepBody) isAutomationStepAttemptBody() {}
func (RunningStepBody) isAutomationStepAttemptBody()      {}
func (VerifiedStepBody) isAutomationStepAttemptBody()     {}
func (UnsuccessfulStepBody) isAutomationStepAttemptBody() {}

func (body AutomationStepAttemptBody) MarshalJSON() ([]byte, error) {
	switch body.Variant.(type) {
	case NotAttemptedStepBody, RunningStepBody, VerifiedStepBody, UnsuccessfulStepBody:
		return json.Marshal(body.Variant)
	default:
		return nil, fmt.Errorf("invalid Step attempt DTO %T", body.Variant)
	}
}

func automationStepAttemptBody(step automations.StepAttempt) AutomationStepAttemptBody {
	fields := AutomationStepAttemptFields{Position: step.Position, StepID: string(step.StepID)}
	switch state := step.State.(type) {
	case automations.NotAttemptedStep:
		return AutomationStepAttemptBody{
			Variant: NotAttemptedStepBody{AutomationStepAttemptFields: fields, Status: "not_attempted"},
		}
	case automations.RunningStep:
		return AutomationStepAttemptBody{Variant: RunningStepBody{
			AutomationStepAttemptFields: fields, Status: "running", StartedAt: state.StartedAt,
		}}
	case automations.CompletedStep:
		return completedStepBody(fields, state)
	default:
		panic("invalid retained Step state")
	}
}

func completedStepBody(fields AutomationStepAttemptFields, state automations.CompletedStep) AutomationStepAttemptBody {
	switch outcome := state.Outcome.(type) {
	case automations.SatisfiedStep:
		return AutomationStepAttemptBody{Variant: VerifiedStepBody{
			AutomationStepAttemptFields: fields, Status: "satisfied", StartedAt: state.StartedAt,
			CompletedAt: state.CompletedAt, VerifiedCommandID: string(outcome.VerifiedCommandID),
		}}
	case automations.DispatchedStep:
		return AutomationStepAttemptBody{Variant: VerifiedStepBody{
			AutomationStepAttemptFields: fields, Status: "dispatched", StartedAt: state.StartedAt,
			CompletedAt: state.CompletedAt, VerifiedCommandID: string(outcome.VerifiedCommandID),
		}}
	case automations.FailedStep:
		return AutomationStepAttemptBody{Variant: UnsuccessfulStepBody{
			AutomationStepAttemptFields: fields, Status: "failed", StartedAt: state.StartedAt,
			CompletedAt: state.CompletedAt, FailureCode: outcome.FailureCode,
			VerifiedCommandID: publicVerifiedCommandID(outcome.VerifiedCommandID),
		}}
	case automations.InterruptedStep:
		return AutomationStepAttemptBody{Variant: UnsuccessfulStepBody{
			AutomationStepAttemptFields: fields, Status: "interrupted", StartedAt: state.StartedAt,
			CompletedAt: state.CompletedAt, FailureCode: outcome.FailureCode,
			VerifiedCommandID: publicVerifiedCommandID(outcome.VerifiedCommandID),
		}}
	default:
		panic("invalid retained Step outcome")
	}
}

func publicVerifiedCommandID(id *devices.CommandID) *string {
	if id == nil {
		return nil
	}
	value := string(*id)
	return &value
}
