package sqlite

import (
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	"github.com/mholtzscher/hearth/internal/modules/notifications/sqlite/dbsqlc"
)

// The public projection needs a canonical identity and usable display name.
// SQL derives availability; route integrity is checked separately at admission.
func destinationFromRow(row dbsqlc.GetDestinationRow) (notifications.Destination, error) {
	if err := notifications.ValidateID(row.ID, "ndst_"); err != nil {
		return notifications.Destination{}, &storedDestinationError{cause: err}
	}
	if _, err := notifications.PrepareDestinationName(row.Name); err != nil {
		return notifications.Destination{}, &storedDestinationError{cause: err}
	}
	return notifications.Destination{
		ID:        notifications.DestinationID(row.ID),
		Name:      row.Name,
		Available: row.Available != 0,
	}, nil
}

// Admission consumes the full retained route, not just public display metadata.
// Validate it before recording an Attempt that can reach dispatch.
func destinationRouteFromRow(row dbsqlc.GetDestinationRow) (notifications.RouteSnapshot, error) {
	if _, err := notifications.PrepareRegistration(notifications.Registration{
		AdapterID: row.AdapterID, RuntimeID: row.RuntimeID, Key: row.LocalKey, Name: row.Name,
	}); err != nil {
		return notifications.RouteSnapshot{}, &storedDestinationError{cause: err}
	}
	return notifications.RouteSnapshot{
		AdapterID: row.AdapterID, LocalKey: row.LocalKey, RuntimeID: row.RuntimeID,
	}, nil
}

type storedDestinationError struct{ cause error }

func (*storedDestinationError) Error() string { return "corrupt stored notification destination" }

func (*storedDestinationError) Unwrap() error { return notifications.ErrPersistence }

func (err *storedDestinationError) Cause() error { return err.cause }
