package mcpapi

import (
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func readFailure(err error) *mcptools.ToolError {
	switch {
	case errors.Is(err, devices.ErrEntityNotFound):
		return toolError(failureEntityNotFound, "entity not found")
	case errors.Is(err, devices.ErrDeviceNotFound):
		return toolError(failureDeviceNotFound, "device not found")
	case errors.Is(err, devices.ErrCommandNotFound):
		return toolError(failureCommandNotFound, "command not found")
	case errors.Is(err, devices.ErrInvalidPage):
		return invalidRequestFailure("invalid page")
	default:
		return mcptools.InternalToolError(err)
	}
}

// failureCode is the stable, machine-readable code an agent branches on when
// a Devices MCP Tool reports a domain failure. The code and a stable human
// summary cross the MCP boundary; the Hearth outcome's HTTP status does not,
// because MCP has no status codes. Command failure codes reuse the durable codes
// the Command record publishes, so the tool error and `get_command` describe one
// failure the same way.
type failureCode string

const (
	// failureInvalidRequest reports client input the handler rejected, such as
	// an opaque cursor this endpoint cannot interpret.
	failureInvalidRequest failureCode = "invalid_request"
	// failureEntityNotFound is the missing-Entity outcome of one read.
	failureEntityNotFound failureCode = "entity_not_found"
	// failureDeviceNotFound is the missing-Device outcome of one read.
	failureDeviceNotFound failureCode = "device_not_found"
	// failureCommandNotFound is the missing-Command outcome of one read.
	failureCommandNotFound failureCode = "command_not_found"
)

// Decode-time malformed-argument codes. The SDK rejects a malformed scalar
// before the handler runs and publishes no structured content, so the code must
// lead the error text for a client to branch on.
const (
	// failureInvalidLimit is a page size outside the accepted range.
	failureInvalidLimit failureCode = "invalid_limit"
	// failureInvalidEntityID is a malformed Entity ID argument.
	failureInvalidEntityID failureCode = "invalid_entity_id"
	// failureInvalidDeviceID is a malformed Device ID argument.
	failureInvalidDeviceID failureCode = "invalid_device_id"
	// failureInvalidCommandID is a malformed Command ID argument.
	failureInvalidCommandID failureCode = "invalid_command_id"
)

// toolError builds one domain failure result. Message is the stable human
// summary; the failure code reaches clients as Code, which mcpapi renders as
// structured content beside the text.
func toolError(code failureCode, message string) *mcptools.ToolError {
	return &mcptools.ToolError{Code: string(code), Message: message}
}

func invalidRequestFailure(message string) *mcptools.ToolError {
	return toolError(failureInvalidRequest, message)
}
