package httpapi

import (
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func mapError(err error) error {
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
		return huma.Error500InternalServerError("internal error")
	}
}
