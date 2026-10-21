package httpapi

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
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
