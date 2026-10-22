package mcpapi

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler handles one typed Tool call with transport request context.
type Handler[I, O any] func(context.Context, I) (O, error)

// Tool binds a typed handler to independently prepared contracts. Nil contracts
// prepare contracts for this registration, not skip validation.
// Only input schemas are advertised; successful output is validated server-side.
type Tool[I, O any] struct {
	Name        string
	Description string
	Input       *InputContract[I]
	Output      *OutputContract[O]
	// ExactFailureText preserves complete ToolError JSON rather than error prose.
	ExactFailureText bool
	Handler          Handler[I, O]
}

// Register adds a typed tool. Invalid static contracts panic at startup. SDK
// input validation and defaults precede the handler; success validation checks
// its encoded result without changing its bytes or applying output defaults.
func Register[I, O any](server *Server, tool Tool[I, O]) {
	RegisterWithRequest(server, ToolWithRequest[I, O]{
		Name: tool.Name, Description: tool.Description, Input: tool.Input, Output: tool.Output,
		ExactFailureText: tool.ExactFailureText,
		Handler: func(ctx context.Context, _ *mcp.CallToolRequest, input I) (O, error) {
			return tool.Handler(ctx, input)
		},
	})
}

// HandlerWithRequest provides the raw request for exact arguments or metadata.
type HandlerWithRequest[I, O any] func(context.Context, *mcp.CallToolRequest, I) (O, error)

// ToolWithRequest is Tool with a raw-request handler escape hatch.
type ToolWithRequest[I, O any] struct {
	Name             string
	Description      string
	Input            *InputContract[I]
	Output           *OutputContract[O]
	ExactFailureText bool
	Handler          HandlerWithRequest[I, O]
}

// RegisterWithRequest registers the same input/result pipeline as Register.
func RegisterWithRequest[I, O any](server *Server, tool ToolWithRequest[I, O]) {
	registerOutput(server, tool)
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
// structured content carries failure_code, message, and Details; its default
// text is "<code>: <message>". The cause is never published.
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
