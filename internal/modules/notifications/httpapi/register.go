// Package httpapi owns the notification HTTP contract.
package httpapi

import (
	"log/slog"

	"github.com/danielgtaylor/huma/v2"
)

const notificationTag = "Notifications"

// Register publishes the Notification HTTP surface. A nil logger discards
// diagnostics; production assembly supplies the component-scoped logger.
func Register(api huma.API, service Notifications, logger *slog.Logger) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := &handler{service: service, logger: logger}
	registerListNotificationDestinations(api, h)
	registerGetNotificationDestination(api, h)
	registerSendNotification(api, h)
	registerListNotifications(api, h)
	registerGetNotification(api, h)
}
