package mcpapi

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler handles one typed Tool call with transport request context.
type Handler[I, O any] func(context.Context, I) (O, error)

// Tool binds a typed handler to type-owned schemas. Registration derives and
// prepares all contracts internally, including Huma's native schema hooks.
// Only input schemas are advertised; successful output is validated server-side.
type Tool[I, O any] struct {
	Name        string
	Description string
	Handler     Handler[I, O]
}

// Register adds a typed tool. Invalid static contracts panic at startup. Hearth
// input decoding and validation precede the handler; success validation checks
// its encoded result without changing its bytes or applying output defaults.
func Register[I, O any](server *Server, tool Tool[I, O]) {
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

// CodeInternalError is the stable code for a hidden server failure.
const CodeInternalError = "internal_error"

// InternalToolError retains a private cause for safe server diagnostics.
func InternalToolError(cause error) *ToolError {
	failure := &ToolError{Code: CodeInternalError, Message: "internal error"}
	if cause != nil {
		failure = failure.WithCause(cause)
	}
	return failure
}

// ToolError is a modeled isError result, not a JSON-RPC protocol error. Its
// structured content carries failure_code, message, and Details; text contains
// the same exact JSON. The cause is never published.
type ToolError struct {
	Code    string
	Message string
	Details map[string]any
	cause   error
}

// WithCause attaches the hidden server-side cause and returns the same failure.
func (e *ToolError) WithCause(cause error) *ToolError {
	e.cause = cause
	return e
}

// Error implements error without exposing the private cause.
func (e *ToolError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}
