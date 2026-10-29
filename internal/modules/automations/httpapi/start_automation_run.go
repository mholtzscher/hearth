package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

//nolint:goconst // Operation metadata and body fields stay literal at registration.
func registerStartAutomationRun(api huma.API, handler *handler) {
	responses := problemResponses(api,
		http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity,
		http.StatusInternalServerError, http.StatusServiceUnavailable)
	responses["409"] = &huma.Response{
		Description: "A Run is already active, or committed Conditions blocked manual admission",
		Content: map[string]*huma.MediaType{
			problemContentType: {
				Schema: api.OpenAPI().Components.Schemas.Schema(
					reflect.TypeFor[conditionBlockedProblemError](),
					false,
					"",
				),
			},
		},
	}
	huma.Register(api, huma.Operation{
		OperationID:   "start-automation-run",
		Method:        http.MethodPost,
		Path:          "/automations/{automation_id}/runs",
		Summary:       "Start a manual Automation Run",
		Tags:          []string{"Automations"},
		DefaultStatus: http.StatusAccepted,
		MaxBodyBytes:  manualRunMaximumBodyBytes,
		RequestBody: &huma.RequestBody{Required: false, Content: map[string]*huma.MediaType{
			jsonContentType: {Schema: &huma.Schema{
				Type: huma.TypeObject, AdditionalProperties: false,
				Properties: map[string]*huma.Schema{"bypass_conditions": {Type: huma.TypeBoolean}},
			}},
		}},
		Responses: responses,
	}, handler.startAutomationRun)
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
		return nil, handler.mapError(ctx, "start-automation-run", err)
	}
	body, err := automationRunBody(run)
	if err != nil {
		handler.logInternalFailure(ctx, "start-automation-run", "mapping", err)
		return nil, newProblem(
			http.StatusInternalServerError,
			"invalid_history_evidence",
			"automation history evidence is invalid",
		)
	}
	return &AutomationRunOutput{
		Location: "/v1/automations/" + string(run.AutomationID) + "/history/" + string(run.ID),
		Body:     body,
	}, nil
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

// conditionBlockedProblemCode maps one committed manual Condition Skip reason to
// its stable problem code.
func conditionBlockedProblemCode(reason automations.SkipReason) string {
	switch reason {
	case automations.SkipConditionsFalse:
		return "conditions_false"
	case automations.SkipConditionsUnknown:
		return "conditions_unknown"
	case automations.SkipBusy, automations.SkipStaleFact:
		return "conditions_blocked"
	}
	return "conditions_blocked"
}

// newConditionBlockedProblem maps a committed manual Condition Skip to its 409 problem.
func newConditionBlockedProblem(blocked *automations.ConditionsBlockedError) error {
	return &conditionBlockedProblemError{
		automationProblemError: *newProblem(http.StatusConflict, conditionBlockedProblemCode(blocked.Reason), "automation conditions prevented manual admission"),
		HistoryID:              string(blocked.SkipID),
		HistoryURL: "/v1/automations/" + string(
			blocked.AutomationID,
		) + "/history/" + string(
			blocked.SkipID,
		),
	}
}

// conditionBlockedProblemError links the Skip committed by a blocked manual admission.
type conditionBlockedProblemError struct {
	automationProblemError

	HistoryID  string `json:"history_id,omitempty"`
	HistoryURL string `json:"history_url,omitempty"`
}

// Schema includes the fields JSON promotes from the unexported embedded problem,
// which Huma's default reflection skips. Both shapes remain derived from wire types.
func (conditionBlockedProblemError) Schema(registry huma.Registry) *huma.Schema {
	type conflictFields conditionBlockedProblemError
	schema := huma.SchemaFromType(registry, reflect.TypeFor[conflictFields]())
	problem := huma.SchemaFromType(registry, reflect.TypeFor[automationProblemError]())
	maps.Copy(schema.Properties, problem.Properties)
	schema.Required = append(schema.Required, problem.Required...)
	return schema
}
