package sqlite

import (
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	"github.com/mholtzscher/hearth/internal/modules/notifications/sqlite/dbsqlc"
)

// SQL supplies non-null routing columns but does not establish canonical
// identities or descriptor bounds. Validate them before reads or admission.
func destinationFromRow(row dbsqlc.GetDestinationRow) (notifications.Destination, error) {
	if err := notifications.ValidateID(row.ID, "ndst_"); err != nil {
		return notifications.Destination{}, &storedDestinationError{cause: err}
	}
	if _, err := notifications.PrepareRegistration(notifications.Registration{
		AdapterID: row.AdapterID, RuntimeID: row.RuntimeID, Key: row.LocalKey, Name: row.Name,
	}); err != nil {
		return notifications.Destination{}, &storedDestinationError{cause: err}
	}
	return notifications.Destination{
		ID:        notifications.DestinationID(row.ID),
		Name:      row.Name,
		Available: row.Available != 0,
	}, nil
}

type storedDestinationError struct{ cause error }

func (*storedDestinationError) Error() string { return "corrupt stored notification destination" }

func (*storedDestinationError) Unwrap() error { return notifications.ErrPersistence }

func (err *storedDestinationError) Cause() error { return err.cause }
