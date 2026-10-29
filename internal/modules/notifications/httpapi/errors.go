package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func (h *handler) mapError(ctx context.Context, operation string, err error) error {
	switch {
	case errors.Is(err, notifications.ErrInvalidInput):
		return huma.Error422UnprocessableEntity("invalid notification input")
	case errors.Is(err, notifications.ErrDestinationNotFound):
		return huma.Error404NotFound("notification destination not found")
	case errors.Is(err, notifications.ErrAttemptNotFound):
		return huma.Error404NotFound("notification attempt not found")
	case errors.Is(err, notifications.ErrAdmissionClosed):
		return huma.Error503ServiceUnavailable("notification admission closed")
	default:
		// Admitted sends outlive the caller. A joined persistence failure is
		// still diagnostic evidence even when it also matches caller cancellation.
		if errors.Is(err, notifications.ErrPersistence) || ctx.Err() == nil || !errors.Is(err, ctx.Err()) {
			h.logger.ErrorContext(ctx, "notification HTTP operation failed",
				slog.String("event", "notification.http_internal_failure"),
				slog.String("operation", operation),
				slog.String("error_code", "internal_error"),
				slog.String("error_type", fmt.Sprintf("%T", err)),
			)
		}
		return huma.Error500InternalServerError("internal error")
	}
}
