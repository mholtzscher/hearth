package mcpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerOutput retains SDK input validation and structured failure handling.
// Out is any with a nil success value so the SDK leaves our raw result untouched.
func registerOutput[I, O any](server *Server, tool ToolWithRequest[I, O]) {
	input, preparationErr := prepareInputSchema[I](tool.InputSchema)
	if preparationErr != nil {
		panic(preparationErr)
	}
	validator, preparationErr := compileSuccessSchema[O](tool.SuccessSchema)
	if preparationErr != nil {
		panic(preparationErr)
	}
	mcp.AddTool[I, any](server.server, &mcp.Tool{
		Name: tool.Name, Description: tool.Description,
		InputSchema: input,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input I) (*mcp.CallToolResult, any, error) {
		output, handlerErr := tool.Handler(ctx, request, input)
		if handlerErr != nil {
			if failure, ok := errors.AsType[*ToolError](handlerErr); ok && tool.ExactFailureText {
				encoded, err := json.Marshal(failure.structuredContent())
				if err != nil {
					return nil, nil, toolFailure(InternalToolError(err))
				}
				// Keep opaque failure details exact in the text half too. MCP
				// clients may decode structured content through float64.
				result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
				result.SetError(failure)
				return result, nil, nil
			}
			return nil, nil, toolFailure(handlerErr)
		}
		encoded, validationErr := encodeValidatedOutput(output, validator)
		if validationErr != nil {
			return nil, nil, toolFailure(InternalToolError(validationErr))
		}
		return &mcp.CallToolResult{
			StructuredContent: encoded,
			Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		}, nil, nil
	})
}
