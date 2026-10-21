package httpapi

import "github.com/mholtzscher/hearth/internal/modules/notifications"

func destinationBody(d notifications.Destination) DestinationBody {
	return DestinationBody{ID: string(d.ID), Name: d.Name, Available: d.Available}
}
