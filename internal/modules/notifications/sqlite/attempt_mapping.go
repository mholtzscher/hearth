package sqlite

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
	"github.com/mholtzscher/hearth/internal/modules/notifications/sqlite/dbsqlc"
)

func attemptFromRow(row dbsqlc.NotificationAttempt) (notifications.Attempt, error) {
	a := notifications.Attempt{
		ID:              notifications.AttemptID(row.ID),
		DestinationID:   notifications.DestinationID(row.DestinationID),
		DestinationName: row.DestinationName,
		Route: notifications.RouteSnapshot{
			AdapterID: row.AdapterID,
			LocalKey:  row.LocalKey,
			RuntimeID: row.RuntimeID,
		},
		Message:    notifications.Message{Title: row.Title, Text: row.Message},
		Outcome:    notifications.Outcome(row.Outcome),
		ReasonCode: row.ReasonCode,
		Receipt:    row.Receipt,
	}
	var err error
	a.StartedAt, err = time.Parse(time.RFC3339Nano, row.StartedAt)
	if err != nil {
		return a, err
	}
	a.Deadline, err = time.Parse(time.RFC3339Nano, row.Deadline)
	if err != nil {
		return a, err
	}
	if row.CompletedAt.Valid {
		at, parseErr := time.Parse(time.RFC3339Nano, row.CompletedAt.String)
		if parseErr != nil {
			return a, parseErr
		}
		a.CompletedAt = &at
	}
	return a, nil
}
