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
	input, toolOutput := tool.Input, tool.Output
	var preparationErr error
	if input == nil {
		input, preparationErr = defaultInput[I]()
		if preparationErr != nil {
			panic(preparationErr)
		}
	}
	if toolOutput == nil {
		toolOutput, preparationErr = defaultOutput[O]()
		if preparationErr != nil {
			panic(preparationErr)
		}
	}
	if len(input.document) == 0 {
		panic("uninitialized input contract")
	}
	if toolOutput.validator == nil {
		panic("uninitialized output contract")
	}
	mcp.AddTool[I, any](server.server, &mcp.Tool{
		Name: tool.Name, Description: tool.Description,
		InputSchema: input.document,
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
		encoded, validationErr := encodeValidatedOutput(output, toolOutput.validator)
		if validationErr != nil {
			return nil, nil, toolFailure(InternalToolError(validationErr))
		}
		return &mcp.CallToolResult{
			StructuredContent: encoded,
			Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		}, nil, nil
	})
}
