package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func registerStartAutomationRun(api huma.API, handler *handler) {
	start := operation(
		"start-automation-run",
		http.MethodPost,
		"/automations/{automation_id}/runs",
		"Start a manual Automation Run",
	)
	start.DefaultStatus = http.StatusAccepted
	start.RequestBody = manualRunRequestBody()
	start.MaxBodyBytes = manualRunMaximumBodyBytes
	// 409 stays out of Errors so its published schema keeps the optional
	// committed-Skip history reference instead of the generic error model.
	start.Responses["409"] = conditionBlockedProblemResponse(
		"A Run is already active, or committed Conditions blocked manual admission",
	)
	start.Errors = []int{
		http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError,
	}
	huma.Register(api, start, handler.startAutomationRun)
}

// StartAutomationRunInput never accepts caller-supplied Command identities; an
// omitted body applies Conditions.
type StartAutomationRunInput struct {
	AutomationID string                  `path:"automation_id" doc:"Canonical Hearth Automation ID"`
	Body         *StartAutomationRunBody `                     doc:"Optional Condition bypass request"`
}

// AutomationRunOutput uses 202 for admission with a history Location.
type AutomationRunOutput struct {
	Location string `header:"Location"`
	Body     AutomationRunBody
}

func (handler *handler) startAutomationRun(
	ctx context.Context,
	input *StartAutomationRunInput,
) (*AutomationRunOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	run, err := handler.automations.StartManualRun(ctx, automations.ManualRunInput{
		AutomationID:     id,
		BypassConditions: input.Body != nil && input.Body.BypassConditions,
	})
	if err != nil {
		return nil, mapDomainError(err)
	}
	return &AutomationRunOutput{
		Location: "/v1/automations/" + string(run.AutomationID) + "/history/" + string(run.ID),
		Body:     automationRunBody(run),
	}, nil
}

// manualRunRequestBody documents the optional, non-nullable manual bypass body
// inline so the accepted member and closed additionalProperties contract stay visible.
func manualRunRequestBody() *huma.RequestBody {
	return &huma.RequestBody{Required: false, Content: map[string]*huma.MediaType{
		jsonContentType: {Schema: &huma.Schema{
			Type: huma.TypeObject,
			Properties: map[string]*huma.Schema{
				"bypass_conditions": {Type: huma.TypeBoolean},
			},
			AdditionalProperties: false,
		}},
	}}
}

// StartAutomationRunBody carries only the optional Condition bypass request for
// a manual Run; an omitted body applies Conditions like an explicit false.
type StartAutomationRunBody struct {
	BypassConditions bool `json:"bypass_conditions,omitempty"`
}

// UnmarshalJSON enforces the strict optional manual bypass body, closing the
// shapes the generated object schema accepts but the product contract forbids.
func (body *StartAutomationRunBody) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var members map[string]json.RawMessage
	if err := decoder.Decode(&members); err != nil {
		return errors.New("manual run body must be one JSON object")
	}
	if members == nil {
		return errors.New("manual run body must be a JSON object, not null")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("manual run body must contain exactly one JSON object")
	}
	for member := range members {
		if member != "bypass_conditions" {
			return errors.New("manual run body has an unknown member")
		}
	}
	bypass := false
	if operand, present := members["bypass_conditions"]; present {
		switch strings.TrimSpace(string(operand)) {
		case "true":
			bypass = true
		case "false":
			bypass = false
		default:
			return errors.New("bypass_conditions must be a JSON boolean")
		}
	}
	// Assign only after every member is validated, so a rejected body never
	// leaves a partially-applied bypass behind.
	*body = StartAutomationRunBody{BypassConditions: bypass}
	return nil
}

// manualRunMaximumBodyBytes bounds the optional manual bypass body to 1 KiB.
const manualRunMaximumBodyBytes = 1024
