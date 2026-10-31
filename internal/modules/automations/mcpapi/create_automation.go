package mcpapi

import (
	"context"
	"encoding/json"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerCreateAutomation(server *mcptools.Server, handler *handler) {
	mcptools.Register(server, mcptools.Tool[createAutomationToolInput, AutomationReceipt]{
		Name:        "create_automation",
		Description: "Create an Automation and return its committed identity and revision receipt using definition schema v1. Every Step requires kind, including command. Observation comparisons address the Observation value directly: use value_pointer \"\" for scalar values and never /state/value; the pointer alias is invalid.",
		Handler:     handler.createAutomation,
	})
}

type createAutomationToolInput struct {
	Definition definitionValue `json:"definition" doc:"The strict Automation definition document"`
}

func (createAutomationToolInput) ValidateEnvelope(raw json.RawMessage) error {
	return validateDefinitionEnvelope(raw)
}

func (handler *handler) createAutomation(
	ctx context.Context,
	input createAutomationToolInput,
) (AutomationReceipt, error) {
	record, err := handler.automations.CreateAutomation(ctx, input.Definition.value)
	if err != nil {
		return AutomationReceipt{}, automationFailure(err, failureNone)
	}
	return automationReceipt(record), nil
}
