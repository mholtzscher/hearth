package mcpapi

import (
	"context"
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

// toolErrorMiddleware publishes the structured half of every domain [ToolError]
// result and logs every failure that retains a server-side cause.
//
// The SDK renders a handler error as an isError result whose text is the error
// string, but it owns the structured content field, so a typed handler cannot
// also set it. This receiving middleware reads the server-only error the SDK
// attached to the result and, for a domain ToolError, adds the machine-readable
// failure code and details beside the text. A success result keeps its typed
// structured content, and a failure the wrapper does not model gains only a
// diagnostic log.
func toolErrorMiddleware(logger *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, request)
			callResult, ok := result.(*mcp.CallToolResult)
			if !ok || callResult == nil {
				return result, err
			}
			attachToolErrorContent(callResult)
			logToolFailure(ctx, logger, request, callResult)
			return result, err
		}
	}
}

// logToolFailure records one handler failure that retains a cause the client
// must not receive, at Error level with a fixed code and safe metadata. Only
// its fixed failure code and Go error type name cross into the record.
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
	if cause == nil {
		return
	}
	logger.ErrorContext(ctx, "MCP tool failed",
		slog.String(mcpToolEventKey, mcpToolInternalFailure),
		slog.String(mcpToolNameKey, toolName(request)),
		slog.String(mcpToolErrorCodeKey, code),
		slog.String(mcpToolErrorTypeKey, fmt.Sprintf("%T", cause)),
	)
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

// attachToolErrorContent adds a domain [ToolError]'s structured fields to one
// isError result. Automatic input rejection and unmodelled failures produce
// isError results with no attached error or a generic one, so they keep their
// nil structured content.
func attachToolErrorContent(result *mcp.CallToolResult) {
	if result == nil || !result.IsError {
		return
	}
	var toolError *ToolError
	if !errors.As(result.GetError(), &toolError) {
		return
	}
	result.StructuredContent = toolError.structuredContent()
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
