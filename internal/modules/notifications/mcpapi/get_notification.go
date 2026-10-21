//nolint:gochecknoglobals // Fixed contracts are immutable process-lifetime preparation.
package mcpapi

import (
	"context"
	"sync"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
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

var prepareGetNotificationInput = sync.OnceValues(func() (*mcptools.InputContract[GetNotificationInput], error) {
	return mcptools.PrepareInput[GetNotificationInput](argumentSchema(map[string]any{"id": stringSchema()}, "id"))
})

func registerGetNotification(server *mcptools.Server, h *handler) {
	mcptools.Register(server, mcptools.Tool[GetNotificationInput, NotificationDetail]{
		Name:        "get_notification",
		Description: "Inspect retained notification evidence by id from a receipt; accepted is provider acceptance, not verified delivery",
		Input:       mustContract(prepareGetNotificationInput()),
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
