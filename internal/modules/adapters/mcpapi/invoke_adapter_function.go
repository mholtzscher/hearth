//nolint:goconst // Literal field names keep this operation's contract local and readable.
package mcpapi

import (
	"context"
	"encoding/json"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

const maximumFunctionBodyBytes = 128 * 1024

type invokeFunctionInput struct {
	AdapterID       string              `json:"adapter_id"`
	FunctionKey     string              `json:"function_key"`
	CatalogRevision string              `json:"catalog_revision"`
	Arguments       mcptools.JSONObject `json:"arguments"`
}

func (invokeFunctionInput) ValidateEnvelope(raw json.RawMessage) error {
	if len(raw) > maximumFunctionBodyBytes {
		return functionToolError(adapters.ErrInvalidFunctionInput)
	}
	return nil
}

type InvocationSuccess struct {
	InvocationID    string          `json:"invocation_id"`
	AdapterID       string          `json:"adapter_id"`
	FunctionKey     string          `json:"function_key"`
	CatalogRevision string          `json:"catalog_revision"`
	Status          string          `json:"status"`
	Result          json.RawMessage `json:"result"`
}

func (InvocationSuccess) Schema(_ huma.Registry) *huma.Schema { return invocationSuccessSchema() }

func registerInvokeAdapterFunction(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[invokeFunctionInput, InvocationSuccess]{
		Name:        "invoke_adapter_function",
		Description: "Discover and describe first; invoke the selected revision once. Arguments and results are retained for thirty days and must contain no secrets. Repeating may repeat effects; never automatically retry. Inspect the returned invocation_id for evidence.",
		Handler:     h.invoke,
	})
}

func (h *handler) invoke(
	ctx context.Context,
	in invokeFunctionInput,
) (InvocationSuccess, error) {
	item, err := h.functions.Invoke(
		ctx,
		adapters.InvokeInput{AdapterID: in.AdapterID, FunctionKey: adapters.FunctionKey(in.FunctionKey),
			CatalogRevision: adapters.CatalogID(in.CatalogRevision), Arguments: json.RawMessage(in.Arguments)},
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

func invocationSuccessSchema() *huma.Schema {
	properties, required := invocationIdentitySchema("succeeded", functionSummarySchema().Properties)
	properties["result"] = &huma.Schema{Type: "object"}
	return &huma.Schema{
		Type: "object", AdditionalProperties: false,
		Properties: properties, Required: append(required, "result"),
	}
}
