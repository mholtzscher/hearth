package mcpapi

import (
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// updateFailure maps a metadata write. The domain rejects a name override,
// Area, or tag set after the schema accepts them. An unknown Label is reported
// before the general invalid-patch outcome it also wraps.
func updateFailure(err error) *mcptools.ToolError {
	switch {
	case errors.Is(err, devices.ErrLabelNotFound):
		return toolError(failureLabelNotFound, "label not found")
	case errors.Is(err, devices.ErrInvalidMetadataPatch):
		return invalidRequestFailure(
			"name_override must be 1 to 128 code points after trimming and contain no control characters; " +
				"area_id must name an Area; tag_ids must name at most 32 tags",
		)
	}
	return readFailure(err)
}

// labelFailure maps a Label catalog operation.
func labelFailure(err error) *mcptools.ToolError {
	switch {
	case errors.Is(err, devices.ErrInvalidLabel):
		return toolError(failureInvalidLabel, "label name must be 1 to 128 code points after trimming, "+
			"contain no control characters, and the kind must be area or tag")
	case errors.Is(err, devices.ErrLabelNotFound):
		return toolError(failureLabelNotFound, "label not found")
	case errors.Is(err, devices.ErrLabelNameConflict):
		return toolError(failureLabelNameConflict, "another label already has this name")
	case errors.Is(err, devices.ErrLabelLimitReached):
		return toolError(failureLabelLimitReached, "the household label limit is reached")
	}
	return mcptools.InternalToolError(err)
}

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
	// failureLabelNotFound is a Label the request names that does not exist.
	failureLabelNotFound failureCode = "label_not_found"
	// failureInvalidLabel is a Label name or kind the domain rejects.
	failureInvalidLabel failureCode = "invalid_label"
	// failureLabelNameConflict is a Label name another Label already uses.
	failureLabelNameConflict failureCode = "label_name_conflict"
	// failureLabelLimitReached is a create that would exceed the Label catalog bound.
	failureLabelLimitReached failureCode = "label_limit_reached"
	// failureInvalidSelector is a malformed Entity selector.
	failureInvalidSelector failureCode = "invalid_selector"
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
	// failureInvalidLabelID is a malformed Label ID argument.
	failureInvalidLabelID failureCode = "invalid_label_id"
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
