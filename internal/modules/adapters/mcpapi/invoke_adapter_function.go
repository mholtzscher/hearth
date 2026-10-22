//nolint:goconst // Literal field names keep this operation's contract local and readable.
package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
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
		ExactFailureText: true,
		InputSchema:      invokeFunctionInputSchema(),
		SuccessSchema:    invocationSuccessSchema(),
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

func invocationSuccessSchema() map[string]any {
	properties, required := invocationIdentitySchema("succeeded")
	properties["result"] = objectSchema()
	return functionToolSchema(properties, append(required, "result")...)
}

func invokeFunctionInputSchema() *jsonschema.Schema {
	schema, err := jsonschema.For[invokeFunctionInput](nil)
	if err != nil {
		panic(err)
	}
	// RawMessage is an object here, not the byte array reflection infers.
	schema.Properties["arguments"] = &jsonschema.Schema{Type: "object"}
	return schema
}
