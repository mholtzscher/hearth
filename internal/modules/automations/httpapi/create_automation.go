package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

const automationRequestMaxBytes = 196608

//nolint:goconst // Operation metadata stays literal at registration.
func registerCreateAutomation(api huma.API, handler *handler) {
	huma.Register(api, huma.Operation{
		OperationID:      "create-automation",
		Method:           http.MethodPost,
		Path:             "/automations",
		Summary:          "Create an Automation",
		Tags:             []string{"Automations"},
		DefaultStatus:    http.StatusCreated,
		SkipValidateBody: true,
		MaxBodyBytes:     automationRequestMaxBytes + 1,
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
			jsonContentType: {Schema: &huma.Schema{Ref: "#/components/schemas/AutomationDefinition"}},
		}},
		Responses: problemResponses(api,
			http.StatusBadRequest, http.StatusConflict, http.StatusRequestEntityTooLarge,
			http.StatusUnprocessableEntity, http.StatusInternalServerError, http.StatusServiceUnavailable),
	}, handler.createAutomation)
}

// CreateAutomationInput leaves strict definition decoding to the canonical
// embedded schema, so unknown fields and typed-family violations are rejected.
type CreateAutomationInput struct {
	Body json.RawMessage
}

func (handler *handler) createAutomation(
	ctx context.Context,
	input *CreateAutomationInput,
) (*AutomationOutput, error) {
	definition, err := decodeDefinitionBody(input.Body)
	if err != nil {
		return nil, err
	}
	record, err := handler.automations.CreateAutomation(ctx, definition)
	if err != nil {
		return nil, mapDomainError(err)
	}
	return &AutomationOutput{
		Location: "/v1/automations/" + string(record.ID),
		Body:     automationBody(record),
	}, nil
}
