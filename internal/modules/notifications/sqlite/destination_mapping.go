package sqlite

import (
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	"github.com/mholtzscher/hearth/internal/modules/notifications/sqlite/dbsqlc"
)

func destinationFromRow(row dbsqlc.GetDestinationRow) notifications.Destination {
	return notifications.Destination{
		ID:        notifications.DestinationID(row.ID),
		Name:      row.Name,
		Available: row.Available != 0,
	}
}
