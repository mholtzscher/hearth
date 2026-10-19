package api

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

func (h *functionHandler) registerFunctionResources(server *mcpapi.Server) {
	for _, resource := range []struct{ name, uri string }{
		{"adapter_functions", "hearth://adapter/{adapter_id}/functions{?limit,cursor}"},
		{"adapter_function", "hearth://adapter/{adapter_id}/function/{function_key}"},
		{"adapter_function_invocations", "hearth://adapter/{adapter_id}/function-invocations{?function_key,limit,cursor}"},
		{"adapter_function_invocation", "hearth://adapter-function-invocation/{invocation_id}"},
	} {
		mcpapi.RegisterResourceTemplate(server, resource.name, "Poll retained Adapter function data", resource.uri,
			mcpapi.ResourceMIMEType, h.functionResource, functionResourceFailure)
	}
}

func (h *functionHandler) functionResource(ctx context.Context, uri string) (any, error) {
	resource, err := parseMCPResourceURI(uri)
	if err != nil {
		return nil, err
	}
	if resource.kind == "adapter-function-invocation" && len(resource.segments) == 1 {
		if queryErr := resource.query.Only(); queryErr != nil {
			return nil, queryErr
		}
		out, readErr := h.getInvocation(ctx, &invocationInput{InvocationID: resource.segments[0]})
		if readErr != nil {
			return nil, functionToolError(readErr)
		}
		return out.Body, nil
	}
	if resource.kind != "adapter" || len(resource.segments) < 2 {
		return nil, mcpResourceNotFoundError{}
	}
	adapterID := resource.segments[0]
	if len(resource.segments) == 3 && resource.segments[1] == "function" {
		if queryErr := resource.query.Only(); queryErr != nil {
			return nil, queryErr
		}
		out, readErr := h.getFunction(ctx, &FunctionInput{AdapterID: adapterID, FunctionKey: resource.segments[2]})
		if readErr != nil {
			return nil, functionToolError(readErr)
		}
		return out.Body, nil
	}
	if len(resource.segments) != 2 { //nolint:mnd // Adapter ID and collection name are the two URI segments.
		return nil, mcpResourceNotFoundError{}
	}
	return h.functionPageResource(ctx, resource, adapterID)
}

func (h *functionHandler) functionPageResource(
	ctx context.Context,
	resource mcpResourceURI,
	adapterID string,
) (any, error) {
	allowed := []string{mcpapi.ResourceQueryLimit, mcpapi.ResourceQueryCursor}
	if resource.segments[1] == "function-invocations" {
		allowed = append(allowed, "function_key")
	}
	if err := resource.query.Only(allowed...); err != nil {
		return nil, err
	}
	page, err := resource.query.Page()
	if err != nil {
		return nil, err
	}
	input := FunctionPageInput{AdapterID: adapterID, Limit: page.Limit, Cursor: page.Cursor}
	switch resource.segments[1] {
	case "functions":
		out, readErr := h.listFunctions(ctx, &input)
		if readErr != nil {
			return nil, functionToolError(readErr)
		}
		return out.Body, nil
	case "function-invocations":
		key, queryErr := resource.query.Value("function_key")
		if queryErr != nil {
			return nil, queryErr
		}
		out, readErr := h.listInvocations(ctx, &invocationListInput{FunctionPageInput: input, FunctionKey: key})
		if readErr != nil {
			return nil, functionToolError(readErr)
		}
		return out.Body, nil
	default:
		return nil, mcpResourceNotFoundError{}
	}
}

func functionResourceFailure(uri string, err error) error {
	if failure, ok := errors.AsType[*mcpapi.ToolError](err); ok {
		switch failure.Code {
		case "adapter_not_found", "function_not_found", "invocation_not_found":
			return mcp.ResourceNotFoundError(uri)
		case "invalid_input", functionCatalogChangedCode:
			return mcpapi.InvalidParamsError(failure.Message)
		}
	}
	return mcpResourceFailure(uri, err)
}
