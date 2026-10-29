package sqlite

import (
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func commandFromRow(row dbsqlc.Command) (devices.CommandRecord, error) {
	if err := validateStoredCommandIdentities(row); err != nil {
		return devices.CommandRecord{}, &storedDataError{cause: err}
	}
	requestedAt, err := parseTime(row.RequestedAt)
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("parse command requested_at: %w", err)
	}
	deadlineAt, err := parseTime(row.DeadlineAt)
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("parse command deadline_at: %w", err)
	}
	acceptedAt, err := parseOptionalTime(row.AcceptedAt)
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("parse command accepted_at: %w", err)
	}
	completedAt, err := parseOptionalTime(row.CompletedAt)
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("parse command completed_at: %w", err)
	}
	command := devices.CommandRecord{
		ID:            devices.CommandID(row.ID),
		EntityID:      devices.EntityID(row.EntityID),
		AdapterID:     row.AdapterID,
		OperationName: devices.OperationName(row.Operation),
		Parameters:    devices.CommandParameters(json.RawMessage(row.ParametersJson)),
		CorrelationID: devices.CorrelationID(row.CorrelationID),
		Status:        devices.CommandStatus(row.Status),
		RequestedAt:   requestedAt,
		DeadlineAt:    deadlineAt,
		AcceptedAt:    acceptedAt,
		CompletedAt:   completedAt,
	}
	if row.RuntimeID.Valid {
		id := adapters.RuntimeID(row.RuntimeID.String)
		command.RuntimeID = &id
	}
	if row.OutcomeObservationID.Valid {
		id := devices.ObservationID(row.OutcomeObservationID.String)
		command.OutcomeObservationID = &id
	}
	if row.FailureCode.Valid {
		code := devices.CommandFailureCode(row.FailureCode.String)
		command.FailureCode = &code
	}
	return command, nil
}

// Foreign keys establish referenced rows, not canonical UUIDv7 identities.
// SQL only constrains the prefixes of Command and correlation identities.
func validateStoredCommandIdentities(row dbsqlc.Command) error {
	if _, err := devices.ParseCommandID(row.ID); err != nil {
		return err
	}
	if _, err := devices.ParseCorrelationID(row.CorrelationID); err != nil {
		return err
	}
	if _, err := devices.ParseEntityID(row.EntityID); err != nil {
		return err
	}
	if row.RuntimeID.Valid {
		if _, err := adapters.ParseRuntimeID(row.RuntimeID.String); err != nil {
			return err
		}
	}
	if row.OutcomeObservationID.Valid {
		if _, err := devices.ParseObservationID(row.OutcomeObservationID.String); err != nil {
			return err
		}
	}
	return nil
}
