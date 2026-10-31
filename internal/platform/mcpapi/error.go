package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Structured failure field names shared by every [ToolError] result.
const (
	// toolErrorCodeField carries the stable, machine-readable failure code.
	toolErrorCodeField = "failure_code"
	// toolErrorMessageField carries the human-readable failure summary.
	toolErrorMessageField = "message"
)

// Generic message for a handler failure the wrapper does not model.
const unexpectedToolErrorMessage = "internal error"

// Structured diagnostic field names for one handler failure the client must not
// learn the detail of. The cause itself is never logged as text: an unknown
// error string can carry secrets or rejected values.
const (
	mcpToolEventKey        = "event"
	mcpToolNameKey         = "tool"
	mcpToolErrorCodeKey    = "error_code"
	mcpToolErrorTypeKey    = "error_type"
	mcpToolInternalFailure = "mcp.tool_internal_failure"

	// mcpToolErrorCodeUnexpected is the fixed code for a handler failure the
	// wrapper does not model.
	mcpToolErrorCodeUnexpected = "unexpected_error"
	// mcpToolErrorCodeInternal is the fixed code for a modeled internal failure
	// that retained a cause with [ToolError.WithCause].
	mcpToolErrorCodeInternal = CodeInternalError
)

// unexpectedToolError is a handler failure that is neither a domain [ToolError]
// nor an SDK protocol error. The client sees only the generic message; the
// cause stays reachable through [errors.Unwrap] for server-side diagnostics.
type unexpectedToolError struct {
	cause error
}

func (*unexpectedToolError) Error() string { return unexpectedToolErrorMessage }

func (e *unexpectedToolError) Unwrap() error { return e.cause }

// toolFailure maps the error a typed Tool handler returned onto the error the
// SDK renders: a [ToolError] and a [jsonrpc.Error] cross unchanged, and
// anything else is an unpublished invariant whose detail the client never
// learns.
func toolFailure(err error) error {
	if err == nil {
		return nil
	}
	if toolError, ok := errors.AsType[*ToolError](err); ok {
		return toolError
	}
	if protocolError, ok := errors.AsType[*jsonrpc.Error](err); ok {
		// The SDK detects a protocol error with a type assertion, so return the
		// concrete value rather than a wrapper.
		return protocolError
	}
	return &unexpectedToolError{cause: err}
}

// toolErrorMiddleware diagnoses unexpected failures with a retained private
// cause. Rendering belongs to the registered handler; cancellation stays silent.
func toolErrorMiddleware(logger *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, request)
			callResult, ok := result.(*mcp.CallToolResult)
			if !ok || callResult == nil {
				return result, err
			}
			logToolFailure(ctx, logger, request, callResult)
			return result, err
		}
	}
}

// logToolFailure records one unexpected handler failure that retains a private
// cause, with a fixed code and safe metadata. Expected request cancellation
// stays silent; joined or unrelated failures remain diagnostic.
//
// A failure is logged only when a cause is retained: an [unexpectedToolError],
// or a [ToolError] with code [CodeInternalError] that attached the cause with
// [ToolError.WithCause]. A domain [ToolError] is an expected outcome the client
// already branches on, the SDK's automatic input rejection is not a server
// fault, and an internal failure with no retained cause has nothing to report,
// so all stay silent.
func logToolFailure(
	ctx context.Context,
	logger *slog.Logger,
	request mcp.Request,
	result *mcp.CallToolResult,
) {
	code, cause := retainedFailure(result)
	if cause == nil || expectedToolCancellation(ctx, cause) {
		return
	}
	logger.ErrorContext(ctx, "MCP tool failed",
		slog.String(mcpToolEventKey, mcpToolInternalFailure),
		slog.String(mcpToolNameKey, toolName(request)),
		slog.String(mcpToolErrorCodeKey, code),
		slog.String(mcpToolErrorTypeKey, fmt.Sprintf("%T", cause)),
	)
}

func expectedToolCancellation(ctx context.Context, err error) bool {
	ownerErr := ctx.Err()
	if ownerErr == nil {
		return false
	}
	// A joined failure may describe detached persistence after the caller left.
	// Only an ordinary wrapping chain ending in the owner error is expected.
	for err != nil {
		if err == ownerErr { //nolint:errorlint // Require the exact owner sentinel, not a matching error class.
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

// retainedFailure returns the fixed failure code and server-side cause one
// isError result keeps off the client, or an empty code and nil cause when the
// result carries nothing to report. The cause is returned so the caller can
// record its Go type name; its message never reaches the log.
func retainedFailure(result *mcp.CallToolResult) (string, error) {
	if result == nil || !result.IsError {
		return "", nil
	}
	if toolError, ok := errors.AsType[*ToolError](result.GetError()); ok {
		if toolError.Code != CodeInternalError {
			return "", nil
		}
		return mcpToolErrorCodeInternal, toolError.cause
	}
	if unexpected, ok := errors.AsType[*unexpectedToolError](result.GetError()); ok {
		return mcpToolErrorCodeUnexpected, unexpected.cause
	}
	return "", nil
}

func toolName(request mcp.Request) string {
	call, ok := request.(*mcp.CallToolRequest)
	if !ok {
		return ""
	}
	return call.Params.Name
}

// renderToolFailure is the sole owner of modeled failure JSON. SetError keeps
// the private cause available to the diagnostic middleware without changing text.
func renderToolFailure(err error) (*mcp.CallToolResult, error) {
	failure := toolFailure(err)
	if protocol, ok := errors.AsType[*jsonrpc.Error](failure); ok {
		return nil, protocol
	}
	result := &mcp.CallToolResult{}
	if modeled, ok := errors.AsType[*ToolError](failure); ok {
		encoded, encodeErr := json.Marshal(modeled.structuredContent())
		if encodeErr != nil {
			return renderToolFailure(InternalToolError(encodeErr))
		}
		result.StructuredContent = json.RawMessage(encoded)
		result.Content = []mcp.Content{&mcp.TextContent{Text: string(encoded)}}
	}
	result.SetError(failure)
	return result, nil
}

// structuredContent renders the machine-readable half of one failure:
// failure_code and message always, with Details merged beside them, so a
// Command failure publishes the durable status and Command ID that
// get_command returns and an Automation failure publishes its history
// coordinates.
func (e *ToolError) structuredContent() map[string]any {
	content := make(map[string]any, len(e.Details))
	maps.Copy(content, e.Details)
	content[toolErrorCodeField] = e.Code
	content[toolErrorMessageField] = e.Message
	return content
}
