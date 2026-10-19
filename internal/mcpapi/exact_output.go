package mcpapi

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerExactOutput retains SDK input validation and structured failure handling.
// Out is any with a nil success value so the SDK leaves our raw result untouched.
func registerExactOutput[I, O any](server *Server, tool ToolWithRequest[I, O]) {
	outputSchema := portableOutputSchema[O](tool.OutputSchema)
	success := tool.OutputSchema
	if success == nil {
		success = successSchema[O]()
	}
	resolved := compileOutputSuccess(success, tool.OutputSchema != nil)
	mcp.AddTool(server.server, &mcp.Tool{
		Name: tool.Name, Description: tool.Description,
		InputSchema: portableInputSchema[I](tool.InputSchema), OutputSchema: outputSchema,
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
		encoded, validationErr := encodeValidatedOutput(output, resolved)
		if validationErr != nil {
			return nil, nil, toolFailure(InternalToolError(validationErr))
		}
		return &mcp.CallToolResult{
			StructuredContent: encoded,
			Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		}, nil, nil
	})
}
