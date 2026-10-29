package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

type DestinationInput struct {
	ID string `path:"id"`
}
type DestinationOutput struct{ Body DestinationBody }

func registerGetNotificationDestination(api huma.API, h *handler) {
	huma.Register(api, huma.Operation{
		OperationID: "get-notification-destination",
		Method:      http.MethodGet,
		Path:        "/notification-destinations/{id}",
		Summary:     "Get a notification destination",
		Tags:        []string{notificationTag},
	}, h.getDestination)
}

func (h *handler) getDestination(ctx context.Context, in *DestinationInput) (*DestinationOutput, error) {
	d, err := h.service.GetDestination(ctx, notifications.DestinationID(in.ID))
	if err != nil {
		return nil, h.mapError(ctx, "get-notification-destination", err)
	}
	return &DestinationOutput{Body: destinationBody(d)}, nil
}
