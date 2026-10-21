package mcpapi

import "github.com/mholtzscher/hearth/internal/modules/notifications"

func destinationFromDomain(d notifications.Destination) Destination {
	return Destination{DestinationID: string(d.ID), Name: d.Name, Available: d.Available}
}
