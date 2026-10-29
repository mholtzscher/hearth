package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func (h *handler) internalAPIError(ctx context.Context, operation, stage string, cause error) error {
	h.logInternalFailure(ctx, operation, stage, cause)
	return huma.NewError(http.StatusInternalServerError, "internal error")
}

func (h *handler) logInternalFailure(ctx context.Context, operation, stage string, cause error) {
	if expectedRequestCancellation(ctx, cause) {
		return
	}
	h.logger.ErrorContext(ctx, "adapter HTTP operation failed",
		slog.String("event", "adapter.http_internal_failure"),
		slog.String("operation", operation), slog.String("stage", stage),
		slog.String("error_code", "internal_error"), slog.String("error_type", fmt.Sprintf("%T", cause)),
	)
}

func expectedRequestCancellation(ctx context.Context, err error) bool {
	ownerErr := ctx.Err()
	if ownerErr == nil {
		return false
	}
	for err != nil {
		if err == ownerErr { //nolint:errorlint // Require the exact owner sentinel in a single-cause chain.
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
