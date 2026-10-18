package api

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

func apiError(status int, message string) error { return huma.NewError(status, message) }

// internalProblemError retains diagnostics without exposing a cause in a response.
type internalProblemError struct {
	problem huma.StatusError
	cause   error
}

func (problem *internalProblemError) Error() string { return problem.problem.Error() }
func (problem *internalProblemError) Unwrap() error { return problem.problem }

func internalAPIError(cause error) error {
	return &internalProblemError{problem: huma.NewError(http.StatusInternalServerError, "internal error"), cause: cause}
}

type mcpFailureCode string

const (
	mcpFailureNone             mcpFailureCode = ""
	mcpFailureInvalidRequest   mcpFailureCode = "invalid_request"
	mcpFailureAdapterNotFound  mcpFailureCode = "adapter_not_found"
	mcpFailureInvalidLimit     mcpFailureCode = "invalid_limit"
	mcpFailureInvalidAdapterID mcpFailureCode = "invalid_adapter_id"
)

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
		return &mcpapi.ToolError{Code: string(missing), Message: status.Error()}
	case http.StatusBadRequest:
		return &mcpapi.ToolError{Code: string(mcpFailureInvalidRequest), Message: status.Error()}
	default:
		problem, ok := errors.AsType[*internalProblemError](err)
		if !ok {
			return mcpapi.InternalToolError(nil)
		}
		return mcpapi.InternalToolError(problem.cause)
	}
}
