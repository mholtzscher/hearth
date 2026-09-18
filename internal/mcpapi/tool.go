package mcpapi

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Handler handles one typed Tool call. The context carries request-scoped
// values set by the transport layer, such as Echo middleware.
type Handler[I, O any] func(context.Context, I) (O, error)

// Tool describes one typed MCP tool: its identity and its handler.
//
// The input and output JSON Schemas are derived from I and O, reading json and
// jsonschema struct tags, so callers describe inputs with plain Go structs. The
// advertised output schema is the union of the derived success schema and the
// structured failure object [Register] can emit for a [ToolError].
type Tool[I, O any] struct {
	// Name is the tool name clients call, mirroring the Huma operationId in
	// snake_case.
	Name string
	// Description is the human-readable tool description surfaced to clients.
	Description string
	// InputSchema overrides the argument JSON Schema the SDK would derive from I.
	// Leave it nil to advertise the derived schema. Set it, for example with
	// [InputSchemaWithProperty], when one argument carries a strict document the
	// Go type cannot express, so the SDK validates that document against the
	// canonical schema before the handler runs.
	InputSchema any
	// Handler executes the tool for one decoded, validated input.
	Handler Handler[I, O]
}

// Register adds tool to server.
//
// The SDK derives the input and output JSON Schemas, decodes and validates the
// arguments before invoking the handler, validates the handler's output, and
// populates the result's structured content. A non-nil [Tool.InputSchema]
// replaces the derived argument schema. Invalid arguments are rejected
// automatically and the handler never runs.
//
// The advertised output schema is the union of the success type and the
// structured failure object, so a success result and a [ToolError] result each
// validate against it.
//
// A returned [ToolError] reaches the client as an isError result whose text is
// "<code>: <message>" and whose structured content carries the code, message,
// and details. Any other failure is reported generically.
func Register[I, O any](server *Server, tool Tool[I, O]) {
	mcp.AddTool(server.server, &mcp.Tool{
		Name:         tool.Name,
		Description:  tool.Description,
		InputSchema:  tool.InputSchema,
		OutputSchema: outputSchema[O](),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input I) (*mcp.CallToolResult, O, error) {
		output, err := tool.Handler(ctx, input)
		return nil, output, toolFailure(err)
	})
}

// HandlerWithRequest handles one typed Tool call with access to the raw MCP
// request. Prefer [Handler]: most tools need only the context and their input.
type HandlerWithRequest[I, O any] func(context.Context, *mcp.CallToolRequest, I) (O, error)

// ToolWithRequest describes one typed MCP tool whose handler needs the raw MCP
// request.
type ToolWithRequest[I, O any] struct {
	// Name is the tool name clients call, mirroring the Huma operationId in
	// snake_case.
	Name string
	// Description is the human-readable tool description surfaced to clients.
	Description string
	// InputSchema overrides the argument JSON Schema the SDK would derive from I.
	// Leave it nil to advertise the derived schema. Set it, for example with
	// [InputSchemaWithProperty], when one argument carries a strict document the
	// Go type cannot express, so the SDK validates that document against the
	// canonical schema before the handler runs.
	InputSchema any
	// Handler executes the tool with the raw MCP request.
	Handler HandlerWithRequest[I, O]
}

// RegisterWithRequest adds tool to server for handlers that need session or
// request metadata. Use [Register] otherwise: this escape hatch exists only for
// tools that genuinely need the raw request.
//
// The handler must still translate the request into plain domain values before
// calling a service; no MCP request crosses the service boundary. Failures are
// mapped exactly as [Register] maps them.
func RegisterWithRequest[I, O any](server *Server, tool ToolWithRequest[I, O]) {
	mcp.AddTool(server.server, &mcp.Tool{
		Name:         tool.Name,
		Description:  tool.Description,
		InputSchema:  tool.InputSchema,
		OutputSchema: outputSchema[O](),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input I) (*mcp.CallToolResult, O, error) {
		output, err := tool.Handler(ctx, req, input)
		return nil, output, toolFailure(err)
	})
}

// CodeInternalError is the stable failure code for a 500-class server failure
// the wrapper hides from the client.
//
// The client learns only that the call failed. A handler that still holds the
// server-side cause attaches it with [ToolError.WithCause]; that cause never
// reaches the client and the tool middleware logs it for server diagnostics.
const CodeInternalError = "internal_error"

// ToolError is a tool-domain failure that reaches the client as an MCP tool
// result with isError set, rather than as a JSON-RPC protocol error the model
// cannot recover from.
//
// A [Handler] returns a *ToolError to signal a recoverable failure the agent
// can branch on. The result's text is "<code>: <message>", and its structured
// content carries failure_code, message, and every Details field, so the agent
// can branch on codes instead of parsing prose.
type ToolError struct {
	// Code is the stable, machine-readable failure code, such as
	// "entity_disabled".
	Code string
	// Message is the human-readable failure summary.
	Message string
	// Details carries optional extra fields describing the failure.
	Details map[string]any
	// cause is the server-side failure this result hides from the client. It is
	// never rendered by [ToolError.Error] nor published as structured content;
	// the tool middleware logs it for an internal failure.
	cause error
}

// WithCause attaches the server-side failure this result hides from the client
// and returns the same [ToolError].
//
// A handler retaining a cause keeps it reachable for server diagnostics without
// widening the client contract: [ToolError.Error] and the structured content
// still carry only Code, Message, and Details. The tool middleware logs the
// cause only for an internal failure ([ToolError] with code
// [CodeInternalError]), so a domain failure's cause stays silent.
func (e *ToolError) WithCause(cause error) *ToolError {
	e.cause = cause
	return e
}

// Error implements the error interface as "<code>: <message>", or just the
// message when Code is empty.
func (e *ToolError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}
