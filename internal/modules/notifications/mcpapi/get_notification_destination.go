package mcpapi

import (
	"context"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type GetDestinationInput struct {
	DestinationID string `json:"destination_id"`
}

func registerGetNotificationDestination(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[GetDestinationInput, Destination]{
		Name:        "get_notification_destination",
		Description: "Inspect a notification destination and its current availability",
		InputSchema: argumentSchema(map[string]any{"destination_id": stringSchema()}, "destination_id"),
		Handler:     h.getDestination,
	})
}

func (h *handler) getDestination(ctx context.Context, in GetDestinationInput) (Destination, error) {
	d, err := h.service.GetDestination(ctx, notifications.DestinationID(in.DestinationID))
	if err != nil {
		return Destination{}, toolError(err)
	}
	return destinationFromDomain(d), nil
}
