package api

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// mcpFailureCode is the stable, machine-readable code an agent branches on when
// a Devices MCP Tool reports a domain failure.
//
// The code and a stable human summary cross the MCP boundary; the Hearth
// outcome's HTTP status does not, because MCP has no status codes. Command
// failure codes reuse the durable codes the Command record publishes, so the
// tool error and `get_command` describe one failure the same way.
type mcpFailureCode string

const (
	// mcpFailureNone marks a read that has no missing-parent outcome.
	mcpFailureNone mcpFailureCode = ""
	// mcpFailureInvalidRequest reports client input the handler rejected, such
	// as an opaque cursor this endpoint cannot interpret.
	mcpFailureInvalidRequest mcpFailureCode = "invalid_request"
	// mcpFailureEntityNotFound is the missing-Entity outcome of one read.
	mcpFailureEntityNotFound mcpFailureCode = "entity_not_found"
	// mcpFailureDeviceNotFound is the missing-Device outcome of one read.
	mcpFailureDeviceNotFound mcpFailureCode = "device_not_found"
	// mcpFailureAdapterNotFound is the missing-Adapter outcome of one read.
	mcpFailureAdapterNotFound mcpFailureCode = "adapter_not_found"
	// mcpFailureCommandNotFound is the missing-Command outcome of one read.
	mcpFailureCommandNotFound mcpFailureCode = "command_not_found"
	// mcpFailureEntityDisabled is the durable Command failure code for a Command
	// refused because its Entity is disabled.
	mcpFailureEntityDisabled = mcpFailureCode(devices.CommandFailureEntityDisabled)
	// mcpFailureAdapterUnhealthy is the durable Command failure code for a
	// Command refused because its Adapter is unhealthy.
	mcpFailureAdapterUnhealthy = mcpFailureCode(devices.CommandFailureAdapterUnhealthy)
	// mcpFailureEntityUnavailable is the durable Command failure code for a
	// Command refused because its Entity is unavailable.
	mcpFailureEntityUnavailable = mcpFailureCode(devices.CommandFailureEntityUnavailable)
	// mcpFailureUpstreamRejected is the durable Command failure code for an
	// Adapter that rejected the Command.
	mcpFailureUpstreamRejected = mcpFailureCode(devices.CommandFailureUpstreamRejected)
	// mcpFailureOutcomeTimeout is the durable Command failure code for a Command
	// whose outcome deadline expired.
	mcpFailureOutcomeTimeout = mcpFailureCode(devices.CommandFailureOutcomeTimeout)
	// mcpFailureCommandUnavailable reports closed Command admission.
	mcpFailureCommandUnavailable mcpFailureCode = "command_unavailable"
	// mcpFailureInternalError is the generic 500-class result: the agent learns
	// only that the call failed, and full detail stays in server logs. The code
	// is shared with the mcpapi wrapper, whose middleware logs the cause a
	// handler retained with mcpapi.ToolError.WithCause.
	mcpFailureInternalError = mcpFailureCode(mcpapi.CodeInternalError)
)

// Structured error field names. mcpapi attaches a ToolError's Code and Details
// as a structured isError result, so these keys are the machine-readable half of
// the failure contract.
const (
	mcpDetailStatus    = "status"
	mcpDetailCommandID = "command_id"
)

// mcpTerminalCommandStatuses are the two terminal Command statuses a successful
// execute_entity_command reports, mirroring the Huma response body.
const (
	mcpCommandStatusSatisfied  = "satisfied"
	mcpCommandStatusDispatched = "dispatched"
)

// mcpToolError builds one domain failure result. Message is the stable human
// summary; the failure code reaches clients as Code, which mcpapi renders as
// structured content beside the text.
func mcpToolError(code mcpFailureCode, message string) *mcpapi.ToolError {
	return &mcpapi.ToolError{Code: string(code), Message: message}
}

// mcpInternalFailure is the 500-class result with no retained cause: the agent
// learns only that the call failed, and the middleware logs nothing because
// there is no server-side detail to record.
func mcpInternalFailure() *mcpapi.ToolError {
	return mcpToolError(mcpFailureInternalError, "internal error")
}

// mcpInternalFailureCause is the 500-class result that also retains cause for
// the server-side diagnostic log. The client still sees only the generic
// message; mcpapi.ToolError.WithCause keeps cause off Error and structured
// content.
func mcpInternalFailureCause(cause error) *mcpapi.ToolError {
	return mcpInternalFailure().WithCause(cause)
}

// mcpInvalidRequestFailure reports client input the handler rejected.
func mcpInvalidRequestFailure(message string) *mcpapi.ToolError {
	return mcpToolError(mcpFailureInvalidRequest, message)
}

// mcpReadFailure translates one failed read into a ToolError.
//
// Reads reuse the shared Huma operation, so the HTTP status selects the failure
// class and the operation supplies the one missing-parent code it can report. A
// 404 without a code is an unpublished invariant and stays internal, and so
// does any non-Huma failure. A non-Huma failure is the read's own error and
// retains it directly. A Huma 500 also retains its cause: the shared operation
// builds it with [internalAPIError] from the service, response-mapping, or
// cursor-encoding failure it could not publish, so the MCP result logs the
// original cause instead of the generic problem that veiled it.
func mcpReadFailure(err error, missing mcpFailureCode) *mcpapi.ToolError {
	var status huma.StatusError
	if !errors.As(err, &status) {
		return mcpInternalFailureCause(err)
	}
	switch status.GetStatus() {
	case http.StatusNotFound:
		if missing == mcpFailureNone {
			return mcpInternalFailure()
		}
		return mcpToolError(missing, status.Error())
	case http.StatusBadRequest:
		return mcpInvalidRequestFailure(status.Error())
	default:
		return mcpInternalFailureCause(mcpReadInternalCause(err))
	}
}

// mcpReadInternalCause returns the server-side failure a shared read retained
// with [internalAPIError], or nil when the problem carries no cause.
//
// [mcpapi.ToolError.WithCause] with a nil cause leaves the generic internal
// result the client already branches on and gives the diagnostic log nothing to
// report, which is exactly the behavior of an internal failure that never
// retained a cause.
func mcpReadInternalCause(err error) error {
	problem, ok := errors.AsType[*internalProblemError](err)
	if !ok {
		return nil
	}
	return problem.cause
}

// mcpCommandFailure maps one failed execute_entity_command to the tool error the
// agent branches on. The failure code and the durable Command status cross the
// boundary; the HTTP status does not.
func mcpCommandFailure(err error) *mcpapi.ToolError {
	commandID := mcpFailedCommandID(err)
	switch {
	case errors.Is(err, devices.ErrEntityDisabled):
		return mcpCommandToolError(
			mcpFailureEntityDisabled, "entity is disabled", commandID, devices.CommandStatusEntityDisabled,
		)
	case errors.Is(err, devices.ErrCommandUnavailable):
		return mcpToolError(mcpFailureCommandUnavailable, "command admission is unavailable")
	case errors.Is(err, devices.ErrInvalidCommand):
		return mcpInvalidRequestFailure("invalid command")
	case errors.Is(err, devices.ErrEntityNotFound):
		return mcpToolError(mcpFailureEntityNotFound, "entity not found")
	case errors.Is(err, devices.ErrAdapterUnhealthy):
		return mcpCommandToolError(
			mcpFailureAdapterUnhealthy, "adapter unhealthy", commandID, devices.CommandStatusAdapterUnhealthy,
		)
	case errors.Is(err, devices.ErrEntityUnavailable):
		return mcpCommandToolError(
			mcpFailureEntityUnavailable, "entity unavailable", commandID, devices.CommandStatusEntityUnavailable,
		)
	case errors.Is(err, devices.ErrUpstreamRejected):
		return mcpCommandToolError(
			mcpFailureUpstreamRejected, "upstream rejected command", commandID, devices.CommandStatusRejected,
		)
	case errors.Is(err, devices.ErrOutcomeTimeout):
		return mcpCommandToolError(
			mcpFailureOutcomeTimeout, "command outcome timed out", commandID, devices.CommandStatusOutcomeTimeout,
		)
	default:
		return mcpInternalFailureCause(err)
	}
}

// mcpCommandToolError builds one Command failure result carrying the durable
// Command status, so the agent can correlate the failure code with the record
// get_command returns.
func mcpCommandToolError(
	code mcpFailureCode,
	message string,
	commandID string,
	status devices.CommandStatus,
) *mcpapi.ToolError {
	details := map[string]any{mcpDetailStatus: string(status)}
	if commandID != "" {
		details[mcpDetailCommandID] = commandID
	}
	return &mcpapi.ToolError{Code: string(code), Message: message, Details: details}
}

// mcpFailedCommandID returns the durable Command ID a failed execution was
// created for, or the empty string when the failure preceded any record.
func mcpFailedCommandID(err error) string {
	var executionError *devices.CommandExecutionError
	if !errors.As(err, &executionError) {
		return ""
	}
	return string(executionError.CommandID)
}

// mcpResourceInputError is an unreadable resource URI or query. It becomes a
// JSON-RPC invalid-params error, mirroring the Huma 400 for a malformed
// request.
type mcpResourceInputError struct {
	message string
}

// Error implements the error interface.
func (err *mcpResourceInputError) Error() string {
	return err.message
}

// mcpResourceNotFoundError reports a resource URI this server does not serve.
// Read dispatchers return it so one place translates it to the SDK's
// resource-not-found error.
type mcpResourceNotFoundError struct{}

// Error implements the error interface.
func (mcpResourceNotFoundError) Error() string {
	return "resource not found"
}

// mcpResourceFailure translates one failed resource read into the JSON-RPC error
// a client sees: a missing parent becomes resource-not-found, unreadable input
// becomes invalid params, and anything else stays internal without leaking
// detail.
func mcpResourceFailure(uri string, err error) error {
	if inputError, ok := errors.AsType[*mcpResourceInputError](err); ok {
		return mcpInvalidParamsError(inputError.message)
	}
	if _, ok := errors.AsType[mcpResourceNotFoundError](err); ok {
		return mcp.ResourceNotFoundError(uri)
	}
	if toolError, ok := errors.AsType[*mcpapi.ToolError](err); ok {
		switch toolError.Code {
		case string(mcpFailureEntityNotFound), string(mcpFailureDeviceNotFound),
			string(mcpFailureAdapterNotFound), string(mcpFailureCommandNotFound):
			return mcp.ResourceNotFoundError(uri)
		case string(mcpFailureInvalidRequest):
			return mcpInvalidParamsError(toolError.Message)
		}
	}
	return mcpResourceInternalError()
}

// mcpInvalidParamsError reports one unreadable resource request with the
// JSON-RPC invalid-params code.
func mcpInvalidParamsError(message string) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: message}
}

// mcpResourceInternalError reports a 500-class resource failure without leaking
// detail to the client.
func mcpResourceInternalError() error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "internal error"}
}
