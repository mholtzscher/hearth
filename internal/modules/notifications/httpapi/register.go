// Package httpapi owns the notification HTTP contract.
package httpapi

import "github.com/danielgtaylor/huma/v2"

const notificationTag = "Notifications"

func Register(api huma.API, service Notifications) {
	h := &handler{service: service}
	registerListNotificationDestinations(api, h)
	registerGetNotificationDestination(api, h)
	registerSendNotification(api, h)
	registerListNotifications(api, h)
	registerGetNotification(api, h)
}
