package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type AttemptInput struct {
	ID string `path:"id"`
}
type AttemptOutput struct{ Body AttemptBody }

func registerGetNotification(api huma.API, h *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-notification", Method: http.MethodGet, Path: "/notifications/{id}",
		Summary: "Get a notification attempt", Tags: []string{notificationTag},
	}, h.getAttempt)
}

func (h *handler) getAttempt(ctx context.Context, in *AttemptInput) (*AttemptOutput, error) {
	a, err := h.service.GetAttempt(ctx, notifications.AttemptID(in.ID))
	if err != nil {
		return nil, h.mapError(ctx, "get-notification", err)
	}
	return &AttemptOutput{Body: attemptBody(a)}, nil
}
