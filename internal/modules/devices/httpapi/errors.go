package httpapi

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func apiError(status int, message string) error {
	return huma.NewError(status, message)
}

// internalAPIError deliberately excludes the private cause from Huma's problem.
func internalAPIError(_ error) error {
	return huma.NewError(http.StatusInternalServerError, "internal error")
}
