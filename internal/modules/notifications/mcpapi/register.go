// Package mcpapi owns notification tool arguments and result projections.
package mcpapi

import mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"

func Register(server *mcptools.Server, service Notifications) {
	h := &handler{service: service}
	registerListNotificationDestinations(server, h)
	registerGetNotificationDestination(server, h)
	registerSendNotification(server, h)
	registerListNotifications(server, h)
	registerGetNotification(server, h)
}
