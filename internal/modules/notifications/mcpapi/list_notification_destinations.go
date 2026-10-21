//nolint:gochecknoglobals // Fixed contracts are immutable process-lifetime preparation.
package mcpapi

import (
	"context"
	"sync"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type ListDestinationsInput struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}
type DestinationPage struct {
	Items      []Destination `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

var prepareListNotificationDestinationsInput = sync.OnceValues(
	func() (*mcptools.InputContract[ListDestinationsInput], error) {
		return mcptools.PrepareInput[ListDestinationsInput](argumentSchema(pageProperties()))
	},
)

func registerListNotificationDestinations(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[ListDestinationsInput, DestinationPage]{
		Name:        "list_notification_destinations",
		Description: "List notification destinations; inspect a destination before sending",
		Input:       mustContract(prepareListNotificationDestinationsInput()),
		Handler:     h.listDestinations,
	})
}

func (h *handler) listDestinations(ctx context.Context, in ListDestinationsInput) (DestinationPage, error) {
	page, err := h.service.ListDestinations(ctx, pageLimit(in.Limit), in.Cursor)
	if err != nil {
		return DestinationPage{}, toolError(err)
	}
	result := DestinationPage{Items: make([]Destination, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, d := range page.Items {
		result.Items = append(result.Items, destinationFromDomain(d))
	}
	return result, nil
}
