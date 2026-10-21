package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func registerCreateAutomation(api huma.API, handler *handler) {
	create := operation("create-automation", http.MethodPost, "/automations", "Create an Automation")
	create.DefaultStatus = http.StatusCreated
	create.SkipValidateBody = true
	create.RequestBody = definitionRequestBody()
	create.Errors = []int{
		http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusInternalServerError,
	}
	huma.Register(api, create, handler.createAutomation)
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

func definitionRequestBody() *huma.RequestBody {
	return &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{
		jsonContentType: {Schema: &huma.Schema{Ref: "#/components/schemas/AutomationDefinition"}},
	}}
}
