package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func registerGetAutomation(api huma.API, handler *handler) {
	huma.Register(api, operation(
		"get-automation", http.MethodGet, "/automations/{automation_id}", "Get an Automation", "Automations",
	), handler.getAutomation)
}

// GetAutomationInput addresses one current definition.
type GetAutomationInput struct {
	AutomationID string `path:"automation_id" doc:"Canonical Hearth Automation ID"`
}

func (handler *handler) getAutomation(
	ctx context.Context,
	input *GetAutomationInput,
) (*AutomationOutput, error) {
	id, err := automations.ParseAutomationID(input.AutomationID)
	if err != nil {
		return nil, newProblem(http.StatusBadRequest, "invalid_automation_id", "automation_id is not canonical")
	}
	record, err := handler.automations.GetAutomation(ctx, id)
	if err != nil {
		return nil, mapDomainError(err)
	}
	return &AutomationOutput{Body: automationBody(record)}, nil
}
