package sqlite

import (
	"github.com/mholtzscher/hearth/internal/modules/notifications"
	"github.com/mholtzscher/hearth/internal/modules/notifications/sqlite/dbsqlc"
)

// storedAttemptError keeps decoder causes available for diagnostics without
// exposing their input-validation identities or retained values to callers.
type storedAttemptError struct {
	cause error
}

func (*storedAttemptError) Error() string { return "corrupt stored notification attempt" }

func (*storedAttemptError) Unwrap() error { return notifications.ErrPersistence }

func (err *storedAttemptError) Cause() error { return err.cause }

// attemptFromRow relies on SQL for outcome/nullability and receipt column shape.
// Decode retained timestamps and terminal outcome meaning without reapplying
// current admission, route, message-size, receipt-size, or timeout policies.
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
	a.StartedAt, err = parseTime(row.StartedAt)
	if err != nil {
		return notifications.Attempt{}, &storedAttemptError{cause: err}
	}
	a.Deadline, err = parseTime(row.Deadline)
	if err != nil {
		return notifications.Attempt{}, &storedAttemptError{cause: err}
	}
	a.CompletedAt, err = parseOptionalTime(row.CompletedAt)
	if err != nil {
		return notifications.Attempt{}, &storedAttemptError{cause: err}
	}
	if a.CompletedAt != nil {
		// Receipt shape is enforced by SQL. Only outcome/reason interpretation
		// is needed here; retained receipt bytes are not new provider input.
		if err = notifications.ValidateEvidence(notifications.Evidence{
			Outcome: a.Outcome, ReasonCode: a.ReasonCode,
		}); err != nil {
			return notifications.Attempt{}, &storedAttemptError{cause: err}
		}
	}
	return a, nil
}
