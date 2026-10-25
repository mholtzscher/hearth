package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

//nolint:goconst // Operation metadata stays literal at registration.
func registerReplaceAutomation(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID:      "replace-automation",
		Method:           http.MethodPut,
		Path:             "/automations/{automation_id}",
		Summary:          "Replace an Automation",
		Tags:             []string{"Automations"},
		SkipValidateBody: true,
		MaxBodyBytes:     automationRequestMaxBytes + 1,
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
			jsonContentType: {Schema: &huma.Schema{Ref: "#/components/schemas/AutomationReplacement"}},
		}},
		Errors: []int{
			http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusConflict,
			http.StatusServiceUnavailable, http.StatusInternalServerError,
		},
	}, handler.replaceAutomation)
}

// ReplaceAutomationInput replaces one definition at a required current revision.
type ReplaceAutomationInput struct {
	AutomationID string          `path:"automation_id" doc:"Canonical Hearth Automation ID"`
	Body         json.RawMessage `                     doc:"expected_revision envelope and definition"`
}

func (handler *handler) replaceAutomation(
	ctx context.Context,
	input *ReplaceAutomationInput,
) (*AutomationOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	expectedRevision, definition, err := decodeReplaceEnvelope(input.Body)
	if err != nil {
		return nil, err
	}
	record, err := handler.automations.ReplaceAutomation(ctx, id, expectedRevision, definition)
	if err != nil {
		return nil, mapDomainError(err)
	}
	return &AutomationOutput{Body: automationBody(record)}, nil
}

// decodeReplaceEnvelope strictly decodes the expected-revision envelope and its nested definition.
func decodeReplaceEnvelope(
	raw json.RawMessage,
) (int64, automations.Definition, error) {
	if len(raw) > automationRequestMaxBytes {
		return 0, automations.Definition{}, newProblem(
			http.StatusRequestEntityTooLarge,
			"invalid_request_body",
			"request body exceeds limit",
		)
	}
	var envelope struct {
		ExpectedRevision int64           `json:"expected_revision"`
		Definition       json.RawMessage `json:"definition"`
	}
	if err := automations.DecodeStrictJSONObject(raw, &envelope); err != nil {
		return 0, automations.Definition{}, newProblem(
			http.StatusBadRequest, "invalid_request_body", "replacement body is invalid",
		)
	}
	if envelope.ExpectedRevision < 1 {
		return 0, automations.Definition{}, newProblem(
			http.StatusBadRequest, "invalid_revision", "expected_revision must be at least 1",
		)
	}
	definition, err := decodeDefinitionBody(envelope.Definition)
	if err != nil {
		return 0, automations.Definition{}, err
	}
	return envelope.ExpectedRevision, definition, nil
}

// publishReplacementSchema documents the required expected_revision/definition envelope.
//
//nolint:goconst // Canonical schema fields stay literal at publication.
func publishReplacementSchema(api huma.API) {
	minimumRevision := float64(1)
	api.OpenAPI().Components.Schemas.Map()["AutomationReplacement"] = &huma.Schema{
		Type: huma.TypeObject,
		Properties: map[string]*huma.Schema{
			"expected_revision": {Type: huma.TypeInteger, Minimum: &minimumRevision},
			"definition":        {Ref: "#/components/schemas/AutomationDefinition"},
		},
		Required:             []string{"expected_revision", "definition"},
		AdditionalProperties: false,
	}
}
