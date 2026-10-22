package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type invocationInput struct {
	InvocationID string `json:"invocation_id"`
}

type InvocationDetail struct {
	InvocationSummary

	Arguments json.RawMessage `json:"arguments"`
	Deadline  string          `json:"deadline"`
	Result    json.RawMessage `json:"result,omitempty"`
}

func registerGetAdapterFunctionInvocation(server *mcptools.Server, h *functionHandler) {
	mcptools.Register(server, mcptools.Tool[invocationInput, InvocationDetail]{
		Name:          "get_adapter_function_invocation",
		Description:   "Inspect one retained invocation's exact arguments and terminal evidence.",
		SuccessSchema: invocationDetailSchema(),
		Handler:       h.getInvocation,
	})
}

func (h *functionHandler) getInvocation(ctx context.Context, in invocationInput) (InvocationDetail, error) {
	item, err := h.service.GetInvocation(ctx, adapters.InvocationID(in.InvocationID))
	if err != nil {
		return InvocationDetail{}, functionToolError(err)
	}
	if _, err = functionschema.NormalizeObject(item.Arguments); err != nil {
		return InvocationDetail{}, mcptools.InternalToolError(err)
	}
	result := InvocationDetail{
		InvocationSummary: invocationSummary(item),
		Arguments:         item.Arguments,
		Deadline:          formatTime(item.Deadline),
	}
	if item.Completion != nil {
		if success, ok := item.Completion.Evidence.(adapters.Succeeded); ok {
			if _, err = functionschema.NormalizeObject(success.Result); err != nil {
				return InvocationDetail{}, mcptools.InternalToolError(err)
			}
			result.Result = success.Result
		}
	}
	return result, nil
}

//nolint:goconst // Literal schema types and statuses keep each alternative readable.
func invocationDetailSchema() *huma.Schema {
	function := functionSummarySchema().Properties
	var variants []*huma.Schema
	for _, status := range []string{"running", "succeeded", "failed", "not_executed", "unknown"} {
		properties, required := invocationIdentitySchema(status, function)
		properties["started_at"] = &huma.Schema{Type: "string"}
		properties["completed_at"] = &huma.Schema{Type: "string"}
		properties["arguments"] = &huma.Schema{Type: "object"}
		properties["deadline"] = &huma.Schema{Type: "string"}
		required = append(required, "started_at", "completed_at", "arguments", "deadline")
		switch status {
		case "running":
			properties["completed_at"] = &huma.Schema{Type: "null"}
		case "succeeded":
			properties["result"] = &huma.Schema{Type: "object"}
			required = append(required, "result")
		default:
			properties["reason_code"] = &huma.Schema{Type: "string", Pattern: `^[a-z0-9][a-z0-9_.-]{0,127}$`}
			required = append(required, "reason_code")
		}
		variants = append(variants, &huma.Schema{
			Type: "object", AdditionalProperties: false, Properties: properties, Required: required,
		})
	}
	return &huma.Schema{Type: "object", OneOf: variants}
}
