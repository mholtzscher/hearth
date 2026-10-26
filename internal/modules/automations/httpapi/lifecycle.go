package httpapi

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
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
