package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func apiError(status int, message string) error {
	return huma.NewError(status, message)
}

// internalProblemError is the 500-class Huma problem a read returns for an
// unpublished internal failure, carrying the server-side cause the client must
// never receive.
//
// It renders exactly the generic problem huma.NewError(500, "internal error")
// renders: Unwrap exposes the wrapped problem, Huma finds it with [errors.As], and
// the problem document, content type, `$schema` link, and Link header are
// therefore unchanged. Error never renders the cause, and the cause is reachable
// only by a server-side caller that unwraps the problem explicitly, so no
// transport can leak it by logging the error.
type internalProblemError struct {
	problem huma.StatusError
	cause   error
}

// Error implements the error interface with the generic problem detail only.
func (problem *internalProblemError) Error() string { return problem.problem.Error() }

// Unwrap exposes the generic problem Huma renders, so a caller that classifies
// the failure with [errors.As] or [errors.Is] sees the problem it always saw.
func (problem *internalProblemError) Unwrap() error { return problem.problem }

// internalAPIError builds the generic 500 problem for one server-side failure
// while retaining cause for server diagnostics.
//
// A read reaches it with the failure it cannot publish: a service error, a
// response-mapping error, or a cursor-encoding error. The client still learns
// only that the call failed; the cause stays available to the MCP read wrapper,
// which logs it for the agent's operator without widening the response.
func internalAPIError(cause error) error {
	return &internalProblemError{
		problem: huma.NewError(http.StatusInternalServerError, "internal error"),
		cause:   cause,
	}
}

type disabledCommandError struct {
	Type      string `json:"type"       format:"uri" default:"about:blank"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      string `json:"code"`
	CommandID string `json:"command_id"`
}

func (problem *disabledCommandError) Error() string  { return problem.Detail }
func (problem *disabledCommandError) GetStatus() int { return problem.Status }
func (problem *disabledCommandError) ContentType(contentType string) string {
	if contentType == "application/json" {
		return "application/problem+json"
	}
	return contentType
}

func entityDisabledProblem(commandID devices.CommandID) error {
	return &disabledCommandError{
		Type: "about:blank", Title: http.StatusText(http.StatusConflict), Status: http.StatusConflict,
		Detail: "entity is disabled", Code: "entity_disabled", CommandID: string(commandID),
	}
}
