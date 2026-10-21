package mcpapi

import (
	"context"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type invocationListInput struct {
	functionPageInput

	FunctionKey string `json:"function_key,omitempty"`
}
type InvocationPage struct {
	Items      []InvocationSummary `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

func registerListAdapterFunctionInvocations(server *mcptools.Server, h *functionHandler) {
	mcptools.Register(server, mcptools.Tool[invocationListInput, InvocationPage]{
		Name:         "list_adapter_function_invocations",
		Description:  "List compact retained invocation evidence; inspect an invocation for arguments and result. History is not a queue to retry.",
		ExactOutput:  true,
		InputSchema:  listInvocationsInputSchema(),
		OutputSchema: invocationSummaryPageSchema(),
		Handler:      h.listInvocations,
	})
}

func (h *functionHandler) listInvocations(ctx context.Context, in invocationListInput) (InvocationPage, error) {
	page, err := h.service.ListInvocations(ctx, adapters.InvocationFilter{
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

//nolint:gochecknoglobals // Fixed immutable contract, independent of server dependencies.
var invocationSummaryPageSchema = sync.OnceValue(
	func() *huma.Schema { return collectionSchema(invocationRecordSchema(false)) },
)

//nolint:gochecknoglobals // Fixed immutable input, independent of server dependencies.
var listInvocationsInputSchema = sync.OnceValue(func() *huma.Schema {
	properties := functionPageProperties()
	properties["function_key"] = textSchema()
	return functionToolSchema(properties, "adapter_id")
})
