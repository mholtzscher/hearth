package mcpapi

import (
	"context"
	"time"

	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// GetNotificationInput retains the existing id argument, not notification_id.
type GetNotificationInput struct {
	ID string `json:"id"`
}
type NotificationDetail struct {
	NotificationSummary

	Message  string    `json:"message"`
	Deadline time.Time `json:"deadline"`
	Receipt  string    `json:"receipt,omitempty"`
}

func registerGetNotification(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[GetNotificationInput, NotificationDetail]{
		Name:        "get_notification",
		Description: "Inspect retained notification evidence by id from a receipt; accepted is provider acceptance, not verified delivery",
		ExactOutput: true,
		InputSchema: argumentSchema(map[string]any{"id": stringSchema()}, "id"),
		Handler:     h.getNotification,
	})
}

func (h *handler) getNotification(ctx context.Context, in GetNotificationInput) (NotificationDetail, error) {
	a, err := h.service.GetAttempt(ctx, notifications.AttemptID(in.ID))
	if err != nil {
		return NotificationDetail{}, toolError(err)
	}
	return NotificationDetail{
		NotificationSummary: notificationSummary(a),
		Message:             a.Message.Text,
		Deadline:            a.Deadline.UTC(),
		Receipt:             a.Receipt,
	}, nil
}
