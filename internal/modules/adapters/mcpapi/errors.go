//nolint:goconst // Literal codes and messages keep the safe domain failure matrix readable.
package mcpapi

import (
	"errors"

	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type failureCode string

const (
	failureInvalidLimit     failureCode = "invalid_limit"
	failureInvalidAdapterID failureCode = "invalid_adapter_id"
)

func managementFailure(err error) error {
	switch {
	case errors.Is(err, adapters.ErrAdapterNotFound):
		return &mcptools.ToolError{Code: "adapter_not_found", Message: "adapter not found"}
	case errors.Is(err, adapters.ErrInvalidPage):
		return invalidRequest("invalid page")
	default:
		return mcptools.InternalToolError(err)
	}
}

func invalidRequest(message string) error {
	return &mcptools.ToolError{Code: "invalid_request", Message: message}
}

func functionToolError(err error) error {
	code, message := functionFailure(err)
	failure := &mcptools.ToolError{Code: code, Message: message}
	if code == "internal_error" {
		failure = failure.WithCause(err)
		if invocationError, ok := errors.AsType[*adapters.InvocationError](err); ok {
			failure.Details = map[string]any{"invocation_id": string(invocationError.ID)}
		}
	}
	return failure
}

func functionFailure(err error) (string, string) {
	switch {
	case errors.Is(err, adapters.ErrFunctionPersistence):
		return "internal_error", "internal error"
	case errors.Is(err, adapters.ErrInvalidFunctionInput):
		return "invalid_input", "invalid function input"
	case errors.Is(err, adapters.ErrAdapterNotFound):
		return "adapter_not_found", "adapter not found"
	case errors.Is(err, adapters.ErrFunctionNotFound):
		return "function_not_found", "function not found"
	case errors.Is(err, adapters.ErrInvocationNotFound):
		return "invocation_not_found", "function invocation not found"
	case errors.Is(err, adapters.ErrCatalogChanged):
		return "catalog_changed", "catalog_changed"
	case errors.Is(err, adapters.ErrCatalogConflict):
		return "catalog_conflict", "catalog_conflict"
	case errors.Is(err, adapters.ErrAdapterUnavailable):
		return "adapter_unavailable", "adapter_unavailable"
	case errors.Is(err, adapters.ErrFunctionAdmissionClosed):
		return "admission_closed", "admission_closed"
	default:
		return "internal_error", "internal error"
	}
}
