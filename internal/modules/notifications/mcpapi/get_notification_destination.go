//nolint:gochecknoglobals // Fixed contracts are immutable process-lifetime preparation.
package mcpapi

import (
	"context"
	"sync"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type GetDestinationInput struct {
	DestinationID string `json:"destination_id"`
}

var prepareGetNotificationDestinationInput = sync.OnceValues(
	func() (*mcptools.InputContract[GetDestinationInput], error) {
		return mcptools.PrepareInput[GetDestinationInput](
			argumentSchema(map[string]any{"destination_id": stringSchema()}, "destination_id"),
		)
	},
)

func registerGetNotificationDestination(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[GetDestinationInput, Destination]{
		Name:        "get_notification_destination",
		Description: "Inspect a notification destination and its current availability",
		Input:       mustContract(prepareGetNotificationDestinationInput()),
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
