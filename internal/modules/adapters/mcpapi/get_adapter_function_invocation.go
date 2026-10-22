//nolint:goconst // Literal field names keep this operation's contract local and readable.
package mcpapi

import (
	"context"
	"encoding/json"

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
		Name:        "get_adapter_function_invocation",
		Description: "Inspect one retained invocation's exact arguments and terminal evidence.",
		Input:       mustContract(mcptools.PrepareInput[invocationInput](getInvocationInputSchema())),
		Output:      mustContract(mcptools.PrepareOutput[InvocationDetail](invocationDetailSchema())),
		Handler:     h.getInvocation,
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

func invocationDetailSchema() map[string]any { return invocationRecordSchema(true) }

func getInvocationInputSchema() map[string]any {
	return functionToolSchema(map[string]any{"invocation_id": textSchema()}, "invocation_id")
}
