package mcpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type ListNotificationsInput struct {
	Limit         int    `json:"limit,omitempty"          minimum:"1" maximum:"200"`
	Cursor        string `json:"cursor,omitempty"`
	DestinationID string `json:"destination_id,omitempty"`
}

func (ListNotificationsInput) TransformSchema(_ huma.Registry, schema *huma.Schema) *huma.Schema {
	// Preserve the published list document rather than omitting required.
	schema.Extensions = map[string]any{"required": nil}
	return schema
}

type NotificationPage struct {
	Items      []NotificationSummary `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

func registerListNotifications(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[ListNotificationsInput, NotificationPage]{
		Name:        "list_notifications",
		Description: "List compact notification history; use get_notification with id=notification_id for message and receipt",
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
