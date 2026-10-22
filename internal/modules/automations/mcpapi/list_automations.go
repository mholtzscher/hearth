package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/apicursor"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerListAutomations(server *mcptools.Server, handler *handler) {
	mcptools.Register(server, mcptools.Tool[listAutomationsToolInput, AutomationPage]{
		Output:      mustContract(mcptools.PrepareOutput[AutomationPage](pageSchema(summarySchema()))),
		Name:        "list_automations",
		Description: "List compact Automation summaries; use get_automation to inspect a definition",
		Handler:     handler.listAutomations,
	})
}

type AutomationSummary struct {
	AutomationID string `json:"automation_id"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	Revision     int64  `json:"revision"`
}
type AutomationPage struct {
	Items      []AutomationSummary `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func (handler *handler) listAutomations(ctx context.Context, input listAutomationsToolInput) (AutomationPage, error) {
	params := automations.ListAutomationsParams{Limit: input.Limit.pageSize()}
	if input.Cursor != "" {
		id, err := apicursor.DecodeAutomations(input.Cursor)
		if err != nil {
			return AutomationPage{}, &mcptools.ToolError{
				Code:    "invalid_cursor",
				Message: "automation cursor is invalid",
			}
		}
		params.AfterID = id
	}
	page, err := handler.automations.ListAutomations(ctx, params)
	if err != nil {
		return AutomationPage{}, automationFailure(err, failureNone)
	}
	result := AutomationPage{Items: make([]AutomationSummary, len(page.Items))}
	for i, record := range page.Items {
		result.Items[i] = AutomationSummary{
			AutomationID: string(record.ID),
			Name:         record.Definition.Name,
			Enabled:      record.Definition.Enabled,
			Revision:     record.Revision,
		}
	}
	if page.HasMore && len(page.Items) > 0 {
		result.NextCursor, err = apicursor.EncodeAutomations(page.Items[len(page.Items)-1].ID)
		if err != nil {
			return AutomationPage{}, mcptools.InternalToolError(err)
		}
	}
	return result, nil
}

type listAutomationsToolInput struct {
	Limit  pageLimit `json:"limit,omitempty"  jsonschema:"Page size, 1 to 200; omitted means 50"`
	Cursor string    `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from a previous page"`
}

//nolint:goconst // Wire field names stay literal beside this operation's result contract.
func summarySchema() map[string]any {
	summary := objectSchema(
		map[string]any{
			automationIDField: stringSchema(),
			"name":            stringSchema(),
			"enabled":         map[string]any{"type": "boolean"},
			"revision":        revisionSchema(),
		},
		automationIDField,
		"name",
		"enabled",
		"revision",
	)
	return summary
}
