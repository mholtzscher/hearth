package mcpapi

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerOutput binds the single Hearth pipeline to the SDK's raw tool API.
func registerOutput[I, O any](server *Server, tool Tool[I, O]) {
	input, preparationErr := prepareInput[I]()
	if preparationErr != nil {
		panic(preparationErr)
	}
	validator, preparationErr := compileSuccessSchema[O]()
	if preparationErr != nil {
		panic(preparationErr)
	}
	server.server.AddTool(&mcp.Tool{
		Name: tool.Name, Description: tool.Description,
		InputSchema: input.advertised,
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		decoded, inputErr := input.Decode(request.Params.Arguments)
		if inputErr != nil {
			return renderToolFailure(inputErr)
		}
		output, handlerErr := tool.Handler(ctx, decoded)
		if handlerErr != nil {
			return renderToolFailure(handlerErr)
		}
		encoded, validationErr := encodeValidatedOutput(output, validator)
		if validationErr != nil {
			return renderToolFailure(InternalToolError(validationErr))
		}
		return &mcp.CallToolResult{
			StructuredContent: encoded,
			Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		}, nil
	})
}
