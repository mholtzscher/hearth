package api

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// mcpFailureCode is the stable, machine-readable code an agent branches on when
// a Devices MCP Tool reports a domain failure. The code and a stable human
// summary cross the MCP boundary; the Hearth outcome's HTTP status does not,
// because MCP has no status codes. Command failure codes reuse the durable codes
// the Command record publishes, so the tool error and `get_command` describe one
// failure the same way.
type mcpFailureCode string

const (
	// mcpFailureNone marks a read that has no missing-parent outcome.
	mcpFailureNone mcpFailureCode = ""
	// mcpFailureInvalidRequest reports client input the handler rejected, such as
	// an opaque cursor this endpoint cannot interpret.
	mcpFailureInvalidRequest mcpFailureCode = "invalid_request"
	// mcpFailureEntityNotFound is the missing-Entity outcome of one read.
	mcpFailureEntityNotFound mcpFailureCode = "entity_not_found"
	// mcpFailureDeviceNotFound is the missing-Device outcome of one read.
	mcpFailureDeviceNotFound mcpFailureCode = "device_not_found"
	// mcpFailureAdapterNotFound is the missing-Adapter outcome of one read.
	mcpFailureAdapterNotFound mcpFailureCode = "adapter_not_found"
	// mcpFailureCommandNotFound is the missing-Command outcome of one read.
	mcpFailureCommandNotFound mcpFailureCode = "command_not_found"
	// mcpFailureEntityDisabled is the durable code for a disabled Entity.
	mcpFailureEntityDisabled = mcpFailureCode(devices.CommandFailureEntityDisabled)
	// mcpFailureAdapterUnhealthy is the durable code for an unhealthy Adapter.
	mcpFailureAdapterUnhealthy = mcpFailureCode(devices.CommandFailureAdapterUnhealthy)
	// mcpFailureEntityUnavailable is the durable code for an unavailable Entity.
	mcpFailureEntityUnavailable = mcpFailureCode(devices.CommandFailureEntityUnavailable)
	// mcpFailureUpstreamRejected is the durable code for an Adapter rejection.
	mcpFailureUpstreamRejected = mcpFailureCode(devices.CommandFailureUpstreamRejected)
	// mcpFailureOutcomeTimeout is the durable code for an expired outcome deadline.
	mcpFailureOutcomeTimeout = mcpFailureCode(devices.CommandFailureOutcomeTimeout)
	// mcpFailureCommandUnavailable reports closed Command admission.
	mcpFailureCommandUnavailable mcpFailureCode = "command_unavailable"
)

// Decode-time malformed-argument codes. The SDK rejects a malformed scalar
// before the handler runs and publishes no structured content, so the code must
// lead the error text for a client to branch on.
const (
	// mcpFailureInvalidLimit is a page size outside the accepted range.
	mcpFailureInvalidLimit mcpFailureCode = "invalid_limit"
	// mcpFailureInvalidEntityID is a malformed Entity ID argument.
	mcpFailureInvalidEntityID mcpFailureCode = "invalid_entity_id"
	// mcpFailureInvalidDeviceID is a malformed Device ID argument.
	mcpFailureInvalidDeviceID mcpFailureCode = "invalid_device_id"
	// mcpFailureInvalidAdapterID is a malformed Adapter ID argument.
	mcpFailureInvalidAdapterID mcpFailureCode = "invalid_adapter_id"
	// mcpFailureInvalidCommandID is a malformed Command ID argument.
	mcpFailureInvalidCommandID mcpFailureCode = "invalid_command_id"
)

// Structured error field names mcpapi attaches to a ToolError's structured
// isError result, so these keys are the machine-readable half of the failure
// contract.
const (
	mcpDetailStatus    = "status"
	mcpDetailCommandID = "command_id"
)

// mcpToolError builds one domain failure result. Message is the stable human
// summary; the failure code reaches clients as Code, which mcpapi renders as
// structured content beside the text.
func mcpToolError(code mcpFailureCode, message string) *mcpapi.ToolError {
	return &mcpapi.ToolError{Code: string(code), Message: message}
}

func mcpInvalidRequestFailure(message string) *mcpapi.ToolError {
	return mcpToolError(mcpFailureInvalidRequest, message)
}

// mcpReadFailure translates one failed read into a ToolError. Reads reuse the
// shared Huma operation, so the HTTP status selects the failure class and the
// operation supplies the one missing-parent code it can report. A 404 without a
// code is an unpublished invariant and stays internal, and so does any non-Huma
// failure; both retain their cause so the MCP result logs the original cause
// instead of the generic problem that veiled it.
func mcpReadFailure(err error, missing mcpFailureCode) *mcpapi.ToolError {
	var status huma.StatusError
	if !errors.As(err, &status) {
		return mcpapi.InternalToolError(err)
	}
	switch status.GetStatus() {
	case http.StatusNotFound:
		if missing == mcpFailureNone {
			return mcpapi.InternalToolError(nil)
		}
		return mcpToolError(missing, status.Error())
	case http.StatusBadRequest:
		return mcpInvalidRequestFailure(status.Error())
	default:
		return mcpapi.InternalToolError(mcpReadInternalCause(err))
	}
}

// mcpReadInternalCause returns the server-side failure a shared read retained
// with [internalAPIError], or nil when the problem carries no cause. A nil cause
// leaves the generic internal result the client already branches on and gives
// the diagnostic log nothing to report.
func mcpReadInternalCause(err error) error {
	problem, ok := errors.AsType[*internalProblemError](err)
	if !ok {
		return nil
	}
	return problem.cause
}

// mcpCommandFailure maps one failed execute_entity_command to the tool error the
// agent branches on, reading the same domain classification the HTTP route maps.
// The failure code and the durable Command status cross the boundary; the HTTP
// status does not.
func mcpCommandFailure(err error) *mcpapi.ToolError {
	failure, classified := classifyCommandFailure(err)
	if !classified {
		return mcpapi.InternalToolError(err)
	}
	if failure.durableStatus == "" {
		return mcpToolError(failure.code, failure.message)
	}
	return mcpCommandToolError(failure.code, failure.message, mcpFailedCommandID(err), failure.durableStatus)
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

func mcpFailedCommandID(err error) string {
	var executionError *devices.CommandExecutionError
	if !errors.As(err, &executionError) {
		return ""
	}
	return string(executionError.CommandID)
}
