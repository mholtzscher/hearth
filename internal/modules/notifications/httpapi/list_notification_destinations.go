package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type DestinationListOutput struct{ Body DestinationCollectionBody }

type DestinationCollectionBody struct {
	Items      []DestinationBody `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

func registerListNotificationDestinations(api huma.API, h *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "list-notification-destinations", Method: http.MethodGet,
		Path: "/notification-destinations", Summary: "List notification destinations", Tags: []string{notificationTag},
	}, h.listDestinations)
}

func (h *handler) listDestinations(ctx context.Context, in *PageInput) (*DestinationListOutput, error) {
	page, err := h.service.ListDestinations(ctx, in.Limit, in.Cursor)
	if err != nil {
		return nil, mapError(err)
	}
	body := DestinationCollectionBody{
		Items:      make([]DestinationBody, 0, len(page.Items)),
		NextCursor: nextCursor(page.NextCursor),
	}
	for _, d := range page.Items {
		body.Items = append(body.Items, destinationBody(d))
	}
	return &DestinationListOutput{Body: body}, nil
}
