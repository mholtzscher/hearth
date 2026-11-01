package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/dashboards"
)

func (h *handler) mapError(ctx context.Context, operation string, err error) error {
	switch {
	case errors.Is(err, dashboards.ErrCorruptDashboard):
		return h.internalError(ctx, operation, err)
	case errors.Is(err, dashboards.ErrInvalidDefinition):
		return huma.Error400BadRequest("invalid dashboard input")
	case errors.Is(err, dashboards.ErrNotFound):
		return huma.Error404NotFound("dashboard not found")
	case errors.Is(err, dashboards.ErrRevisionConflict):
		return huma.Error409Conflict("dashboard revision conflict")
	default:
		return h.internalError(ctx, operation, err)
	}
}

func (h *handler) internalError(ctx context.Context, operation string, err error) error {
	if !expectedRequestCancellation(ctx, err) {
		h.logger.ErrorContext(
			ctx,
			"dashboard HTTP operation failed",
			slog.String("event", "dashboard.http_internal_failure"),
			slog.String("operation", operation),
			slog.String("stage", "service"),
			slog.String("error_code", "internal_error"),
			slog.String("error_type", fmt.Sprintf("%T", err)),
		)
	}
	return huma.Error500InternalServerError("internal server error")
}

func expectedRequestCancellation(ctx context.Context, err error) bool {
	ownerErr := ctx.Err()
	if ownerErr == nil {
		return false
	}
	for err != nil {
		if err == ownerErr { //nolint:errorlint // Only an exact owner cancellation in a single-cause chain is expected.
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
