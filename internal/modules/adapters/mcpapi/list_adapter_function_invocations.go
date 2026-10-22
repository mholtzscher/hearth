package mcpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type invocationListInput struct {
	FunctionPageInput

	FunctionKey string `json:"function_key,omitempty"`
}
type InvocationPage struct {
	Items      []InvocationSummary `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func registerListAdapterFunctionInvocations(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[invocationListInput, InvocationPage]{
		Name:          "list_adapter_function_invocations",
		Description:   "List compact retained invocation evidence; inspect an invocation for arguments and result. History is not a queue to retry.",
		SuccessSchema: invocationSummaryPageSchema(),
		Handler:       h.listInvocations,
	})
}

func (h *handler) listInvocations(ctx context.Context, in invocationListInput) (InvocationPage, error) {
	page, err := h.functions.ListInvocations(ctx, adapters.InvocationFilter{
		AdapterID: in.AdapterID,
		FunctionKey: adapters.FunctionKey(
			in.FunctionKey,
		),
		PageInput: adapters.PageInput{ //nolint:modernize // Keep domain page construction explicit.
			Limit:  functionPageLimit(in.Limit),
			Cursor: in.Cursor,
		},
	})
	if err != nil {
		return InvocationPage{}, functionToolError(err)
	}
	result := InvocationPage{Items: make([]InvocationSummary, len(page.Items)), NextCursor: page.NextCursor}
	for i, item := range page.Items {
		result.Items[i] = invocationSummary(item)
	}
	return result, nil
}

//nolint:goconst // Literal schema types and statuses keep each alternative readable.
func invocationSummaryPageSchema() *huma.Schema {
	function := functionSummarySchema().Properties
	var variants []*huma.Schema
	for _, status := range []string{"running", "succeeded", "failed", "not_executed", "unknown"} {
		properties, required := invocationIdentitySchema(status, function)
		properties["started_at"] = &huma.Schema{Type: "string"}
		properties["completed_at"] = &huma.Schema{Type: "string"}
		required = append(required, "started_at", "completed_at")
		switch status {
		case "running":
			properties["completed_at"] = &huma.Schema{Type: "null"}
		case "succeeded":
			// Successful summaries omit the result; detail owns that evidence.
		default:
			properties["reason_code"] = &huma.Schema{Type: "string", Pattern: `^[a-z0-9][a-z0-9_.-]{0,127}$`}
			required = append(required, "reason_code")
		}
		variants = append(variants, &huma.Schema{
			Type: "object", AdditionalProperties: false, Properties: properties, Required: required,
		})
	}
	return &huma.Schema{
		Type: "object", AdditionalProperties: false,
		Properties: map[string]*huma.Schema{
			"items":       {Type: "array", Items: &huma.Schema{Type: "object", OneOf: variants}},
			"next_cursor": {Type: "string"},
		},
		Required: []string{"items"},
	}
}
