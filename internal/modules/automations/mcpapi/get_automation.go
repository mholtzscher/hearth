package mcpapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func registerGetAutomation(server *mcptools.Server, handler *handler, definitions map[string]any) {
	schema := detailSchema()
	// Canonical documents retain their exact values and relocated reference bases.
	schema.Extensions = map[string]any{canonicalDefinitionsKeyword: definitions}
	mcptools.Register(server, mcptools.Tool[getAutomationToolInput, AutomationDetail]{
		SuccessSchema: schema,
		Name:          "get_automation",
		Description:   "Inspect an Automation revision and its complete canonical definition",
		Handler:       handler.getAutomation,
	})
}

type getAutomationToolInput struct {
	AutomationID automationID `json:"automation_id" doc:"Canonical Hearth Automation ID"`
}

func (handler *handler) getAutomation(
	ctx context.Context,
	input getAutomationToolInput,
) (AutomationDetail, error) {
	record, err := handler.automations.GetAutomation(ctx, automations.AutomationID(input.AutomationID))
	if err != nil {
		return AutomationDetail{}, automationFailure(err, failureAutomationNotFound)
	}
	result, err := automationDetail(record)
	if err != nil {
		return AutomationDetail{}, mcptools.InternalToolError(err)
	}
	return result, nil
}

// AutomationDetail owns the MCP inspection projection; definition is canonical v2 JSON.
type AutomationDetail struct {
	AutomationID string          `json:"automation_id"`
	Revision     int64           `json:"revision"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Definition   json.RawMessage `json:"definition"`
}

func automationDetail(record automations.Record) (AutomationDetail, error) {
	definition, err := automations.EncodeDefinition(record.Definition)
	if err != nil {
		return AutomationDetail{}, err
	}
	return AutomationDetail{AutomationID: string(record.ID), Revision: record.Revision,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, Definition: definition}, nil
}

//nolint:goconst // Wire field names stay literal beside this operation's result contract.
func detailSchema() *huma.Schema {
	detail := objectSchema(
		map[string]*huma.Schema{
			automationIDField: stringSchema(),
			"revision":        revisionSchema(),
			"created_at":      timeSchema(),
			"updated_at":      timeSchema(),
			"definition":      refSchema("#/$defs/definition"),
		},
		automationIDField,
		"revision",
		"created_at",
		"updated_at",
		"definition",
	)
	return detail
}
