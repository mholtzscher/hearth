package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// registerExactOutput retains SDK input validation and structured failure handling.
// Out is any with a nil success value so the SDK leaves our raw result untouched.
func registerExactOutput[I, O any](server *Server, tool ToolWithRequest[I, O]) {
	outputSchema := portableOutputSchema[O]()
	raw, err := json.Marshal(outputSchema)
	if err != nil {
		panic(fmt.Errorf("marshal exact output schema: %w", err))
	}
	var document any
	if err = json.Unmarshal(raw, &document); err != nil {
		panic(fmt.Errorf("decode exact output schema: %w", err))
	}
	compiler := jsonschema.NewCompiler()
	const schemaURI = "https://hearth.invalid/mcp-output.json"
	if err = compiler.AddResource(schemaURI, document); err != nil {
		panic(fmt.Errorf("load exact output schema: %w", err))
	}
	resolved, err := compiler.Compile(schemaURI)
	if err != nil {
		panic(fmt.Errorf("resolve exact output schema: %w", err))
	}
	mcp.AddTool(server.server, &mcp.Tool{
		Name: tool.Name, Description: tool.Description,
		InputSchema: portableInputSchema[I](tool.InputSchema), OutputSchema: outputSchema,
	}, func(ctx context.Context, request *mcp.CallToolRequest, input I) (*mcp.CallToolResult, any, error) {
		output, handlerErr := tool.Handler(ctx, request, input)
		if handlerErr != nil {
			return nil, nil, toolFailure(handlerErr)
		}
		encoded, marshalErr := json.Marshal(output)
		if marshalErr != nil {
			return nil, nil, toolFailure(InternalToolError(marshalErr))
		}
		// Validate a decoded copy only. Never marshal that copy back into the result.
		var value any
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if decodeErr := decoder.Decode(&value); decodeErr != nil {
			return nil, nil, toolFailure(InternalToolError(decodeErr))
		}
		if validationErr := resolved.Validate(value); validationErr != nil {
			return nil, nil, toolFailure(InternalToolError(validationErr))
		}
		return &mcp.CallToolResult{
			StructuredContent: json.RawMessage(encoded),
			Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		}, nil, nil
	})
}
