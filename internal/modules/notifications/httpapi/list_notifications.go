package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type AttemptListInput struct {
	PageInput

	DestinationID string `query:"destination_id"`
}
type AttemptListOutput struct{ Body AttemptCollectionBody }

type AttemptCollectionBody struct {
	Items      []AttemptBody `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

func registerListNotifications(api huma.API, h *handler) {
	huma.Register(api, huma.Operation{OperationID: "list-notifications", Method: http.MethodGet,
		Path: "/notifications", Summary: "List notification attempts", Tags: []string{notificationTag}}, h.listAttempts)
}

func (h *handler) listAttempts(ctx context.Context, in *AttemptListInput) (*AttemptListOutput, error) {
	page, err := h.service.ListAttempts(ctx, notifications.AttemptFilter{
		DestinationID: notifications.DestinationID(in.DestinationID), Limit: in.Limit, Cursor: in.Cursor,
	})
	if err != nil {
		return nil, mapError(err)
	}
	body := AttemptCollectionBody{
		Items:      make([]AttemptBody, 0, len(page.Items)),
		NextCursor: nextCursor(page.NextCursor),
	}
	for _, a := range page.Items {
		body.Items = append(body.Items, attemptBody(a))
	}
	return &AttemptListOutput{Body: body}, nil
}
