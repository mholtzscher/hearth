package mcpapi

import (
	"errors"

	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func toolError(err error) error {
	switch {
	case errors.Is(err, notifications.ErrInvalidInput):
		return &mcptools.ToolError{Code: "invalid_input", Message: "invalid notification input"}
	case errors.Is(err, notifications.ErrDestinationNotFound), errors.Is(err, notifications.ErrAttemptNotFound):
		return &mcptools.ToolError{Code: "not_found", Message: "notification resource not found"}
	case errors.Is(err, notifications.ErrAdmissionClosed):
		return &mcptools.ToolError{Code: "admission_closed", Message: "notification admission closed"}
	default:
		return mcptools.InternalToolError(err)
	}
}
