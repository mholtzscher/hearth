//nolint:goconst // Literal field names keep this operation's contract local and readable.
package mcpapi

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

const maximumFunctionBodyBytes = 128 * 1024

type invokeFunctionInput struct {
	functionInput

	CatalogRevision string          `json:"catalog_revision"`
	Arguments       json.RawMessage `json:"arguments"`
}
type InvocationSuccess struct {
	InvocationID    string          `json:"invocation_id"`
	AdapterID       string          `json:"adapter_id"`
	FunctionKey     string          `json:"function_key"`
	CatalogRevision string          `json:"catalog_revision"`
	Status          string          `json:"status"`
	Result          json.RawMessage `json:"result"`
}

func registerInvokeAdapterFunction(server *mcptools.Server, h *functionHandler) {
	mcptools.RegisterWithRequest(server, mcptools.ToolWithRequest[invokeFunctionInput, InvocationSuccess]{
		Name:             "invoke_adapter_function",
		Description:      "Discover and describe first; invoke the selected revision once. Arguments and results are retained for thirty days and must contain no secrets. Repeating may repeat effects; never automatically retry. Inspect the returned invocation_id for evidence.",
		ExactOutput:      true,
		ExactFailureText: true,
		InputSchema:      invokeFunctionInputSchema(),
		OutputSchema:     invocationSuccessSchema(),
		Handler:          h.invoke,
	})
}

func (h *functionHandler) invoke(
	ctx context.Context,
	request *mcp.CallToolRequest,
	_ invokeFunctionInput,
) (InvocationSuccess, error) {
	// Decode the original request, not the SDK's default-applied numeric copy.
	var in invokeFunctionInput
	if len(request.Params.Arguments) > maximumFunctionBodyBytes {
		return InvocationSuccess{}, functionToolError(adapters.ErrInvalidFunctionInput)
	}
	if !request.Params.Arguments.IsValid() {
		return InvocationSuccess{}, functionToolError(adapters.ErrInvalidFunctionInput)
	}
	if err := json.Unmarshal(request.Params.Arguments, &in); err != nil {
		return InvocationSuccess{}, functionToolError(adapters.ErrInvalidFunctionInput)
	}
	item, err := h.service.Invoke(
		ctx,
		adapters.InvokeInput{AdapterID: in.AdapterID, FunctionKey: adapters.FunctionKey(in.FunctionKey),
			CatalogRevision: adapters.CatalogID(in.CatalogRevision), Arguments: in.Arguments},
	)
	if err != nil {
		return InvocationSuccess{}, functionToolError(err)
	}
	receipt := invocationSummary(item).InvocationReceipt
	if receipt.Status != "succeeded" {
		return InvocationSuccess{}, &mcptools.ToolError{
			Code:    receipt.Status,
			Message: "function invocation did not succeed",
			Details: map[string]any{"invocation": receipt},
		}
	}
	success, ok := item.Completion.Evidence.(adapters.Succeeded)
	if !ok {
		return InvocationSuccess{}, mcptools.InternalToolError(nil)
	}
	if _, err = functionschema.NormalizeObject(success.Result); err != nil {
		return InvocationSuccess{}, mcptools.InternalToolError(err)
	}
	return InvocationSuccess{
		InvocationID:    receipt.InvocationID,
		AdapterID:       receipt.AdapterID,
		FunctionKey:     receipt.FunctionKey,
		CatalogRevision: receipt.CatalogRevision,
		Status:          receipt.Status,
		Result:          success.Result,
	}, nil
}

//nolint:gochecknoglobals // Fixed immutable contract, independent of server dependencies.
var invocationSuccessSchema = sync.OnceValue(func() *huma.Schema {
	properties, required := invocationIdentitySchema("succeeded")
	properties["result"] = objectSchema()
	return functionToolSchema(properties, append(required, "result")...)
})

//nolint:gochecknoglobals // Fixed immutable input, independent of server dependencies.
var invokeFunctionInputSchema = sync.OnceValue(func() *huma.Schema {
	return functionToolSchema(map[string]*huma.Schema{
		"adapter_id": textSchema(), "function_key": textSchema(),
		"catalog_revision": textSchema(), "arguments": objectSchema(),
	}, "adapter_id", "function_key", "catalog_revision", "arguments")
})
