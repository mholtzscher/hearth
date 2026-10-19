package api

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

const functionCatalogChangedCode = "catalog_changed"
const functionInternalErrorMessage = "internal error"

func functionFailure(err error) (int, string, string) {
	switch {
	case errors.Is(err, adapters.ErrFunctionPersistence):
		return http.StatusInternalServerError, "internal_error", functionInternalErrorMessage
	case errors.Is(err, adapters.ErrInvalidFunctionInput):
		return http.StatusUnprocessableEntity, "invalid_input", "invalid function input"
	case errors.Is(err, adapters.ErrAdapterNotFound):
		return http.StatusNotFound, "adapter_not_found", "adapter not found"
	case errors.Is(err, adapters.ErrFunctionNotFound):
		return http.StatusNotFound, "function_not_found", "function not found"
	case errors.Is(err, adapters.ErrInvocationNotFound):
		return http.StatusNotFound, "invocation_not_found", "function invocation not found"
	case errors.Is(err, adapters.ErrCatalogChanged):
		return http.StatusConflict, functionCatalogChangedCode, functionCatalogChangedCode
	case errors.Is(err, adapters.ErrCatalogConflict):
		return http.StatusConflict, "catalog_conflict", "catalog_conflict"
	case errors.Is(err, adapters.ErrAdapterUnavailable):
		return http.StatusServiceUnavailable, "adapter_unavailable", "adapter_unavailable"
	case errors.Is(err, adapters.ErrFunctionAdmissionClosed):
		return http.StatusServiceUnavailable, "admission_closed", "admission_closed"
	default:
		return http.StatusInternalServerError, "internal_error", functionInternalErrorMessage
	}
}

type functionProblemError struct {
	huma.ErrorModel

	Code         string `json:"code,omitempty"`
	InvocationID string `json:"invocation_id,omitempty"`
	cause        error
}

func (problem *functionProblemError) Unwrap() error { return problem.cause }

func functionHTTPError(err error) error {
	status, code, message := functionFailure(err)
	model := huma.ErrorModel{Status: status, Title: http.StatusText(status), Detail: message}
	problem := &functionProblemError{
		ErrorModel: model,
		Code:       code,
		cause:      err,
	}
	if status == http.StatusInternalServerError {
		if invocationError, ok := errors.AsType[*adapters.InvocationError](err); ok {
			problem.InvocationID = string(invocationError.ID)
		}
	}
	return problem
}

func functionToolError(err error) error {
	status, code, message := functionFailure(err)
	failure := &mcpapi.ToolError{Code: code, Message: message}
	if status == http.StatusInternalServerError {
		failure = failure.WithCause(err)
		if invocationError, ok := errors.AsType[*adapters.InvocationError](err); ok {
			failure.Details = map[string]any{"invocation_id": string(invocationError.ID)}
		}
	}
	return failure
}
