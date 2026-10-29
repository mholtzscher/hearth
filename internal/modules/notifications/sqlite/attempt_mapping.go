package sqlite

import (
	"errors"

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

// attemptFromRow relies on SQL for the outcome/nullability column shape. Domain
// parsers establish identity, descriptor/message bounds, deadline agreement, and
// terminal evidence meaning. Retained wall times need not establish chronology.
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
	if err = notifications.ValidateSend(notifications.SendInput{
		AttemptID: a.ID, DestinationID: a.DestinationID, Message: a.Message,
	}); err != nil {
		return notifications.Attempt{}, &storedAttemptError{cause: err}
	}
	if _, err = notifications.PrepareRegistration(notifications.Registration{
		AdapterID: a.Route.AdapterID, RuntimeID: a.Route.RuntimeID,
		Key: a.Route.LocalKey, Name: a.DestinationName,
	}); err != nil {
		return notifications.Attempt{}, &storedAttemptError{cause: err}
	}
	if a.StartedAt.IsZero() || !a.Deadline.Equal(a.StartedAt.Add(notifications.SendTimeout)) {
		return notifications.Attempt{}, &storedAttemptError{cause: errors.New("invalid retained send deadline")}
	}
	if a.CompletedAt != nil {
		if a.CompletedAt.IsZero() {
			return notifications.Attempt{}, &storedAttemptError{cause: errors.New("missing retained completion time")}
		}
		if err = notifications.ValidateEvidence(notifications.Evidence{
			Outcome: a.Outcome, ReasonCode: a.ReasonCode, Receipt: a.Receipt,
		}); err != nil {
			return notifications.Attempt{}, &storedAttemptError{cause: err}
		}
	}
	return a, nil
}
