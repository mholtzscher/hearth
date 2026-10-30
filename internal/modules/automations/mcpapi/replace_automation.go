package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerReplaceAutomation(server *mcptools.Server, handler *handler, definition json.RawMessage) {
	mcptools.RegisterWithRequest(server, mcptools.ToolWithRequest[replaceAutomationToolInput, AutomationReceipt]{
		Name:             "replace_automation",
		Description:      "Replace an Automation and return its committed identity and revision receipt using definition schema v1. Every Step requires kind, including command. Observation comparisons address the Observation value directly: use value_pointer \"\" for scalar values and never /state/value; the pointer alias is invalid.",
		InputSchema:      definitionInputSchema[replaceAutomationToolInput](definition),
		HandlerValidated: []string{definitionProperty},
		Handler:          handler.replaceAutomation,
	})
}

type replaceAutomationToolInput struct {
	AutomationID     automationID     `json:"automation_id"     doc:"Canonical Hearth Automation ID"`
	ExpectedRevision expectedRevision `json:"expected_revision" doc:"Current revision the replacement expects"`
	Definition       map[string]any   `json:"definition"        doc:"The replacement strict Automation definition document"`
}

func (handler *handler) replaceAutomation(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input replaceAutomationToolInput,
) (AutomationReceipt, error) {
	definition, err := definitionArgument(request.Params.Arguments)
	if err != nil {
		return AutomationReceipt{}, automationFailure(err, failureNone)
	}
	record, err := handler.automations.ReplaceAutomation(
		ctx,
		automations.AutomationID(input.AutomationID),
		int64(input.ExpectedRevision),
		definition,
	)
	if err != nil {
		return AutomationReceipt{}, automationFailure(err, failureAutomationNotFound)
	}
	return automationReceipt(record), nil
}
