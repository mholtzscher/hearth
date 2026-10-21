//nolint:gochecknoglobals // Fixed contracts are immutable process-lifetime preparation.
package mcpapi

import (
	"context"
	"sync"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type ListNotificationsInput struct {
	Limit         int    `json:"limit,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	DestinationID string `json:"destination_id,omitempty"`
}
type NotificationPage struct {
	Items      []NotificationSummary `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

var prepareListNotificationsInput = sync.OnceValues(func() (*mcptools.InputContract[ListNotificationsInput], error) {
	properties := pageProperties()
	properties["destination_id"] = stringSchema()
	return mcptools.PrepareInput[ListNotificationsInput](argumentSchema(properties))
})

func registerListNotifications(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[ListNotificationsInput, NotificationPage]{
		Name:        "list_notifications",
		Description: "List compact notification history; use get_notification with id=notification_id for message and receipt",
		Input:       mustContract(prepareListNotificationsInput()),
		Handler:     h.listNotifications,
	})
}

func (h *handler) listNotifications(ctx context.Context, in ListNotificationsInput) (NotificationPage, error) {
	page, err := h.service.ListAttempts(ctx, notifications.AttemptFilter{
		DestinationID: notifications.DestinationID(in.DestinationID), Limit: pageLimit(in.Limit), Cursor: in.Cursor,
	})
	if err != nil {
		return NotificationPage{}, toolError(err)
	}
	result := NotificationPage{Items: make([]NotificationSummary, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, a := range page.Items {
		result.Items = append(result.Items, notificationSummary(a))
	}
	return result, nil
}
