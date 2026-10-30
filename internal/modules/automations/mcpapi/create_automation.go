package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerCreateAutomation(server *mcptools.Server, handler *handler, definition json.RawMessage) {
	mcptools.RegisterWithRequest(server, mcptools.ToolWithRequest[createAutomationToolInput, AutomationReceipt]{
		Name:             "create_automation",
		Description:      "Create an Automation and return its committed identity and revision receipt using definition schema v1. Every Step requires kind, including command. Observation comparisons address the Observation value directly: use value_pointer \"\" for scalar values and never /state/value; the pointer alias is invalid.",
		InputSchema:      definitionInputSchema[createAutomationToolInput](definition),
		HandlerValidated: []string{definitionProperty},
		Handler:          handler.createAutomation,
	})
}

// createAutomationToolInput carries one strict Automation definition document.
// Definition is declared as a JSON object so the SDK can decode the argument, but
// the tool advertises the canonical Automation definition schema in place of the
// derived property schema, so the SDK validates the document against the same
// strict schema the Huma route publishes. The decoded value itself is unused: the
// handler reads the exact document bytes from the raw tool request, because the
// SDK's argument pipeline rounds every number through float64.
type createAutomationToolInput struct {
	Definition map[string]any `json:"definition" doc:"The strict Automation definition document"`
}

// createAutomation decodes the exact definition document and persists it. The
// decoded argument struct is unused: the SDK validates it against the published
// schema, but the document itself is decoded from the raw request so no number
// is rounded before the canonical strict decoder sees it.
func (handler *handler) createAutomation(
	ctx context.Context,
	request *mcp.CallToolRequest,
	_ createAutomationToolInput,
) (AutomationReceipt, error) {
	definition, err := definitionArgument(request.Params.Arguments)
	if err != nil {
		return AutomationReceipt{}, automationFailure(err, failureNone)
	}
	record, err := handler.automations.CreateAutomation(ctx, definition)
	if err != nil {
		return AutomationReceipt{}, automationFailure(err, failureNone)
	}
	return automationReceipt(record), nil
}
